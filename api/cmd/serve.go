package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/signal"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	regularrp "github.com/catalystcommunity/linkkeys/sdks/regular-rp/go"
	api "github.com/catalystcommunity/linkkeys/sdks/regular-rp/go/generated"
	"github.com/catalystcommunity/tinku/api/internal/config"
	"github.com/catalystcommunity/tinku/api/internal/csilservices"
	"github.com/catalystcommunity/tinku/api/internal/federation"
	"github.com/catalystcommunity/tinku/api/internal/linkkeys"
	"github.com/catalystcommunity/tinku/api/internal/metrics"
	"github.com/catalystcommunity/tinku/api/internal/server"
	"github.com/catalystcommunity/tinku/api/internal/store"
	// Links both store implementations in, so a --db-uri of either scheme
	// resolves to a backend.
	_ "github.com/catalystcommunity/tinku/api/internal/store/backends"
	"github.com/catalystcommunity/tinku/coredb"
)

// sessionSweepInterval is how often expired sessions are deleted. Nothing
// depends on the sweep for correctness — every read already filters on
// expires_at — so this only has to be often enough that the table does not
// grow without bound.
const sessionSweepInterval = time.Hour

// Serve runs the API as a service.
//
// The boot order is the whole point of this function:
//
//  1. Start the OPS listener first, before anything that can be slow. From
//     this moment /healthz answers 200 (the process is alive) and /readyz
//     answers 503 with what is being waited on. An orchestrator can see the
//     difference between "still starting" and "broken" for the whole of a
//     slow start, instead of getting connection refused.
//  2. Apply migrations, and then verify none are pending — including when
//     --skip-migrate said not to apply them. A binary whose schema is behind
//     it must not serve traffic.
//  3. Open the store and confirm it answers.
//  4. Only now open the readiness gate and start the API listener.
//
// Shutdown reverses it: SIGINT or SIGTERM closes the readiness gate first,
// so load balancers stop sending work, then drains the API, then the ops
// listener last so probes keep answering to the end.
func Serve(flags map[string]string) error {
	log.SetFormatter(&log.JSONFormatter{})
	cfg := config.Load(flags)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ---- 1. ops listener, before anything slow ----
	readiness := metrics.NewReadiness("starting up")
	ops := metrics.NewOpsServer(cfg.OpsPort, readiness)
	opsErr := make(chan error, 1)
	go func() { opsErr <- ops.ListenAndServe(context.WithoutCancel(ctx)) }()

	// ---- 2. migrations, then proof that the schema is current ----
	readiness.NotReady("applying database migrations")
	metrics.MigrationsPending.Set(1)
	if cfg.MigrateOnBoot {
		log.WithField("db_uri", redact(cfg.DBURI)).Info("applying database migrations")
		if err := coredb.Up(cfg.DBURI); err != nil {
			return fmt.Errorf("applying migrations: %w", err)
		}
	} else {
		log.Warn("--skip-migrate: not applying migrations; readiness still waits for the schema to be current")
	}

	// Checked whether or not we applied them. A `serve --skip-migrate`
	// against a database an operator has not migrated yet must wait here
	// rather than serve queries against a schema the code does not match.
	switch err := awaitMigrations(ctx, cfg, readiness); {
	case errors.Is(err, errShutdownWhileWaiting):
		// Being told to stop while waiting is a clean stop, not a failure —
		// exiting non-zero here would make an ordinary scale-down or a
		// Ctrl-C look like a crash.
		log.Info("shut down before the schema was current")
		return nil
	case err != nil:
		return err
	}
	metrics.MigrationsPending.Set(0)
	log.Info("database schema is current")

	// ---- 3. store ----
	readiness.NotReady("connecting to the database")
	st, err := store.Open(cfg.DBURI)
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer st.Close()

	go sweepExpiredSessions(ctx, st)

	// ---- 4. services, readiness, API listener ----
	sink := server.NewSessionSink()
	pki := buildPKIClient(cfg)
	svcs := server.Services{
		Auth:     &csilservices.AuthService{Store: st, Cfg: cfg, PKI: pki, Sink: sink},
		Greeting: &csilservices.GreetingService{Store: st},
		// OriginDomain is the domain half of every organization and gathering
		// address this node mints, so the two services that mint one both
		// carry it.
		Organization: &csilservices.OrganizationService{Store: st, OriginDomain: cfg.OriginDomain()},
		Gathering:    &csilservices.GatheringService{Store: st, OriginDomain: cfg.OriginDomain()},
		Event:        &csilservices.EventService{Store: st, OriginDomain: cfg.OriginDomain()},
		Search:       &csilservices.SearchService{Store: st, OriginDomain: cfg.OriginDomain()},
		Admin:        &csilservices.AdminService{Store: st},
	}
	// Federation is off unless it is switched on AND this instance has an
	// account of its own to sign with. Both are required: an instance with
	// no address cannot sign, so registering the service would only produce
	// ops that always fail.
	if cfg.FederationAllowed() {
		crypto, err := buildFederationCrypto(cfg)
		if err != nil {
			return err
		}
		publisher := &federation.Publisher{
			Store:         st,
			Signer:        crypto.Signer,
			PublicBaseURL: cfg.FederationBaseURL(),
			OriginDomain:  cfg.OriginDomain(),
		}
		sender := &federation.Sender{
			Store:         st,
			FailureWindow: cfg.FederationFailureWindow,
			BaseDelay:     cfg.FederationRetryBase,
			MaxDelay:      cfg.FederationRetryMax,
			RateDelay:     cfg.FederationRateDelay,
		}
		svcs.Federation = &csilservices.FederationService{
			Store: st, OriginDomain: cfg.OriginDomain(),
			Signer: crypto.Signer, Verifier: crypto.Verifier, PeeringVerifier: crypto.PeeringVerifier,
			SubjectUserID: crypto.Identity.SubjectUserID, SubjectDomain: crypto.Identity.SubjectDomain,
			ApplicationID: crypto.Identity.ApplicationID, InstanceID: crypto.Identity.InstanceID,
		}
		svcs.Event = &csilservices.EventService{Store: st, OriginDomain: cfg.OriginDomain(), Publisher: publisher}
		if crypto.Enroller != nil {
			go crypto.Enroller.RunRenewalLoop(ctx, 15*time.Minute)
		}
		log.WithFields(log.Fields{
			"address":        crypto.Signer.Address(),
			"algorithm":      crypto.Signer.Algorithm(),
			"failure_window": cfg.FederationFailureWindow,
			"base_url":       cfg.FederationBaseURL(),
		}).Info("federation enabled")

		// The sender starts only once there is something for it to send
		// for. It drains the outbox on a timer, exactly as the session
		// sweep does.
		go sender.Run(ctx, cfg.FederationPollInterval)
		go sweepExpiredRemoteEvents(ctx, st)
	}

	if cfg.DevAuthAllowed() {
		log.WithField("env", cfg.Env).
			Warn("DEV AUTH ENABLED: devauth.dev-login mints sessions with no identity assertion")
		svcs.DevAuth = &csilservices.DevAuthService{Store: st, Cfg: cfg, Sink: sink}
	}
	if pki == nil && svcs.DevAuth == nil {
		log.Warn("linkkeys is not configured and dev auth is off: nobody can log in. " +
			"Reads still work; set the TINKU_LINKKEYS_* settings or pass --dev-auth.")
	}

	// The gate's ongoing check is a database ping, so readiness keeps
	// tracking the dependency instead of latching true at boot.
	readiness.Ready(st.Ping)
	log.WithFields(log.Fields{"api_port": cfg.APIPort, "ops_port": cfg.OpsPort, "env": cfg.Env}).
		Info("tinku-api ready")

	apiSrv := server.New(cfg, st, pki, svcs)
	apiErr := make(chan error, 1)
	go func() { apiErr <- apiSrv.ListenAndServe(ctx) }()

	// ---- shutdown ----
	select {
	case err := <-apiErr:
		readiness.NotReady("the API listener stopped")
		if err != nil {
			return fmt.Errorf("api listener: %w", err)
		}
	case err := <-opsErr:
		// The ops listener failing is fatal: without it nothing can tell
		// whether this process is healthy, so running on would be worse
		// than stopping.
		return fmt.Errorf("ops listener: %w", err)
	case <-ctx.Done():
		log.Info("shutdown signal received: draining")
		readiness.NotReady("shutting down")
		// Give load balancers a moment to act on the failing readiness
		// probe before in-flight requests start being refused.
		time.Sleep(shutdownGrace)
		stop()
		if err := <-apiErr; err != nil {
			return fmt.Errorf("draining api listener: %w", err)
		}
	}

	log.Info("tinku-api stopped")
	return nil
}

