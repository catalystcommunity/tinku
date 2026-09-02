package server_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/config"
	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/csilservices"
	"github.com/catalystcommunity/tinku/api/internal/federation"
	"github.com/catalystcommunity/tinku/api/internal/server"
	"github.com/catalystcommunity/tinku/api/internal/store"
	_ "github.com/catalystcommunity/tinku/api/internal/store/backends"
	"github.com/catalystcommunity/tinku/api/internal/transport"
	"github.com/catalystcommunity/tinku/api/internal/webhooks"
	"github.com/catalystcommunity/tinku/coredb"
)

// testEnv is the real HTTP handler over a real SQLite database, with a clock
// the test controls. Nothing here is a mock — the migration, the store, the
// dispatcher, the CBOR envelopes and the cookie handling are all production
// code paths, which is only affordable because the SQLite backend needs no
// server.
//
// The controllable clock is what makes the start-time lock testable: it is a
// rule about the passage of time, and the only alternative to moving the
// clock is sleeping through it.
type testEnv struct {
	Server *httptest.Server
	Store  store.Store
	// DBURI lets a test open its own connection and read raw column text.
	// One invariant is only checkable that way: what is actually on disk.
	DBURI string

	// cfg and sink are kept so a test can rebuild the handler with more
	// services than the default set — EnableFederation does exactly that.
	cfg  config.Config
	sink csilservices.SessionSink

	// Notify is the dispatcher the services under test write through, so a
	// test can assert on what a change queued.
	Notify *webhooks.Dispatcher

	mu  sync.Mutex
	now time.Time
}

// sharedMigrations runs the migrations once for a shared database. A
// per-test SQLite file needs its own; one Postgres database serves the whole
// package, and migrating it per test would be both slow and pointless.
var sharedMigrations sync.Once

