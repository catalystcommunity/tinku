// Package config loads tinku-api's settings. Every setting has an
// TINKU_* environment variable; the ones an operator changes per-run also
// have a `tinku serve` flag, and the flag wins over the environment.
//
// Config is a value, not a package-level singleton, so tests construct
// exactly the settings they need instead of mutating global state.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// Config is tinku-api's full runtime configuration.
type Config struct {
	// Env names the deployment: "dev", "nonprod" or "prod".
	//
	// It DEFAULTS TO PROD. An unconfigured deployment is therefore the safe
	// one, and every permissive path — dev login, the unsigned federation
	// scheme — has to be asked for. The opposite default would mean a
	// missing environment variable quietly produced an instance with its
	// guards off, which is exactly the mistake nobody notices until it is
	// running somewhere real.
	//
	// It gates
	// DevAuthEnabled and nothing else.
	Env string

	// APIPort is the port serving CSIL-RPC and the auth callback.
	APIPort int
	// OpsPort is the port serving /metrics, /healthz and /readyz. It is a
	// separate listener from the API on purpose: metrics and probes must
	// stay reachable when the API is refusing traffic, and the ops port is
	// never exposed outside the cluster.
	OpsPort int

	// DBURI selects the backend as well as the database — a postgresql://
	// URI runs the Postgres implementation and its migration tree, a
	// sqlite: URI the SQLite ones.
	DBURI string
	// MigrateOnBoot runs coredb's migrations before `serve` reports ready.
	// Defaults to true. Set it false where migrations are a separate
	// release step; readiness then waits for that step to have happened,
	// because serve still refuses to report ready with migrations pending.
	MigrateOnBoot bool

	// CORSOrigins is the allow-list passed to rs/cors. The default of "*"
	// is right for local dev and wrong for anything else.
	CORSOrigins []string

	// SessionTTL is how long a freshly minted session stays valid.
	SessionTTL time.Duration
	// SessionCookieSecure sets the Secure attribute on the session cookie.
	// Defaults to true; a plain-HTTP local run over http://localhost needs
	// it false, which is the only reason it is configurable.
	SessionCookieSecure bool

	// LinkkeysDomain is tinku's own relying-party DNS identity — the
	// audience linkkeys binds each assertion to.
	LinkkeysDomain string
	// LinkkeysIDPDomain and LinkkeysIDPURL identify the identity provider
	// the login flow redirects to.
	LinkkeysIDPDomain string
	LinkkeysIDPURL    string
	// LinkkeysPKIURL, LinkkeysPKIAPIKey and LinkkeysPKIAllowInvalidCerts
	// configure the relying-party sidecar that holds the private keys. The
	// api never touches a private key itself.
	LinkkeysPKIURL               string
	LinkkeysPKIAPIKey            string
	LinkkeysPKIAllowInvalidCerts bool

	// AppCallbackURL is the absolute URL the identity provider redirects
	// back to: tinku-api's own `GET /auth/callback`, not a SPA route. It
	// is what the signed auth request is bound to.
	AppCallbackURL string
	// PostLoginRedirectURL is where /auth/callback sends the browser once
	// it has set the session cookie.
	PostLoginRedirectURL string
	// SessionNonceSecret HMAC-signs the self-verifying login nonce that
	// round-trips through the RP and IDP inside the assertion. When unset,
	// Load generates a random per-process value so a local run works with
	// no configuration; every nonce minted within one process still
	// verifies, but a multi-replica or restart-heavy deployment MUST set
	// TINKU_SESSION_NONCE_SECRET or logins fail nonce verification
	// intermittently.
	SessionNonceSecret string

	// OriginDomainOverride forces the origin domain instead of deriving it
	// from the linkkeys identity. It exists for a local run with no
	// linkkeys configured at all, and for nothing else — see the
	// OriginDomain method.
	OriginDomainOverride string

	// FederationEnabled switches the whole federation surface on. Off by
	// default: an instance that has not been asked to talk to anybody
	// should not have the service registered at all.
	FederationEnabled bool

	// FederationHandle is the local part of this instance's own account.
	// The full address is FederationHandle@OriginDomain, which is what a
	// peer knows this instance as and what a signature is checked against.
	FederationHandle string

	// PublicBaseURL is where a reader reaches this instance. A delivery
	// carries a link back to it, because a delivery is a summary and the
	// origin is the only place the whole thing lives.
	PublicBaseURL string

	// FederationFailureWindow is how long delivery to one peer may keep
	// failing before that peer is suspended. Retries then stop until an
	// administrator restarts it.
	FederationFailureWindow time.Duration

	// FederationPollInterval is how often the sender drains the outbox.
	FederationPollInterval time.Duration

	// The retry policy. It is OURS: a peer may be another implementation
	// of this API entirely, and nothing in the protocol tells us how it
	// wants to be retried. These say how patiently this instance waits,
	// not what any peer agreed to.
	//
	// FederationRetryBase is the wait before a first retry after a
	// FAILURE; each further attempt doubles it, up to FederationRetryMax.
	FederationRetryBase time.Duration
	FederationRetryMax  time.Duration
	// FederationRateDelay is the wait after a peer refuses an event for
	// RATE. Flat rather than exponential, because that is backpressure and
	// not a fault.
	FederationRateDelay time.Duration

	// FederationSigningKeys is this instance's OWN Ed25519 federation
	// signing keyring, as JSON produced by federation.LocalKeyring.MarshalSecret
	// (see `tinku federation generate-keys`). It is a SECRET: private key
	// seeds, not just public material. It belongs in whatever secret store
	// the deployment already uses — the same convention this repository's
	// other secrets follow (TINKU_SESSION_NONCE_SECRET,
	// TINKU_LINKKEYS_PKI_API_KEY): one environment variable, sourced from
	// secret storage, never written to ordinary config or a log.
	//
	// Empty means the development scheme (DevSigner/DevVerifier, which
	// authenticates nothing and refuses outside a dev environment) is
	// used instead. Set this to switch a deployment to the real linkkeys
	// application-key scheme — see docs/OPERATING.md, "Federation
	// signing keys".
	FederationSigningKeys string
	// FederationSubjectUserID, FederationApplicationID and
	// FederationInstanceID are this instance's own canonical linkkeys
	// identity: the account UUID that enrolled it, the application id
	// (normally "tinku"), and the application-instance id linkkeys
	// assigned at enrollment (Account/enroll-application-instance). All
	// three, plus the origin domain, are required whenever
	// FederationSigningKeys is set.
	FederationSubjectUserID string
	FederationApplicationID string
	FederationInstanceID    string
	// FederationRPTCPAddress, FederationRPFingerprints and
	// FederationRPAPIKey configure this instance's own regular RP: where
	// Rp/resolve-application-keys is asked for a PEER's cached, verified
	// application keys. Required whenever FederationSigningKeys is set —
	// the real scheme cannot verify an incoming delivery without it. See
	// docs/application-keys.md's "RP-facing operations".
	FederationRPTCPAddress   string
	FederationRPFingerprints []string
	FederationRPAPIKey       string
	// FederationHomeDomainTCPAddress and FederationHomeDomainFingerprints
	// point at this instance's OWN home domain, for the enrollment and
	// renewal operations it calls directly (ApplicationKeys/add-key,
	// /renew-attestation, /start-key-challenge — all unauthenticated by
	// API key; the signed request is the authentication, per
	// docs/application-keys.md's Operations table). Required whenever
	// FederationSigningKeys is set and this instance renews its own keys.
	FederationHomeDomainTCPAddress   string
	FederationHomeDomainFingerprints []string

	// DevAuthEnabled exposes DevAuthService.dev-login, which mints a
	// session with no identity assertion at all. It takes effect only when
	// Env is dev or nonprod — see DevAuthAllowed.
	DevAuthEnabled bool
}