// shutdownGrace is how long readiness reports false before the API starts
// draining, so a load balancer has a chance to stop sending work first.
const shutdownGrace = 2 * time.Second

// awaitMigrations blocks until no migration is pending, or ctx is canceled.
// It polls rather than failing outright, because the case it exists for —
// `--skip-migrate` in a deployment where migrations are a separate release
// step — is one where the schema legitimately arrives a few seconds after
// the pod does.
func awaitMigrations(ctx context.Context, cfg config.Config, readiness *metrics.Readiness) error {
	const pollInterval = 2 * time.Second

	for attempt := 0; ; attempt++ {
		pending, err := coredb.Pending(cfg.DBURI)
		if err == nil && !pending {
			return nil
		}

		switch {
		case err != nil:
			readiness.NotReady("cannot reach the database: " + err.Error())
			// Logged at intervals rather than every poll: a database that
			// is down for a minute should not produce thirty identical
			// lines.
			if attempt%15 == 0 {
				log.WithError(err).Warn("waiting for the database")
			}
		default:
			readiness.NotReady("database migrations are pending")
			if attempt%15 == 0 {
				log.Warn("waiting for pending migrations to be applied")
			}
		}

		select {
		case <-ctx.Done():
			return errShutdownWhileWaiting
		case <-time.After(pollInterval):
		}
	}
}