// testDBURI picks the database this run tests against.
//
// Default: a fresh SQLite file per test, which needs nothing installed.
// TINKU_TEST_DB_URI: whatever it names — `./tools.sh test-pg` points it at
// the compose Postgres, so the SAME end-to-end suite runs against the
// backend production uses. Two backends whose SQL is only ever exercised on
// one of them is how a dialect bug reaches a deployment.
func testDBURI(t *testing.T) string {
	t.Helper()
	if shared := os.Getenv("TINKU_TEST_DB_URI"); shared != "" {
		return shared
	}
	return "sqlite:" + filepath.Join(t.TempDir(), "test.db")
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	dbURI := testDBURI(t)
	if os.Getenv("TINKU_TEST_DB_URI") == "" {
		if err := coredb.Up(dbURI); err != nil {
			t.Fatalf("migrating test database: %v", err)
		}
	} else {
		// A shared database carries the previous test's rows. Migrate once,
		// then empty it — no test in this package runs in parallel, so the
		// emptying cannot race one that is still reading.
		var migrateErr error
		sharedMigrations.Do(func() { migrateErr = coredb.Up(dbURI) })
		if migrateErr != nil {
			t.Fatalf("migrating the shared test database: %v", migrateErr)
		}
		truncateAll(t, dbURI)
	}
	st, err := store.Open(dbURI)
	if err != nil {
		t.Fatalf("opening test store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	env := &testEnv{Store: st, DBURI: dbURI, now: time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)}

	cfg := config.Config{
		Env: "dev",
		// No linkkeys configuration in a test, so the override is what
		// gives this node a name — the same path a bare local run takes.
		OriginDomainOverride: "tinku.test",
		DBURI:                dbURI,
		CORSOrigins:          []string{"*"},
		SessionTTL:           time.Hour,
		SessionCookieSecure:  false, // httptest serves plain HTTP
		DevAuthEnabled:       true,
		SessionNonceSecret:   "test-secret",
	}

	sink := server.NewSessionSink()
	// The dispatcher is real, so a test can read the delivery rows a change
	// wrote. Nothing SENDS them here: webhooks.Sender is a separate loop
	// that a test starts for itself when it wants one.
	notify := &webhooks.Dispatcher{Store: st, OriginDomain: cfg.OriginDomain()}
	env.Notify = notify

	svcs := server.Services{
		Auth:         &csilservices.AuthService{Store: st, Cfg: cfg, Sink: sink},
		DevAuth:      &csilservices.DevAuthService{Store: st, Cfg: cfg, Sink: sink},
		Greeting:     &csilservices.GreetingService{Store: st},
		Organization: &csilservices.OrganizationService{Store: st, OriginDomain: cfg.OriginDomain(), Notify: notify},
		Gathering:    &csilservices.GatheringService{Store: st, OriginDomain: cfg.OriginDomain(), Notify: notify},
		Event:        &csilservices.EventService{Store: st, OriginDomain: cfg.OriginDomain(), Now: env.clock, Notify: notify},
		Search:       &csilservices.SearchService{Store: st, OriginDomain: cfg.OriginDomain(), Now: env.clock},
		Admin:        &csilservices.AdminService{Store: st},
		Webhook:      &csilservices.WebhookService{Store: st, Cfg: cfg},
	}

	env.cfg = cfg
	env.sink = sink
	env.Server = httptest.NewServer(server.New(cfg, st, nil, svcs).Handler())
	t.Cleanup(env.Server.Close)
	return env
}

// newLocalTestEnv is newTestEnv pinned to its own SQLite file, whatever
// TINKU_TEST_DB_URI says.
//
// A test that boots TWO instances needs two databases. The shared-database
// mode exists so one suite can run against Postgres; it cannot serve a test
// whose whole subject is two instances not sharing state.
func newLocalTestEnv(t *testing.T) *testEnv {
	t.Helper()
	t.Setenv("TINKU_TEST_DB_URI", "")
	return newTestEnv(t)
}

// EnableFederation rebuilds this instance's handler with federation on.
//
// The handler is replaced rather than patched: the routing table is built
// once from the service set, which is what makes "federation off" mean the
// service is absent from the wire rather than present and refusing.
func (e *testEnv) EnableFederation(t *testing.T, signer federation.Signer, verifier federation.Verifier) {
	t.Helper()
	e.EnableFederationFull(t, signer, verifier, verifier)
}

// EnableFederationFull is EnableFederation with the batch verifier and the
// peering verifier given separately — for a test that needs to prove the
// two signature contexts (BatchSignatureTag, PeeringSignatureTag) are
// actually kept apart. EnableFederation is the common case, where a single
// verifier (typically the context-blind dev scheme) serves both.
func (e *testEnv) EnableFederationFull(t *testing.T, signer federation.Signer, verifier, peeringVerifier federation.Verifier) {
	t.Helper()

	domain := strings.SplitN(signer.Address(), "@", 2)[1]
	publisher := &federation.Publisher{
		Store:         e.Store,
		Signer:        signer,
		PublicBaseURL: "https://" + domain,
		OriginDomain:  domain,
		Now:           e.clock,
	}
	// The dev scheme's Verifier ignores identity and context entirely
	// (see federation.DevVerifier), so this instance's "own identity" only
	// has to be a well-formed placeholder — it is never checked against
	// anything under this scheme. It exists here so FederationIdentity has
	// something non-empty to report while federation is on.
	identity := federation.KeyIdentity{
		SubjectUserID: "dev-user-" + domain,
		SubjectDomain: domain,
		ApplicationID: "tinku",
		InstanceID:    "dev-instance-" + domain,
	}
	svcs := server.Services{
		Auth:         &csilservices.AuthService{Store: e.Store, Cfg: e.cfg, Sink: e.sink},
		DevAuth:      &csilservices.DevAuthService{Store: e.Store, Cfg: e.cfg, Sink: e.sink},
		Greeting:     &csilservices.GreetingService{Store: e.Store},
		Organization: &csilservices.OrganizationService{Store: e.Store, OriginDomain: e.cfg.OriginDomain(), Notify: e.Notify},
		Gathering:    &csilservices.GatheringService{Store: e.Store, OriginDomain: e.cfg.OriginDomain(), Notify: e.Notify},
		Event:        &csilservices.EventService{Store: e.Store, OriginDomain: e.cfg.OriginDomain(), Publisher: publisher, Now: e.clock, Notify: e.Notify},
		Search:       &csilservices.SearchService{Store: e.Store, OriginDomain: e.cfg.OriginDomain(), Now: e.clock},
		Admin:        &csilservices.AdminService{Store: e.Store},
		Webhook:      &csilservices.WebhookService{Store: e.Store, Cfg: e.cfg},
		Federation: &csilservices.FederationService{
			Store: e.Store, OriginDomain: e.cfg.OriginDomain(),
			Signer: signer, Verifier: verifier, PeeringVerifier: peeringVerifier, Now: e.clock,
			SubjectUserID: identity.SubjectUserID, SubjectDomain: identity.SubjectDomain,
			ApplicationID: identity.ApplicationID, InstanceID: identity.InstanceID,
		},
	}

	e.Server.Close()
	e.Server = httptest.NewServer(server.New(e.cfg, e.Store, nil, svcs).Handler())
	t.Cleanup(e.Server.Close)
}

// truncateAll empties every table the domain writes, leaving the schema and
// the goose version alone. Listed parent-first with CASCADE, so the order
// does not have to track the foreign keys.
func truncateAll(t *testing.T, dbURI string) {
	t.Helper()
	target, err := coredb.ParseTarget(dbURI)
	if err != nil {
		t.Fatalf("parsing the test database URI: %v", err)
	}
	db, err := sql.Open(target.Driver, target.DSN)
	if err != nil {
		t.Fatalf("opening the test database: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	// webhooks is listed on its own: (owner_kind, owner_id) is a pair, not
	// a foreign key, so nothing cascades into it and rows would survive
	// into the next test on the shared Postgres run.
	statement := `TRUNCATE users, organizations, gatherings, federation_peers, webhooks RESTART IDENTITY CASCADE`
	if target.Dialect == coredb.DialectSQLite {
		statement = `DELETE FROM users; DELETE FROM organizations; DELETE FROM gatherings;
		             DELETE FROM federation_peers; DELETE FROM webhooks;`
	}
	if _, err := db.Exec(statement); err != nil {
		t.Fatalf("emptying the test database: %v", err)
	}
}

func (e *testEnv) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

// advance moves the clock forward. Every service reads the clock through
// the same function, so one call moves the whole system's idea of now.
func (e *testEnv) advance(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.now = e.now.Add(d)
}

// newClient returns a client with its own cookie jar — which is what makes
// it a separate person rather than a separate request.
func (e *testEnv) newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("building cookie jar: %v", err)
	}
	return &http.Client{Jar: jar}
}

// login mints a session for handle and returns the profile.
func (e *testEnv) login(t *testing.T, handle string) (*http.Client, csil.UserProfile) {
	t.Helper()
	client := e.newClient(t)
	resp := e.call(t, client, "devauth", "dev-login",
		csil.EncodeDevLoginRequest(csil.DevLoginRequest{Handle: handle, Domain: "example.test"}))
	requireReply(t, resp, "UserProfile", "devauth/dev-login as "+handle)
	profile, err := csil.DecodeUserProfile(resp.Payload)
	if err != nil {
		t.Fatalf("decoding UserProfile for %s: %v", handle, err)
	}
	return client, profile
}

// loginAt mints a session for handle at a domain the caller picks. Two
// instances of tinku are two domains, so a test about who somebody is has
// to be able to say which domain they are from.
func (e *testEnv) loginAt(t *testing.T, handle, domain string) (*http.Client, csil.UserProfile) {
	t.Helper()
	client := e.newClient(t)
	resp := e.call(t, client, "devauth", "dev-login",
		csil.EncodeDevLoginRequest(csil.DevLoginRequest{Handle: handle, Domain: domain}))
	requireReply(t, resp, "UserProfile", "devauth/dev-login as "+handle+"@"+domain)
	profile, err := csil.DecodeUserProfile(resp.Payload)
	if err != nil {
		t.Fatalf("decoding UserProfile for %s@%s: %v", handle, domain, err)
	}
	return client, profile
}

func (e *testEnv) call(t *testing.T, client *http.Client, service, op string, payload []byte) transport.RpcResponse {
	t.Helper()
	return call(t, client, e.Server.URL, service, op, payload)
}

// makeAdmin grants the global role through the store, which is the same
// bootstrap path `tinku admin grant` uses: the first admin cannot be made
// over the API, because granting the role requires the role.
func (e *testEnv) makeAdmin(t *testing.T, userID string) {
	t.Helper()
	if err := e.Store.SetAdmin(context.Background(), userID, true); err != nil {
		t.Fatalf("granting admin: %v", err)
	}
}

// requireServiceError asserts a declared application failure — a typed
// reply at transport status 0, NOT a transport-level failure. Getting this
// distinction wrong is the easiest way to write a test that passes for the
// wrong reason.
func requireServiceError(t *testing.T, resp transport.RpcResponse, code uint64, what string) csil.ServiceError {
	t.Helper()
	if !resp.Status.IsOk() {
		t.Fatalf("%s: transport status %s, want an application error at status 0", what, resp.Status.Name())
	}
	if resp.Variant == nil || *resp.Variant != "ServiceError" {
		got := "<none>"
		if resp.Variant != nil {
			got = *resp.Variant
		}
		t.Fatalf("%s: reply variant %s, want ServiceError", what, got)
	}
	svcErr, err := csil.DecodeServiceError(resp.Payload)
	if err != nil {
		t.Fatalf("%s: decoding ServiceError: %v", what, err)
	}
	if svcErr.Code != code {
		t.Fatalf("%s: error code %d (%s), want %d", what, svcErr.Code, svcErr.Message, code)
	}
	return svcErr
}

// ---- Helpers that build the common fixtures -------------------------------

func (e *testEnv) createGathering(t *testing.T, client *http.Client, name string) csil.Gathering {
	t.Helper()
	resp := e.call(t, client, "gathering", "create-gathering",
		csil.EncodeCreateGatheringRequest(csil.CreateGatheringRequest{
			Name: name, Blurb: "a blurb", Description: "a longer description",
		}))
	requireReply(t, resp, "Gathering", "gathering/create-gathering")
	gathering, err := csil.DecodeGathering(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Gathering: %v", err)
	}
	return gathering
}

func (e *testEnv) createEvent(t *testing.T, client *http.Client, gatheringID csil.GatheringID, title string, startsIn time.Duration) csil.Event {
	t.Helper()
	starts := e.clock().Add(startsIn)
	resp := e.call(t, client, "event", "create-event",
		csil.EncodeCreateEventRequest(csil.CreateEventRequest{
			GatheringId: gatheringID,
			Title:       title,
			Description: "what happens at " + title,
			IsOnline:    true,
			IsInPerson:  false,
			StartsAt:    starts,
			EndsAt:      starts.Add(time.Hour),
			Timezone:    "America/Denver",
		}))
	requireReply(t, resp, "Event", "event/create-event")
	event, err := csil.DecodeEvent(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Event: %v", err)
	}
	return event
}

// ---- Gatherings -----------------------------------------------------------

func TestGatheringOwnershipAndMembership(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	bob, _ := env.login(t, "bob")

	gathering := env.createGathering(t, ada, "Thursday Bouldering")
	if !gathering.Viewer.IsOwner || !gathering.Viewer.CanEdit {
		t.Errorf("the creator is not the owner: %+v", gathering.Viewer)
	}
	if gathering.Slug != "thursday-bouldering" {
		t.Errorf("slug %q, want thursday-bouldering", gathering.Slug)
	}
	if gathering.OriginDomain != "tinku.test" {
		t.Errorf("origin domain %q, want tinku.test", gathering.OriginDomain)
	}

	// Somebody else sees the same gathering with none of the powers.
	resp := env.call(t, bob, "gathering", "get-gathering",
		csil.EncodeGetGatheringRequest(csil.GetGatheringRequest{Id: gathering.Id}))
	requireReply(t, resp, "Gathering", "gathering/get-gathering as bob")
	asBob, err := csil.DecodeGathering(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Gathering: %v", err)
	}
	if asBob.Viewer.IsOwner || asBob.Viewer.CanEdit || asBob.Viewer.IsMember {
		t.Errorf("a stranger has powers over the gathering: %+v", asBob.Viewer)
	}

	// Joining is open to anybody with a session, and makes them a member
	// without making them an owner.
	resp = env.call(t, bob, "gathering", "join-gathering",
		csil.EncodeJoinGatheringRequest(csil.JoinGatheringRequest{GatheringId: gathering.Id}))
	requireReply(t, resp, "Gathering", "gathering/join-gathering")
	joined, err := csil.DecodeGathering(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Gathering: %v", err)
	}
	if !joined.Viewer.IsMember {
		t.Error("bob joined but is not a member")
	}
	if joined.Viewer.IsOwner {
		t.Error("joining made bob an owner")
	}
	if joined.MemberCount != 1 {
		t.Errorf("member count %d, want 1", joined.MemberCount)
	}
}

// Ownership through an organization is the whole reason organizations exist.
// An owner of an organization that owns a gathering owns the gathering,
// without any row naming them directly.
func TestOrganizationOwnershipReachesTheGathering(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	bob, bobProfile := env.login(t, "bob")

	resp := env.call(t, ada, "organization", "create-organization",
		csil.EncodeCreateOrganizationRequest(csil.CreateOrganizationRequest{
			Name: "Front Range Climbers", Blurb: "we climb", Description: "a longer description",
		}))
	requireReply(t, resp, "Organization", "organization/create-organization")
	organization, err := csil.DecodeOrganization(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Organization: %v", err)
	}

	// Bob becomes an owner of the organization — not of any gathering.
	resp = env.call(t, ada, "organization", "set-organization-member",
		csil.EncodeSetOrganizationMemberRequest(csil.SetOrganizationMemberRequest{
			OrganizationId: organization.Id, UserId: bobProfile.Id, Role: "owner",
		}))
	requireReply(t, resp, "OrganizationMemberList", "organization/set-organization-member")

	// Ada makes a gathering owned by the organization rather than by herself.
	resp = env.call(t, ada, "gathering", "create-gathering",
		csil.EncodeCreateGatheringRequest(csil.CreateGatheringRequest{
			Name:        "Quarterly Crag Day",
			Blurb:       "a blurb",
			Description: "a longer description",
			Owner:       &csil.OwnerRefInput{Kind: "organization", Id: string(organization.Id)},
		}))
	requireReply(t, resp, "Gathering", "gathering/create-gathering owned by an organization")
	gathering, err := csil.DecodeGathering(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Gathering: %v", err)
	}
	if len(gathering.Owners) != 1 || gathering.Owners[0].Kind != "organization" {
		t.Fatalf("owners %+v, want one organization", gathering.Owners)
	}

	// Bob has never touched the gathering, and owns it.
	resp = env.call(t, bob, "gathering", "get-gathering",
		csil.EncodeGetGatheringRequest(csil.GetGatheringRequest{Id: gathering.Id}))
	requireReply(t, resp, "Gathering", "gathering/get-gathering as an organization owner")
	asBob, err := csil.DecodeGathering(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Gathering: %v", err)
	}
	if !asBob.Viewer.IsOwner {
		t.Error("an owner of the owning organization does not own the gathering")
	}
}

// An organization's blurb is limited to 300 WORDS, which no schema constraint can
// state — so it is worth a test that the server actually counts them.
func TestBlurbWordLimit(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")

	resp := env.call(t, ada, "organization", "create-organization",
		csil.EncodeCreateOrganizationRequest(csil.CreateOrganizationRequest{
			Name:  "Verbose",
			Blurb: strings.TrimSpace(strings.Repeat("word ", 301)),
		}))
	svcErr := requireServiceError(t, resp, 1, "organization/create-organization with a 301-word blurb")
	if svcErr.Field == nil || *svcErr.Field != "blurb" {
		t.Errorf("error names field %v, want blurb", svcErr.Field)
	}

	// 300 exactly is allowed: the limit is "less than 300 words" read as a
	// ceiling, and a rule a writer cannot hit exactly is a worse rule.
	resp = env.call(t, ada, "organization", "create-organization",
		csil.EncodeCreateOrganizationRequest(csil.CreateOrganizationRequest{
			Name:  "Just Under",
			Blurb: strings.TrimSpace(strings.Repeat("word ", 300)),
		}))
	requireReply(t, resp, "Organization", "organization/create-organization with a 300-word blurb")
}

// Two organizations with the same name must both be creatable: the slug is
// derived, and a derived value must never make a legal name unusable.
func TestDuplicateNamesGetDistinctSlugs(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")

	slugs := map[string]bool{}
	for i := 0; i < 2; i++ {
		resp := env.call(t, ada, "organization", "create-organization",
			csil.EncodeCreateOrganizationRequest(csil.CreateOrganizationRequest{Name: "Board Games"}))
		requireReply(t, resp, "Organization", "organization/create-organization (duplicate name)")
		organization, err := csil.DecodeOrganization(resp.Payload)
		if err != nil {
			t.Fatalf("decoding Organization: %v", err)
		}
		if slugs[organization.Slug] {
			t.Fatalf("two organizations share the slug %q", organization.Slug)
		}
		slugs[organization.Slug] = true
	}
}

// ---- Events ---------------------------------------------------------------

func TestOnlyGatheringOwnersScheduleEvents(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	bob, _ := env.login(t, "bob")

	gathering := env.createGathering(t, ada, "Board Games")
	env.call(t, bob, "gathering", "join-gathering",
		csil.EncodeJoinGatheringRequest(csil.JoinGatheringRequest{GatheringId: gathering.Id}))

	starts := env.clock().Add(24 * time.Hour)
	resp := env.call(t, bob, "event", "create-event",
		csil.EncodeCreateEventRequest(csil.CreateEventRequest{
			GatheringId: gathering.Id, Title: "Bob's Event", IsOnline: true,
			StartsAt: starts, EndsAt: starts.Add(time.Hour), Timezone: "UTC",
		}))
	requireServiceError(t, resp, 3, "event/create-event as a mere member")

	env.createEvent(t, ada, gathering.Id, "Catan Night", 24*time.Hour)
}

func TestAttendanceNeedsGatheringMembership(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	carol, _ := env.login(t, "carol")

	gathering := env.createGathering(t, ada, "Board Games")
	event := env.createEvent(t, ada, gathering.Id, "Catan Night", 24*time.Hour)

	// A person joins a gathering, then answers its events. Answering one
	// without joining is refused.
	resp := env.call(t, carol, "event", "attend-event",
		csil.EncodeAttendEventRequest(csil.AttendEventRequest{EventId: event.Id}))
	requireServiceError(t, resp, 3, "event/attend-event without joining")

	env.call(t, carol, "gathering", "join-gathering",
		csil.EncodeJoinGatheringRequest(csil.JoinGatheringRequest{GatheringId: gathering.Id}))

	resp = env.call(t, carol, "event", "attend-event",
		csil.EncodeAttendEventRequest(csil.AttendEventRequest{EventId: event.Id}))
	requireReply(t, resp, "Event", "event/attend-event after joining")
	attending, err := csil.DecodeEvent(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Event: %v", err)
	}
	if !attending.ViewerAttending || attending.AttendeeCount != 1 {
		t.Errorf("attending=%v count=%d, want true and 1", attending.ViewerAttending, attending.AttendeeCount)
	}

	resp = env.call(t, carol, "event", "unattend-event",
		csil.EncodeUnattendEventRequest(csil.UnattendEventRequest{EventId: event.Id}))
	requireReply(t, resp, "Event", "event/unattend-event")
	withdrawn, err := csil.DecodeEvent(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Event: %v", err)
	}
	if withdrawn.ViewerAttending || withdrawn.AttendeeCount != 0 {
		t.Errorf("attending=%v count=%d, want false and 0", withdrawn.ViewerAttending, withdrawn.AttendeeCount)
	}
}

// The start-time lock, which is the domain's sharpest rule: once the event
// starts it is frozen, its description is gone, and the only power left over
// it belongs to an admin.
func TestStartedEventIsFrozen(t *testing.T) {
	env := newTestEnv(t)
	ada, adaProfile := env.login(t, "ada")
	carol, _ := env.login(t, "carol")

	gathering := env.createGathering(t, ada, "Board Games")
	event := env.createEvent(t, ada, gathering.Id, "Catan Night", time.Hour)
	if event.Description == nil {
		t.Fatal("an event that has not started should carry its description")
	}

	env.call(t, carol, "gathering", "join-gathering",
		csil.EncodeJoinGatheringRequest(csil.JoinGatheringRequest{GatheringId: gathering.Id}))

	// The moment starts_at passes, everything changes.
	env.advance(2 * time.Hour)

	resp := env.call(t, ada, "event", "get-event",
		csil.EncodeGetEventRequest(csil.GetEventRequest{Id: event.Id}))
	requireReply(t, resp, "Event", "event/get-event after it started")
	started, err := csil.DecodeEvent(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Event: %v", err)
	}
	if !started.Locked {
		t.Error("a started event is not reported as locked")
	}
	if started.Description != nil {
		t.Errorf("a started event still carries its description: %q", *started.Description)
	}
	if started.Viewer.CanEdit {
		t.Error("a started event reports that its owner can edit it")
	}
	if started.Viewer.CanDelete {
		t.Error("a started event reports that its owner can delete it")
	}

	newTitle := "Renamed"
	resp = env.call(t, ada, "event", "update-event",
		csil.EncodeUpdateEventRequest(csil.UpdateEventRequest{Id: event.Id, Title: &newTitle}))
	requireServiceError(t, resp, 3, "event/update-event after it started")

	resp = env.call(t, carol, "event", "attend-event",
		csil.EncodeAttendEventRequest(csil.AttendEventRequest{EventId: event.Id}))
	requireServiceError(t, resp, 3, "event/attend-event after it started")

	// Even the owner cannot delete it. Deleting history is an admin's power.
	resp = env.call(t, ada, "event", "delete-event",
		csil.EncodeDeleteEventRequest(csil.DeleteEventRequest{Id: event.Id}))
	requireServiceError(t, resp, 3, "event/delete-event by the owner after it started")

	env.makeAdmin(t, string(adaProfile.Id))
	resp = env.call(t, ada, "event", "delete-event",
		csil.EncodeDeleteEventRequest(csil.DeleteEventRequest{Id: event.Id}))
	requireReply(t, resp, "Empty", "event/delete-event by an admin after it started")
}

// Every instant is stored in UTC. Timezones are for display and for entry;
// they never reach a timestamp column.
//
// This reads the raw column text rather than a value the store handed back,
// because the store parses on the way out — so a round trip through it
// would pass even if the bytes on disk were wrong.
func TestEveryStoredInstantIsUTC(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	gathering := env.createGathering(t, ada, "Board Games")

	// Submit the instant in a zone that is neither UTC nor a whole number of
	// hours from it, so a dropped or mishandled conversion cannot coincide
	// with the right answer.
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatalf("loading Asia/Kolkata: %v", err)
	}
	starts := time.Date(2026, 3, 12, 19, 30, 0, 0, kolkata)

	resp := env.call(t, ada, "event", "create-event",
		csil.EncodeCreateEventRequest(csil.CreateEventRequest{
			GatheringId: gathering.Id,
			Title:       "Offset Probe",
			IsOnline:    true,
			StartsAt:    starts,
			EndsAt:      starts.Add(time.Hour),
			Timezone:    "Asia/Kolkata",
		}))
	requireReply(t, resp, "Event", "event/create-event with a +05:30 instant")
	event, err := csil.DecodeEvent(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Event: %v", err)
	}
	if !event.StartsAt.Equal(starts) {
		t.Errorf("the event came back at %s, want the same instant as %s",
			event.StartsAt, starts)
	}

	target, err := coredb.ParseTarget(env.DBURI)
	if err != nil {
		t.Fatalf("parsing the test database URI: %v", err)
	}
	db, err := sql.Open(target.Driver, target.DSN)
	if err != nil {
		t.Fatalf("opening the database directly: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	// Render the stored value to the same text on both backends, so the
	// assertion below is one assertion and not two. SQLite already holds
	// this layout; Postgres holds a timestamptz and is asked for it.
	query := `SELECT starts_at, ends_at, timezone FROM events WHERE id = ?`
	if target.Dialect == coredb.DialectPostgres {
		query = `SELECT to_char(starts_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		                to_char(ends_at   AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		                timezone
		         FROM events WHERE id = $1`
	}

	var startsAt, endsAt, timezone string
	if err := db.QueryRow(query, string(event.Id)).Scan(&startsAt, &endsAt, &timezone); err != nil {
		t.Fatalf("reading the raw row: %v", err)
	}

	// 19:30 +05:30 is 14:00 UTC. The stored text must say so, in UTC, with
	// the Z that says it is UTC.
	if startsAt != "2026-03-12T14:00:00Z" {
		t.Errorf("starts_at is stored as %q, want 2026-03-12T14:00:00Z", startsAt)
	}
	if endsAt != "2026-03-12T15:00:00Z" {
		t.Errorf("ends_at is stored as %q, want 2026-03-12T15:00:00Z", endsAt)
	}
	// The zone is kept, but as a separate column that says how to DISPLAY
	// the instant — never as an offset baked into the instant itself.
	if timezone != "Asia/Kolkata" {
		t.Errorf("timezone is stored as %q, want Asia/Kolkata", timezone)
	}
}

// ---- Organizations and the admin role --------------------------------------------

func TestOnlyAdminsDeleteOrganizations(t *testing.T) {
	env := newTestEnv(t)
	ada, adaProfile := env.login(t, "ada")

	resp := env.call(t, ada, "organization", "create-organization",
		csil.EncodeCreateOrganizationRequest(csil.CreateOrganizationRequest{Name: "Front Range Climbers"}))
	requireReply(t, resp, "Organization", "organization/create-organization")
	organization, err := csil.DecodeOrganization(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Organization: %v", err)
	}
	if organization.Viewer.CanDelete {
		t.Error("an organization owner is told they can delete their organization")
	}

	resp = env.call(t, ada, "organization", "delete-organization",
		csil.EncodeDeleteOrganizationRequest(csil.DeleteOrganizationRequest{Id: organization.Id}))
	requireServiceError(t, resp, 3, "organization/delete-organization as the owner")

	env.makeAdmin(t, string(adaProfile.Id))
	resp = env.call(t, ada, "organization", "delete-organization",
		csil.EncodeDeleteOrganizationRequest(csil.DeleteOrganizationRequest{Id: organization.Id}))
	requireReply(t, resp, "Empty", "organization/delete-organization as an admin")
}

func TestAnOrganizationKeepsAtLeastOneOwner(t *testing.T) {
	env := newTestEnv(t)
	ada, adaProfile := env.login(t, "ada")

	resp := env.call(t, ada, "organization", "create-organization",
		csil.EncodeCreateOrganizationRequest(csil.CreateOrganizationRequest{Name: "Solo"}))
	requireReply(t, resp, "Organization", "organization/create-organization")
	organization, err := csil.DecodeOrganization(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Organization: %v", err)
	}

	resp = env.call(t, ada, "organization", "remove-organization-member",
		csil.EncodeRemoveOrganizationMemberRequest(csil.RemoveOrganizationMemberRequest{
			OrganizationId: organization.Id, UserId: adaProfile.Id,
		}))
	requireServiceError(t, resp, 1, "organization/remove-organization-member removing the last owner")
}

// ---- Series ---------------------------------------------------------------

// A series produces ordinary events. This is the claim the whole model rests
// on: after creating one rule, the gathering's event listing is full of
// events nobody created one at a time.
func TestSeriesMaterializesRealEvents(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	gathering := env.createGathering(t, ada, "Board Games")

	weekday := csil.Weekday("thursday")
	ordinal := int64(2)
	resp := env.call(t, ada, "event", "create-event-series",
		csil.EncodeCreateEventSeriesRequest(csil.CreateEventSeriesRequest{
			GatheringId: gathering.Id,
			Title:       "Second Thursday Catan",
			Description: "every second Thursday",
			IsOnline:    true,
			Recurrence: csil.RecurrenceRule{
				Freq: "monthly", Interval: 1, Weekday: &weekday, Ordinal: &ordinal,
			},
			StartsOn:        env.clock(),
			StartTime:       "19:00",
			DurationMinutes: 120,
			Timezone:        "America/Denver",
		}))
	requireReply(t, resp, "EventSeries", "event/create-event-series")
	series, err := csil.DecodeEventSeries(resp.Payload)
	if err != nil {
		t.Fatalf("decoding EventSeries: %v", err)
	}
	// A year's default horizon over a monthly rule is twelve occurrences.
	if series.OccurrenceCount != 12 {
		t.Errorf("materialized %d occurrences, want 12", series.OccurrenceCount)
	}
	if series.NextOccurrenceAt == nil {
		t.Fatal("a series with occurrences ahead of it reports no next occurrence")
	}

	// Those occurrences are events, indistinguishable from any other except
	// for carrying series_id.
	resp = env.call(t, ada, "event", "list-events",
		csil.EncodeListEventsRequest(csil.ListEventsRequest{GatheringId: &gathering.Id}))
	requireReply(t, resp, "EventList", "event/list-events")
	list, err := csil.DecodeEventList(resp.Payload)
	if err != nil {
		t.Fatalf("decoding EventList: %v", err)
	}
	if list.Total != 12 {
		t.Errorf("listing shows %d events, want 12", list.Total)
	}
	for _, e := range list.Events {
		if e.SeriesId == nil || *e.SeriesId != series.Id {
			t.Errorf("event %s is not attributed to the series", e.Id)
		}
	}

	// Expanding further is idempotent for what already exists.
	through := env.clock().AddDate(2, 0, 0)
	resp = env.call(t, ada, "event", "expand-event-series",
		csil.EncodeExpandEventSeriesRequest(csil.ExpandEventSeriesRequest{
			SeriesId: series.Id, Through: &through,
		}))
	requireReply(t, resp, "EventList", "event/expand-event-series")
	expanded, err := csil.DecodeEventList(resp.Payload)
	if err != nil {
		t.Fatalf("decoding EventList: %v", err)
	}
	if expanded.Total <= 12 {
		t.Errorf("expanding to two years produced %d occurrences, want more than 12", expanded.Total)
	}

	before := expanded.Total
	resp = env.call(t, ada, "event", "expand-event-series",
		csil.EncodeExpandEventSeriesRequest(csil.ExpandEventSeriesRequest{
			SeriesId: series.Id, Through: &through,
		}))
	requireReply(t, resp, "EventList", "event/expand-event-series again")
	again, err := csil.DecodeEventList(resp.Payload)
	if err != nil {
		t.Fatalf("decoding EventList: %v", err)
	}
	if again.Total != before {
		t.Errorf("expanding twice produced %d then %d occurrences; it should be idempotent", before, again.Total)
	}
}

// Two rules in one gathering is the normal case the domain describes, not an
// edge case: "every second Thursday" and "every fourth Wednesday" are two
// series, and their occurrences share one listing.
func TestTwoSeriesInOneGathering(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	gathering := env.createGathering(t, ada, "Board Games")

	makeSeries := func(title string, weekday csil.Weekday, ordinal int64) {
		t.Helper()
		resp := env.call(t, ada, "event", "create-event-series",
			csil.EncodeCreateEventSeriesRequest(csil.CreateEventSeriesRequest{
				GatheringId: gathering.Id, Title: title, IsOnline: true,
				Recurrence: csil.RecurrenceRule{
					Freq: "monthly", Interval: 1, Weekday: &weekday, Ordinal: &ordinal,
				},
				StartsOn: env.clock(), StartTime: "19:00",
				DurationMinutes: 120, Timezone: "America/Denver",
			}))
		requireReply(t, resp, "EventSeries", "event/create-event-series "+title)
	}
	makeSeries("Second Thursday Catan", "thursday", 2)
	makeSeries("Fourth Wednesday Chess", "wednesday", 4)

	resp := env.call(t, ada, "event", "list-events",
		csil.EncodeListEventsRequest(csil.ListEventsRequest{GatheringId: &gathering.Id}))
	requireReply(t, resp, "EventList", "event/list-events with two series")
	list, err := csil.DecodeEventList(resp.Payload)
	if err != nil {
		t.Fatalf("decoding EventList: %v", err)
	}
	if list.Total != 24 {
		t.Errorf("two monthly series over a year gave %d events, want 24", list.Total)
	}

	// The gathering counts a series once, not once per occurrence.
	resp = env.call(t, ada, "gathering", "get-gathering",
		csil.EncodeGetGatheringRequest(csil.GetGatheringRequest{Id: gathering.Id}))
	requireReply(t, resp, "Gathering", "gathering/get-gathering")
	fetched, err := csil.DecodeGathering(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Gathering: %v", err)
	}
	if fetched.EventCount != 2 {
		t.Errorf("event count %d, want 2 (one per series)", fetched.EventCount)
	}
}

// ---- Search ---------------------------------------------------------------

func TestSearchByPlaceAndTime(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	gathering := env.createGathering(t, ada, "Bouldering")

	inPerson := func(title, locality string, lat, lon float64, startsIn time.Duration) {
		t.Helper()
		starts := env.clock().Add(startsIn)
		resp := env.call(t, ada, "event", "create-event",
			csil.EncodeCreateEventRequest(csil.CreateEventRequest{
				GatheringId: gathering.Id, Title: title,
				IsInPerson: true,
				Location: &csil.Location{
					Name: title + " gym", Locality: locality, Region: "Colorado", Country: "US",
					Latitude: &lat, Longitude: &lon,
				},
				StartsAt: starts, EndsAt: starts.Add(time.Hour), Timezone: "America/Denver",
			}))
		requireReply(t, resp, "Event", "event/create-event "+title)
	}
	inPerson("Denver Session", "Denver", 39.7392, -104.9903, 24*time.Hour)
	inPerson("Boulder Session", "Boulder", 40.0150, -105.2705, 48*time.Hour)

	search := func(req csil.SearchRequest, what string) csil.SearchResults {
		t.Helper()
		resp := env.call(t, ada, "search", "search", csil.EncodeSearchRequest(req))
		requireReply(t, resp, "SearchResults", "search/search "+what)
		results, err := csil.DecodeSearchResults(resp.Payload)
		if err != nil {
			t.Fatalf("decoding SearchResults: %v", err)
		}
		return results
	}

	locality := "denver"
	byPlace := search(csil.SearchRequest{
		Kinds:    []csil.SearchKind{"event"},
		Location: &csil.LocationFilter{Locality: &locality},
	}, "by locality")
	if len(byPlace.Events) != 1 || byPlace.Events[0].Title != "Denver Session" {
		t.Errorf("locality search returned %d events, want just the Denver one", len(byPlace.Events))
	}

	// Proximity: a 30 km circle on Denver reaches Denver and not Boulder,
	// which is about 40 km away.
	near := csil.GeoCircle{Latitude: 39.7392, Longitude: -104.9903, RadiusKm: 30}
	byDistance := search(csil.SearchRequest{
		Kinds:    []csil.SearchKind{"event"},
		Location: &csil.LocationFilter{Near: &near},
	}, "by proximity")
	if len(byDistance.Events) != 1 || byDistance.Events[0].Title != "Denver Session" {
		t.Errorf("proximity search returned %v, want just the Denver one", titlesOf(byDistance.Events))
	}

	// Widening the circle reaches both.
	near.RadiusKm = 80
	wider := search(csil.SearchRequest{
		Kinds:    []csil.SearchKind{"event"},
		Location: &csil.LocationFilter{Near: &near},
	}, "by a wider proximity")
	if len(wider.Events) != 2 {
		t.Errorf("an 80 km circle returned %v, want both", titlesOf(wider.Events))
	}

	// Time: only what starts in the next day and a half.
	before := env.clock().Add(36 * time.Hour)
	byTime := search(csil.SearchRequest{
		Kinds:        []csil.SearchKind{"event"},
		StartsBefore: &before,
	}, "by time")
	if len(byTime.Events) != 1 || byTime.Events[0].Title != "Denver Session" {
		t.Errorf("time search returned %v, want just the Denver one", titlesOf(byTime.Events))
	}

	// Free text reaches the gathering as well as the events, in one call.
	query := "bouldering"
	everything := search(csil.SearchRequest{Query: &query}, "free text")
	if len(everything.Gatherings) != 1 {
		t.Errorf("free-text search found %d gatherings, want 1", len(everything.Gatherings))
	}
	// The total travels in a different query from the list, sharing the
	// same WHERE clause and argument list. A mismatch between the two is
	// invisible if a test only reads the list — which is exactly how a bad
	// argument list survived here once.
	if everything.GatheringTotal != 1 {
		t.Errorf("free-text search reported a total of %d gatherings, want 1", everything.GatheringTotal)
	}
	if byPlace.EventTotal != 1 {
		t.Errorf("locality search reported a total of %d events, want 1", byPlace.EventTotal)
	}
}

func titlesOf(events []csil.Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Title)
	}
	return out
}

// ---- Roles ----------------------------------------------------------------

// An organizer of a SERIES organizes each of its occurrences, without a row
// naming them on any one of them.
func TestSeriesOrganizerCanEditItsOccurrences(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	bob, bobProfile := env.login(t, "bob")
	gathering := env.createGathering(t, ada, "Board Games")

	weekday := csil.Weekday("thursday")
	ordinal := int64(2)
	resp := env.call(t, ada, "event", "create-event-series",
		csil.EncodeCreateEventSeriesRequest(csil.CreateEventSeriesRequest{
			GatheringId: gathering.Id, Title: "Second Thursday Catan", IsOnline: true,
			Recurrence: csil.RecurrenceRule{
				Freq: "monthly", Interval: 1, Weekday: &weekday, Ordinal: &ordinal,
			},
			StartsOn: env.clock(), StartTime: "19:00",
			DurationMinutes: 120, Timezone: "America/Denver",
		}))
	requireReply(t, resp, "EventSeries", "event/create-event-series")
	series, err := csil.DecodeEventSeries(resp.Payload)
	if err != nil {
		t.Fatalf("decoding EventSeries: %v", err)
	}

	resp = env.call(t, ada, "event", "set-event-role",
		csil.EncodeSetEventRoleRequest(csil.SetEventRoleRequest{
			SeriesId: &series.Id, UserId: bobProfile.Id, Role: "organizer",
		}))
	requireReply(t, resp, "EventRoleList", "event/set-event-role on the series")

	// Find one occurrence and check bob may edit it.
	resp = env.call(t, ada, "event", "list-events",
		csil.EncodeListEventsRequest(csil.ListEventsRequest{GatheringId: &gathering.Id}))
	requireReply(t, resp, "EventList", "event/list-events")
	list, err := csil.DecodeEventList(resp.Payload)
	if err != nil {
		t.Fatalf("decoding EventList: %v", err)
	}
	if len(list.Events) == 0 {
		t.Fatal("the series materialized no occurrences")
	}

	resp = env.call(t, bob, "event", "get-event",
		csil.EncodeGetEventRequest(csil.GetEventRequest{Id: list.Events[0].Id}))
	requireReply(t, resp, "Event", "event/get-event as the series organizer")
	occurrence, err := csil.DecodeEvent(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Event: %v", err)
	}
	if !occurrence.Viewer.IsOrganizer || !occurrence.Viewer.CanEdit {
		t.Errorf("a series organizer cannot edit its occurrence: %+v", occurrence.Viewer)
	}

	title := "Catan Night (moved)"
	resp = env.call(t, bob, "event", "update-event",
		csil.EncodeUpdateEventRequest(csil.UpdateEventRequest{Id: occurrence.Id, Title: &title}))
	requireReply(t, resp, "Event", "event/update-event as the series organizer")
}

// A presenter is billed as presenting and carries a member's permissions and
// nothing more. The flag exists so the UI can show the billing; it must not
// quietly become an edit right.
func TestPresenterHasNoEditPower(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	bob, bobProfile := env.login(t, "bob")

	gathering := env.createGathering(t, ada, "Talks")
	event := env.createEvent(t, ada, gathering.Id, "A Talk", 24*time.Hour)

	resp := env.call(t, ada, "event", "set-event-role",
		csil.EncodeSetEventRoleRequest(csil.SetEventRoleRequest{
			EventId: &event.Id, UserId: bobProfile.Id, Role: "presenter",
		}))
	requireReply(t, resp, "EventRoleList", "event/set-event-role presenter")

	resp = env.call(t, bob, "event", "get-event",
		csil.EncodeGetEventRequest(csil.GetEventRequest{Id: event.Id}))
	requireReply(t, resp, "Event", "event/get-event as a presenter")
	asPresenter, err := csil.DecodeEvent(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Event: %v", err)
	}
	if !asPresenter.Viewer.IsPresenter {
		t.Error("the presenter is not billed as one")
	}
	if asPresenter.Viewer.CanEdit {
		t.Error("a presenter is told they can edit the event")
	}

	title := "Renamed by a presenter"
	resp = env.call(t, bob, "event", "update-event",
		csil.EncodeUpdateEventRequest(csil.UpdateEventRequest{Id: event.Id, Title: &title}))
	requireServiceError(t, resp, 3, "event/update-event as a presenter")
}

// Anonymous discovery is the point of a public directory: somebody has to be
// able to look before they have an account.
func TestAnonymousCanBrowse(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	gathering := env.createGathering(t, ada, "Bouldering")
	env.createEvent(t, ada, gathering.Id, "Thursday Session", 24*time.Hour)

	anon := env.newClient(t)
	resp := env.call(t, anon, "gathering", "list-gatherings",
		csil.EncodeListGatheringsRequest(csil.ListGatheringsRequest{}))
	requireReply(t, resp, "GatheringList", "gathering/list-gatherings anonymously")
	list, err := csil.DecodeGatheringList(resp.Payload)
	if err != nil {
		t.Fatalf("decoding GatheringList: %v", err)
	}
	if list.Total != 1 {
		t.Errorf("an anonymous caller sees %d gatherings, want 1", list.Total)
	}
	if list.Gatherings[0].Viewer.IsMember || list.Gatherings[0].Viewer.CanEdit {
		t.Errorf("an anonymous caller has powers: %+v", list.Gatherings[0].Viewer)
	}

	// Joining, though, needs a session.
	resp = env.call(t, anon, "gathering", "join-gathering",
		csil.EncodeJoinGatheringRequest(csil.JoinGatheringRequest{GatheringId: gathering.Id}))
	requireServiceError(t, resp, 2, "gathering/join-gathering anonymously")
}

// Changing a rule must not change history.
//
// The rewrite drops the future and rebuilds it. If it rebuilt from the
// series' start instead, moving "every Thursday" to "every Friday" would
// invent a Friday in every week the series had already run — inventing
// attendance-bearing history that never happened.
func TestChangingARuleLeavesThePastAlone(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	gathering := env.createGathering(t, ada, "Board Games")

	thursday := csil.Weekday("thursday")
	friday := csil.Weekday("friday")
	weekly := func(day csil.Weekday) csil.RecurrenceRule {
		return csil.RecurrenceRule{Freq: "weekly", Interval: 1, Weekday: &day}
	}

	// Start the series a year before "now", so it has a real past.
	startsOn := env.clock().AddDate(-1, 0, 0)
	resp := env.call(t, ada, "event", "create-event-series",
		csil.EncodeCreateEventSeriesRequest(csil.CreateEventSeriesRequest{
			GatheringId: gathering.Id, Title: "Weekly Catan", IsOnline: true,
			Recurrence: weekly(thursday), StartsOn: startsOn,
			StartTime: "19:00", DurationMinutes: 120, Timezone: "America/Denver",
		}))
	requireReply(t, resp, "EventSeries", "event/create-event-series")
	series, err := csil.DecodeEventSeries(resp.Payload)
	if err != nil {
		t.Fatalf("decoding EventSeries: %v", err)
	}

	countPast := func(what string) int {
		t.Helper()
		before := env.clock()
		resp := env.call(t, ada, "event", "list-events",
			csil.EncodeListEventsRequest(csil.ListEventsRequest{
				GatheringId:    &gathering.Id,
				StartsBefore:   &before,
				IncludeStarted: boolPtr(true),
				Page:           &csil.Page{Limit: uint64Ptr(200)},
			}))
		requireReply(t, resp, "EventList", "event/list-events "+what)
		list, err := csil.DecodeEventList(resp.Payload)
		if err != nil {
			t.Fatalf("decoding EventList: %v", err)
		}
		return int(list.Total)
	}

	pastBefore := countPast("before the change")
	if pastBefore == 0 {
		t.Fatal("the fixture produced no past occurrences, so it cannot test that they survive")
	}

	// Move the rule to a different weekday.
	rule := weekly(friday)
	resp = env.call(t, ada, "event", "update-event-series",
		csil.EncodeUpdateEventSeriesRequest(csil.UpdateEventSeriesRequest{
			Id: series.Id, Recurrence: &rule,
		}))
	requireReply(t, resp, "EventSeries", "event/update-event-series")

	pastAfter := countPast("after the change")
	if pastAfter != pastBefore {
		t.Errorf("changing the rule changed the past: %d occurrences before, %d after",
			pastBefore, pastAfter)
	}
}

// Expanding a series returns EVERY occurrence it has, not one clamped page.
//
// The store caps any single page well below the row bound, so a naive
// single query silently drops the rest — and for a long-running series the
// page it does return is all past, which makes the future invisible.
func TestExpandingASeriesReturnsAllOfIt(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	gathering := env.createGathering(t, ada, "Board Games")

	thursday := csil.Weekday("thursday")
	resp := env.call(t, ada, "event", "create-event-series",
		csil.EncodeCreateEventSeriesRequest(csil.CreateEventSeriesRequest{
			GatheringId: gathering.Id, Title: "Weekly Catan", IsOnline: true,
			Recurrence: csil.RecurrenceRule{Freq: "weekly", Interval: 1, Weekday: &thursday},
			StartsOn:   env.clock(), StartTime: "19:00",
			DurationMinutes: 120, Timezone: "America/Denver",
		}))
	requireReply(t, resp, "EventSeries", "event/create-event-series")
	series, err := csil.DecodeEventSeries(resp.Payload)
	if err != nil {
		t.Fatalf("decoding EventSeries: %v", err)
	}

	// Two years of a weekly rule is comfortably more than one page.
	through := env.clock().AddDate(2, 0, 0)
	resp = env.call(t, ada, "event", "expand-event-series",
		csil.EncodeExpandEventSeriesRequest(csil.ExpandEventSeriesRequest{
			SeriesId: series.Id, Through: &through,
		}))
	requireReply(t, resp, "EventList", "event/expand-event-series")
	expanded, err := csil.DecodeEventList(resp.Payload)
	if err != nil {
		t.Fatalf("decoding EventList: %v", err)
	}

	// What the series actually has, read independently of the op under test.
	resp = env.call(t, ada, "event", "get-event-series",
		csil.EncodeGetEventSeriesRequest(csil.GetEventSeriesRequest{Id: series.Id}))
	requireReply(t, resp, "EventSeries", "event/get-event-series")
	reread, err := csil.DecodeEventSeries(resp.Payload)
	if err != nil {
		t.Fatalf("decoding EventSeries: %v", err)
	}

	if reread.OccurrenceCount <= 100 {
		t.Fatalf("the fixture materialized only %d occurrences, which does not exceed one page",
			reread.OccurrenceCount)
	}
	if uint64(len(expanded.Events)) != reread.OccurrenceCount {
		t.Errorf("expand returned %d of %d materialized occurrences",
			len(expanded.Events), reread.OccurrenceCount)
	}
	for _, e := range expanded.Events {
		if e.SeriesId == nil || *e.SeriesId != series.Id {
			t.Fatalf("expand returned an event that is not part of the series: %s", e.Id)
		}
	}
}

func boolPtr(b bool) *bool       { return &b }
func uint64Ptr(n uint64) *uint64 { return &n }

// TestTheDevelopmentAdministratorArrivesAtAnyDomain covers the affordance a
// person meets first: sign in as devadmin, at whatever domain, and hold the
// role. Two instances of tinku are two domains, so pinning the role to one
// domain would mean granting it by hand on the second — for an account that
// already carries no identity assertion at all.
//
// Nobody else gets it. Only development sign-in reaches this path, and
// `tinku serve` does not build the service outside a dev or nonprod
// environment.
func TestTheDevelopmentAdministratorArrivesAtAnyDomain(t *testing.T) {
	env := newTestEnv(t)

	for _, domain := range []string{"example.test", "somewhere.else.test"} {
		_, profile := env.loginAt(t, "devadmin", domain)
		if profile.LinkkeysDomain != domain {
			t.Fatalf("domain = %q, want %q", profile.LinkkeysDomain, domain)
		}
		user, err := env.Store.UserByHandle(context.Background(), "devadmin", domain)
		if err != nil {
			t.Fatalf("devadmin@%s is missing: %v", domain, err)
		}
		if !user.IsAdmin {
			t.Errorf("devadmin@%s does not hold the administrator role", domain)
		}
	}

	_, ada := env.loginAt(t, "ada", "example.test")
	other, err := env.Store.UserByHandle(context.Background(), "ada", "example.test")
	if err != nil {
		t.Fatalf("ada is missing: %v", err)
	}
	if other.IsAdmin {
		t.Error("an ordinary development sign-in came back with the administrator role")
	}
	if ada.Handle != "ada" {
		t.Errorf("handle = %q, want ada", ada.Handle)
	}
}