// Load reads Config from TINKU_* environment variables, then applies any
// flags that override them. Pass the flag map from cmd's parser; pass nil
// for environment only.
func Load(flags map[string]string) Config {
	cfg := Config{
		Env:     getEnv("TINKU_ENV", "prod"),
		APIPort: getEnvInt("TINKU_API_PORT", 8080),
		OpsPort: getEnvInt("TINKU_OPS_PORT", 9090),

		DBURI:         getEnv("TINKU_DB_URI", "postgresql://tinku:devpass123@localhost:5432/tinku_db?sslmode=disable"),
		MigrateOnBoot: getEnvBool("TINKU_MIGRATE_ON_BOOT", true),

		CORSOrigins: getEnvList("TINKU_CORS_ORIGINS", []string{"*"}),

		SessionTTL:          getEnvDuration("TINKU_SESSION_TTL", 720*time.Hour),
		SessionCookieSecure: getEnvBool("TINKU_SESSION_COOKIE_SECURE", true),

		LinkkeysDomain:               getEnv("TINKU_LINKKEYS_DOMAIN", ""),
		LinkkeysIDPDomain:            getEnv("TINKU_LINKKEYS_IDP_DOMAIN", ""),
		LinkkeysIDPURL:               getEnv("TINKU_LINKKEYS_IDP_URL", ""),
		LinkkeysPKIURL:               getEnv("TINKU_LINKKEYS_PKI_URL", ""),
		LinkkeysPKIAPIKey:            getEnv("TINKU_LINKKEYS_PKI_API_KEY", ""),
		LinkkeysPKIAllowInvalidCerts: getEnvBool("TINKU_LINKKEYS_PKI_ALLOW_INVALID_CERTS", false),

		AppCallbackURL:       getEnv("TINKU_APP_CALLBACK_URL", ""),
		PostLoginRedirectURL: getEnv("TINKU_POST_LOGIN_REDIRECT_URL", "/"),
		SessionNonceSecret:   getEnv("TINKU_SESSION_NONCE_SECRET", ""),

		OriginDomainOverride: getEnv("TINKU_ORIGIN_DOMAIN", ""),

		FederationEnabled:       getEnvBool("TINKU_FEDERATION_ENABLED", false),
		FederationHandle:        getEnv("TINKU_FEDERATION_HANDLE", ""),
		PublicBaseURL:           getEnv("TINKU_PUBLIC_BASE_URL", ""),
		FederationFailureWindow: getEnvDuration("TINKU_FEDERATION_FAILURE_WINDOW", 24*time.Hour),
		FederationPollInterval:  getEnvDuration("TINKU_FEDERATION_POLL_INTERVAL", 30*time.Second),
		FederationRetryBase:     getEnvDuration("TINKU_FEDERATION_RETRY_BASE", 30*time.Second),
		FederationRetryMax:      getEnvDuration("TINKU_FEDERATION_RETRY_MAX", time.Hour),
		FederationRateDelay:     getEnvDuration("TINKU_FEDERATION_RATE_DELAY", 70*time.Second),

		FederationSigningKeys:            getEnv("TINKU_FEDERATION_SIGNING_KEYS", ""),
		FederationSubjectUserID:          getEnv("TINKU_FEDERATION_SUBJECT_USER_ID", ""),
		FederationApplicationID:          getEnv("TINKU_FEDERATION_APPLICATION_ID", "tinku"),
		FederationInstanceID:             getEnv("TINKU_FEDERATION_INSTANCE_ID", ""),
		FederationRPTCPAddress:           getEnv("TINKU_FEDERATION_RP_TCP_ADDRESS", ""),
		FederationRPFingerprints:         getEnvList("TINKU_FEDERATION_RP_FINGERPRINTS", nil),
		FederationRPAPIKey:               getEnv("TINKU_FEDERATION_RP_API_KEY", ""),
		FederationHomeDomainTCPAddress:   getEnv("TINKU_FEDERATION_HOME_DOMAIN_TCP_ADDRESS", ""),
		FederationHomeDomainFingerprints: getEnvList("TINKU_FEDERATION_HOME_DOMAIN_FINGERPRINTS", nil),

		DevAuthEnabled: getEnvBool("TINKU_DEV_AUTH_ENABLED", false),
	}
	cfg.applyFlags(flags)

	if cfg.SessionNonceSecret == "" {
		cfg.SessionNonceSecret = generateSecret("TINKU_SESSION_NONCE_SECRET")
	}
	return cfg
}