// errShutdownWhileWaiting reports that awaitMigrations stopped because the
// process was asked to shut down, not because anything failed.
var errShutdownWhileWaiting = errors.New("shut down while waiting for migrations")

// federationCrypto bundles what buildFederationCrypto picks: the signer,
// the two context-scoped verifiers (see federation.BatchSignatureTag /
// PeeringSignatureTag), this instance's own canonical identity (empty
// under the dev scheme, which authenticates nothing and so has none worth
// reporting), and — under the real scheme only — the Enroller that keeps
// this instance's own attestations current.
type federationCrypto struct {
	Signer          federation.Signer
	Verifier        federation.Verifier
	PeeringVerifier federation.Verifier
	Identity        federation.KeyIdentity
	Enroller        *federation.Enroller
}

// buildFederationCrypto picks the signing scheme.
//
// TINKU_FEDERATION_SIGNING_KEYS empty selects the development scheme: it
// authenticates NOTHING, exists so the queue, the retry rules, the peering
// flow and the receiving side can be exercised end to end without a
// linkkeys deployment, and refuses to construct outside a development
// environment — so it cannot be what is running in production by
// accident.
//
// TINKU_FEDERATION_SIGNING_KEYS set selects the real scheme: this
// instance's own Ed25519 keyring (federation.LocalKeyring) signs, and a
// peer verifies against a short-lived linkkeys application-key attestation
// resolved through this instance's own RP — see docs/application-keys.md
// in the linkkeys repo for the protocol.
func buildFederationCrypto(cfg config.Config) (federationCrypto, error) {
	if cfg.FederationSigningKeys == "" {
		signer, err := federation.NewDevSigner(cfg.FederationAddress(), cfg.Env)
		if err != nil {
			return federationCrypto{}, fmt.Errorf("federation is enabled but has no usable signing scheme: %w", err)
		}
		verifier, err := federation.NewDevVerifier(cfg.Env)
		if err != nil {
			return federationCrypto{}, fmt.Errorf("federation is enabled but has no usable signing scheme: %w", err)
		}
		log.WithField("algorithm", signer.Algorithm()).
			Warn("FEDERATION IS UNSIGNED: deliveries are not authenticated; this scheme is for development only")
		return federationCrypto{Signer: signer, Verifier: verifier, PeeringVerifier: verifier}, nil
	}

	if cfg.FederationSubjectUserID == "" || cfg.FederationApplicationID == "" || cfg.FederationInstanceID == "" {
		return federationCrypto{}, fmt.Errorf(
			"federation: the real signing scheme needs TINKU_FEDERATION_SUBJECT_USER_ID, " +
				"TINKU_FEDERATION_APPLICATION_ID and TINKU_FEDERATION_INSTANCE_ID")
	}
	keyring, err := federation.LoadKeyring([]byte(cfg.FederationSigningKeys))
	if err != nil {
		return federationCrypto{}, fmt.Errorf("federation: loading the signing keyring: %w", err)
	}
	identity := federation.KeyIdentity{
		SubjectUserID: cfg.FederationSubjectUserID,
		SubjectDomain: cfg.OriginDomain(),
		ApplicationID: cfg.FederationApplicationID,
		InstanceID:    cfg.FederationInstanceID,
	}

	rpTransport, err := regularrp.NewPinnedRpcTransport(regularrp.PinnedRpcTransportOptions{
		TCPAddress:   cfg.FederationRPTCPAddress,
		Fingerprints: cfg.FederationRPFingerprints,
		APIKey:       cfg.FederationRPAPIKey,
	})
	if err != nil {
		return federationCrypto{}, fmt.Errorf("federation: configuring the RP transport: %w", err)
	}
	resolver := regularrp.NewCachedResolver(rpTransport, regularrp.CachedResolverOptions{})

	crypto := federationCrypto{
		Signer:          keyring,
		Verifier:        &federation.ApplicationKeyVerifier{Resolver: resolver, Context: federation.BatchSignatureTag},
		PeeringVerifier: &federation.ApplicationKeyVerifier{Resolver: resolver, Context: federation.PeeringSignatureTag},
		Identity:        identity,
	}

	// The home-domain transport is needed only for THIS instance's own
	// renewals, which are optional: an operator who renews out of band
	// (or has not yet configured it) still gets a working signer and
	// verifier. Log rather than fail closed, because the alternative —
	// refusing to start federation at all — would be worse than a
	// deployment that must renew by hand until it is configured.
	if cfg.FederationHomeDomainTCPAddress != "" && len(cfg.FederationHomeDomainFingerprints) > 0 {
		homeTransport, err := regularrp.NewPinnedRpcTransport(regularrp.PinnedRpcTransportOptions{
			TCPAddress:   cfg.FederationHomeDomainTCPAddress,
			Fingerprints: cfg.FederationHomeDomainFingerprints,
			// The add-key/renew-attestation/start-key-challenge operations
			// take no API key (the signed request is the authentication,
			// per docs/application-keys.md's Operations table); this
			// transport still needs a non-empty value to construct, and
			// the home domain does not consult it for these ops.
			APIKey: "unused-for-application-key-operations",
		})
		if err != nil {
			log.WithError(err).Warn("federation: could not configure the home-domain transport; this instance will not renew its own attestations")
		} else {
			crypto.Enroller = &federation.Enroller{
				Client:   api.NewApplicationKeysClient(homeTransport),
				Keyring:  keyring,
				Instance: regularrp.InstanceRef(identity),
			}
		}
	} else {
		log.Warn("federation: TINKU_FEDERATION_HOME_DOMAIN_TCP_ADDRESS/_FINGERPRINTS not set; " +
			"this instance will not renew its own attestations automatically")
	}

	return crypto, nil
}