// applyFlags overrides the loaded settings with any flag the operator
// passed. Only the settings worth changing per-run have a flag; everything
// else stays environment-only and is listed in the CLI's usage text.
func (c *Config) applyFlags(flags map[string]string) {
	for name, value := range flags {
		switch name {
		case "origin-domain-override":
			c.OriginDomainOverride = value
		case "env":
			c.Env = value
		case "api-port":
			c.APIPort = atoiOr(value, c.APIPort)
		case "ops-port":
			c.OpsPort = atoiOr(value, c.OpsPort)
		case "db-uri":
			c.DBURI = value
		case "skip-migrate":
			// Presence alone means true (`--skip-migrate`), and
			// `--skip-migrate=false` still turns migrations back on.
			c.MigrateOnBoot = !parseBoolOr(value, true)
		case "cors-origins":
			c.CORSOrigins = splitList(value, c.CORSOrigins)
		case "session-cookie-insecure":
			c.SessionCookieSecure = !parseBoolOr(value, true)
		case "linkkeys-domain":
			c.LinkkeysDomain = value
		case "linkkeys-idp-domain":
			c.LinkkeysIDPDomain = value
		case "linkkeys-idp-url":
			c.LinkkeysIDPURL = value
		case "linkkeys-pki-url":
			c.LinkkeysPKIURL = value
		case "linkkeys-pki-api-key":
			c.LinkkeysPKIAPIKey = value
		case "app-callback-url":
			c.AppCallbackURL = value
		case "post-login-redirect-url":
			c.PostLoginRedirectURL = value
		case "federation":
			c.FederationEnabled = parseBoolOr(value, true)
		case "federation-handle":
			c.FederationHandle = value
		case "public-base-url":
			c.PublicBaseURL = value
		case "dev-auth":
			c.DevAuthEnabled = parseBoolOr(value, true)
		}
	}
}

// OriginDomain is the domain half of every organization and gathering
// address this node mints (`slug@origin_domain`), and the identity this
// node presents to a peer.
//
// It is DERIVED from the linkkeys identity rather than configured on its
// own. This instance is itself a linkkeys account — it has to be, because
// authenticating a linkkeys user means both parties know each other's
// domain and account. The domain that owns this instance's account is
// therefore already the honest answer to "who is this node", and a second
// setting could only ever disagree with it.
//
// The website's own hostname is not the answer. An instance served from
// tinkudomain.com whose linkkeys account lives on mylinkkeys.com is
// `something@mylinkkeys.com` to every peer, because that is the name a peer
// can verify.
//
// OriginDomainOverride wins when it is set, for a local run with no
// linkkeys configuration; "localhost" is the last resort, which keeps a
// bare `./tools.sh serve-local` working.
func (c Config) OriginDomain() string {
	if c.OriginDomainOverride != "" {
		return c.OriginDomainOverride
	}
	if domain := c.LinkkeysRPDomain(); domain != "" {
		return domain
	}
	return "localhost"
}

// FederationAddress is this instance's own account, `handle@domain`. It is
// the name a peer stores, and the name a signature is checked against.
func (c Config) FederationAddress() string {
	if c.FederationHandle == "" {
		return ""
	}
	return strings.ToLower(c.FederationHandle) + "@" + strings.ToLower(c.OriginDomain())
}

// FederationAllowed reports whether the federation service should be
// registered. It needs the switch AND an address: an instance with no
// account of its own cannot sign anything, so registering the service would
// only produce ops that always fail.
func (c Config) FederationAllowed() bool {
	return c.FederationEnabled && c.FederationHandle != ""
}

// FederationBaseURL is the link a delivery carries back to this instance.
// It falls back to https on the origin domain, which is right whenever the
// website and the linkkeys account share a name and wrong quietly otherwise
// — so a deployment where they differ has to say so.
func (c Config) FederationBaseURL() string {
	if c.PublicBaseURL != "" {
		return strings.TrimRight(c.PublicBaseURL, "/")
	}
	return "https://" + c.OriginDomain()
}

// LinkkeysRPDomain is the audience tinku expects on an assertion. It
// prefers the explicit relying-party domain and falls back to the IDP
// domain, which is the same value in a single-IDP deployment — falling back
// keeps the audience check on instead of silently disabling it when only the
// IDP fields were configured.
func (c Config) LinkkeysRPDomain() string {
	if c.LinkkeysDomain != "" {
		return c.LinkkeysDomain
	}
	return c.LinkkeysIDPDomain
}

// LinkkeysConfigured reports whether the relying-party sidecar can be
// reached. When it is false, begin-login and the callback refuse; dev-auth
// (if its own gates pass) is then the only way to get a session.
func (c Config) LinkkeysConfigured() bool {
	return c.LinkkeysPKIURL != "" && c.LinkkeysIDPDomain != "" && c.LinkkeysIDPURL != "" && c.AppCallbackURL != ""
}

// DevAuthAllowed is the environment half of the dev-auth gate: the flag
// alone is not enough, the deployment must also be a dev or nonprod one.
// Both halves must pass before DevAuthService is registered at all.
func (c Config) DevAuthAllowed() bool {
	switch strings.ToLower(c.Env) {
	case "dev", "development", "nonprod", "test":
		return c.DevAuthEnabled
	default:
		return false
	}
}

func generateSecret(name string) string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// An empty secret would make every nonce unverifiable, so failing
		// loudly here is more honest than limping on.
		panic("config: could not generate a random " + name + ": " + err.Error())
	}
	log.Warnf("%s is unset: generated a random per-process secret. Fine for a local run; "+
		"set it explicitly for any deployment with more than one replica or that restarts often, "+
		"or logins will intermittently fail nonce verification.", name)
	return hex.EncodeToString(buf)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	return atoiOr(os.Getenv(key), fallback)
}

func atoiOr(value string, fallback int) int {
	if i, err := strconv.Atoi(value); err == nil {
		return i
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	return parseBoolOr(os.Getenv(key), fallback)
}

func parseBoolOr(value string, fallback bool) bool {
	if b, err := strconv.ParseBool(value); err == nil {
		return b
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return d
	}
	return fallback
}

func getEnvList(key string, fallback []string) []string {
	return splitList(os.Getenv(key), fallback)
}

func splitList(value string, fallback []string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}