// sweepExpiredRemoteEvents drops what peers sent once it is well over.
//
// Without it a directory grows forever: a busy peer publishes every week
// and nothing ever removes what has happened. The cutoff is read from the
// instance settings on each pass rather than captured at boot, so changing
// the retention takes effect without a restart.
//
// It sweeps on ends_at, not received_at: what matters is that the event is
// over, not when this instance heard about it.
func sweepExpiredRemoteEvents(ctx context.Context, st store.Store) {
	const interval = time.Hour
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			settings, err := st.InstanceSettings(ctx)
			if err != nil {
				log.WithError(err).Warn("federation: could not read the retention setting")
				continue
			}
			// Zero keeps everything. An instance that wants a permanent
			// archive says so by setting it, rather than by the sweep
			// happening not to run.
			if settings.RetentionDays <= 0 {
				continue
			}
			// Remembered batch ids go too. Anything older than the
			// freshness window would be refused on its timestamp anyway, so
			// keeping its id buys nothing and the table would grow forever.
			if forgotten, err := st.ForgetBatchesSeenBefore(
				ctx, time.Now().UTC().Add(-2*csilservices.FreshnessWindow),
			); err != nil {
				log.WithError(err).Warn("federation: could not forget old batch ids")
			} else if forgotten > 0 {
				log.WithField("forgotten", forgotten).Info("federation: forgot old batch ids")
			}

			cutoff := time.Now().UTC().AddDate(0, 0, -int(settings.RetentionDays))
			removed, err := st.DeleteRemoteEventsEndedBefore(ctx, cutoff)
			if err != nil {
				log.WithError(err).Warn("federation: retention sweep failed")
				continue
			}
			if removed > 0 {
				log.WithFields(log.Fields{"removed": removed, "cutoff": cutoff}).
					Info("federation: retention sweep")
			}
		}
	}
}

// sweepExpiredSessions deletes expired sessions on a timer. A failed sweep
// is logged and retried on the next tick; it is never fatal, because every
// session read already filters on expires_at, so a table that has not been
// swept is untidy rather than unsafe.
func sweepExpiredSessions(ctx context.Context, st store.Store) {
	ticker := time.NewTicker(sessionSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			deleted, err := st.DeleteExpiredSessions(ctx)
			if err != nil {
				log.WithError(err).Error("sweeping expired sessions")
				continue
			}
			if deleted > 0 {
				metrics.SessionsReaped.Add(float64(deleted))
				log.WithField("sessions", deleted).Info("swept expired sessions")
			}
		}
	}
}

// buildPKIClient constructs the linkkeys relying-party client, or returns
// nil when linkkeys is not fully configured. Nil is a supported state, not a
// failure: begin-login and the callback then refuse with a clear message
// while everything else keeps working.
func buildPKIClient(cfg config.Config) csilservices.PKIClient {
	if !cfg.LinkkeysConfigured() {
		return nil
	}
	log.WithFields(log.Fields{"idp": cfg.LinkkeysIDPDomain, "rp": cfg.LinkkeysRPDomain()}).
		Info("linkkeys relying party configured (http transport)")
	return linkkeys.New(cfg.LinkkeysPKIURL, cfg.LinkkeysPKIAPIKey, cfg.LinkkeysPKIAllowInvalidCerts)
}

// redact strips the password from a database URI so it can be logged. A URI
// that will not parse is reported as its scheme alone rather than printed —
// an unparseable URI is exactly the case where a password is most likely to
// be in an unexpected position.
func redact(dbURI string) string {
	parsed, err := url.Parse(dbURI)
	if err != nil {
		scheme, _, _ := strings.Cut(dbURI, ":")
		return scheme + ":(unparseable)"
	}
	if parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); hasPassword {
			parsed.User = url.UserPassword(parsed.User.Username(), "xxxxx")
		}
	}
	return parsed.String()
}
