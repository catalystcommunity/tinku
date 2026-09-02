package server_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/config"
	"github.com/catalystcommunity/tinku/api/internal/csilservices"
	"github.com/catalystcommunity/tinku/api/internal/store"
	_ "github.com/catalystcommunity/tinku/api/internal/store/backends"
	"github.com/catalystcommunity/tinku/coredb"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/server"
	"github.com/catalystcommunity/tinku/api/internal/transport"
)

// newTestServer boots the real HTTP handler over a real SQLite database in a
// temp directory. Nothing here is a mock: the migration, the store, the
// dispatcher, the CBOR envelopes and the cookie handling are all the
// production code paths. That is only affordable because the SQLite backend
// needs no server — which is most of why it exists.
func newTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	env := newTestEnv(t)
	return env.Server, env.newClient(t)
}

// call sends one CSIL-RPC envelope and returns the decoded response.
func call(t *testing.T, client *http.Client, baseURL, service, op string, payload []byte) transport.RpcResponse {
	t.Helper()

	body, err := transport.NewRpcRequest(service, op, payload).Encode()
	if err != nil {
		t.Fatalf("encoding %s/%s request: %v", service, op, err)
	}
	res, err := client.Post(baseURL+server.RPCMountPath, "application/cbor", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("calling %s/%s: %v", service, op, err)
	}
	defer res.Body.Close()

	// The carrier answers HTTP 200 for every well-formed request; the real
	// outcome is the envelope's status, never the HTTP status.
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s/%s: HTTP %d, want 200 (the envelope carries the outcome)", service, op, res.StatusCode)
	}
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading %s/%s response body: %v", service, op, err)
	}
	resp, err := transport.DecodeRpcResponse(raw)
	if err != nil {
		t.Fatalf("decoding %s/%s response: %v", service, op, err)
	}
	return resp
}

// TestGreetingRoundTrip is the hello-world path end to end: log in, leave a
// greeting, read it back in the listing.
func TestGreetingRoundTrip(t *testing.T) {
	ts, client := newTestServer(t)

	login := call(t, client, ts.URL, "devauth", "dev-login",
		csil.EncodeDevLoginRequest(csil.DevLoginRequest{Handle: "ada", Domain: "example.test"}))
	requireReply(t, login, "UserProfile", "devauth/dev-login")

	profile, err := csil.DecodeUserProfile(login.Payload)
	if err != nil {
		t.Fatalf("decoding UserProfile: %v", err)
	}
	if profile.Handle != "ada" {
		t.Errorf("logged in as %q, want %q", profile.Handle, "ada")
	}

	created := call(t, client, ts.URL, "greeting", "create-greeting",
		csil.EncodeCreateGreetingRequest(csil.CreateGreetingRequest{Message: "hello, world"}))
	requireReply(t, created, "Greeting", "greeting/create-greeting")

	greeting, err := csil.DecodeGreeting(created.Payload)
	if err != nil {
		t.Fatalf("decoding Greeting: %v", err)
	}
	if greeting.Message != "hello, world" {
		t.Errorf("stored message %q, want %q", greeting.Message, "hello, world")
	}
	if greeting.AuthorHandle == nil || *greeting.AuthorHandle != "ada" {
		t.Errorf("greeting author = %v, want ada", greeting.AuthorHandle)
	}

	listed := call(t, client, ts.URL, "greeting", "list-greetings", csil.EncodeEmpty(csil.Empty{}))
	requireReply(t, listed, "GreetingList", "greeting/list-greetings")

	list, err := csil.DecodeGreetingList(listed.Payload)
	if err != nil {
		t.Fatalf("decoding GreetingList: %v", err)
	}
	if len(list.Greetings) != 1 || list.Greetings[0].Id != greeting.Id {
		t.Fatalf("listing returned %d greeting(s), want the one just created", len(list.Greetings))
	}
}

// TestAnonymousCanReadButNotWrite pins the authorization split: reading is
// open, writing needs a session, and the refusal arrives as the declared
// ServiceError arm rather than a transport failure.
func TestAnonymousCanReadButNotWrite(t *testing.T) {
	ts, _ := newTestServer(t)
	anonymous := &http.Client{} // no cookie jar, so no session

	listed := call(t, anonymous, ts.URL, "greeting", "list-greetings", csil.EncodeEmpty(csil.Empty{}))
	requireReply(t, listed, "GreetingList", "greeting/list-greetings")

	refused := call(t, anonymous, ts.URL, "greeting", "create-greeting",
		csil.EncodeCreateGreetingRequest(csil.CreateGreetingRequest{Message: "hello"}))
	requireReply(t, refused, "ServiceError", "greeting/create-greeting")

	serviceErr, err := csil.DecodeServiceError(refused.Payload)
	if err != nil {
		t.Fatalf("decoding ServiceError: %v", err)
	}
	if serviceErr.Code != csilservices.CodeUnauthenticated {
		t.Errorf("refusal code = %d, want %d (unauthenticated)", serviceErr.Code, csilservices.CodeUnauthenticated)
	}
}

// TestLogoutEndsTheSession covers the half of logout only the HTTP layer can
// do — expiring the cookie — together with the half the service does.
func TestLogoutEndsTheSession(t *testing.T) {
	ts, client := newTestServer(t)

	call(t, client, ts.URL, "devauth", "dev-login",
		csil.EncodeDevLoginRequest(csil.DevLoginRequest{Handle: "grace", Domain: "example.test"}))

	whoami := call(t, client, ts.URL, "auth", "whoami", csil.EncodeEmpty(csil.Empty{}))
	requireReply(t, whoami, "UserProfile", "auth/whoami before logout")

	call(t, client, ts.URL, "auth", "logout", csil.EncodeEmpty(csil.Empty{}))

	after := call(t, client, ts.URL, "auth", "whoami", csil.EncodeEmpty(csil.Empty{}))
	requireReply(t, after, "ServiceError", "auth/whoami after logout")
}

// TestUnknownOpIsATransportStatus pins the layering: an op this server does
// not have is a TRANSPORT failure (status 2), not an application error — the
// two are different channels and clients branch on them differently.
func TestUnknownOpIsATransportStatus(t *testing.T) {
	ts, client := newTestServer(t)

	resp := call(t, client, ts.URL, "greeting", "no-such-op", csil.EncodeEmpty(csil.Empty{}))
	if resp.Status.Code() != transport.StatusUnknownServiceOrOp.Code() {
		t.Errorf("status = %d (%s), want %d (unknown-service-or-op)",
			resp.Status.Code(), resp.Status.Name(), transport.StatusUnknownServiceOrOp.Code())
	}
}

// TestDevAuthAbsentInProd pins the gate: with a prod env the service is not
// registered at all, so the op is indistinguishable from one this build does
// not have.
func TestDevAuthAbsentInProd(t *testing.T) {
	dbURI := "sqlite:" + filepath.Join(t.TempDir(), "test.db")
	if err := coredb.Up(dbURI); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	st, err := store.Open(dbURI)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// DevAuthEnabled is true and the env is prod, which must not be enough.
	cfg := config.Config{Env: "prod", DevAuthEnabled: true, SessionTTL: time.Hour, CORSOrigins: []string{"*"}}
	if cfg.DevAuthAllowed() {
		t.Fatal("DevAuthAllowed returned true for a prod environment")
	}

	// Every service but DevAuth: buildRoutes calls a method on each one,
	// so a partially-filled Services is a nil dereference rather than a
	// server with fewer routes. DevAuth is the one field that is nil on
	// purpose, and the whole service disappears from the table when it is.
	svcs := server.Services{
		Auth:         &csilservices.AuthService{Store: st, Cfg: cfg},
		Greeting:     &csilservices.GreetingService{Store: st},
		Organization: &csilservices.OrganizationService{Store: st, OriginDomain: "tinku.test"},
		Gathering:    &csilservices.GatheringService{Store: st, OriginDomain: "tinku.test"},
		Event:        &csilservices.EventService{Store: st},
		Search:       &csilservices.SearchService{Store: st},
		Admin:        &csilservices.AdminService{Store: st},
		Webhook:      &csilservices.WebhookService{Store: st, Cfg: cfg},
	}
	ts := httptest.NewServer(server.New(cfg, st, nil, svcs).Handler())
	t.Cleanup(ts.Close)

	resp := call(t, &http.Client{}, ts.URL, "devauth", "dev-login",
		csil.EncodeDevLoginRequest(csil.DevLoginRequest{Handle: "mallory", Domain: "example.test"}))
	if resp.Status.Code() != transport.StatusUnknownServiceOrOp.Code() {
		t.Errorf("dev-login on a prod server answered status %d (%s), want unknown-service-or-op",
			resp.Status.Code(), resp.Status.Name())
	}
}

// requireReply asserts the envelope carries a typed reply of the named
// variant, failing with the transport error when it does not.
func requireReply(t *testing.T, resp transport.RpcResponse, variant, what string) {
	t.Helper()
	if !resp.Status.IsOk() {
		errText := ""
		if resp.Error != nil {
			errText = *resp.Error
		}
		t.Fatalf("%s: transport status %s: %s", what, resp.Status.Name(), errText)
	}
	if resp.Variant == nil || *resp.Variant != variant {
		got := "<none>"
		if resp.Variant != nil {
			got = *resp.Variant
		}
		t.Fatalf("%s: reply variant %s, want %s", what, got, variant)
	}
}

// TestCredentialedCorsEchoesTheOrigin.
//
// The client and the api are two origins — one host, two ports — and every
// call carries the session cookie. A browser REFUSES a credentialed
// response whose Access-Control-Allow-Origin is the literal "*", so the
// permissive default has to echo the caller's origin instead. Nothing in a
// Go test can see that refusal; this asserts the header the browser would
// have judged.
func TestCredentialedCorsEchoesTheOrigin(t *testing.T) {
	env := newTestEnv(t)
	const origin = "http://somehost.tld:8080"

	// The preflight the browser sends before a cross-origin POST.
	req, err := http.NewRequest(http.MethodOptions, env.Server.URL+"/csil/v1/rpc", nil)
	if err != nil {
		t.Fatalf("building the preflight: %v", err)
	}
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // nothing is read from it

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("Access-Control-Allow-Origin = %q, want the caller's origin %q "+
			"(a browser drops a credentialed response that answers \"*\")", got, origin)
	}
	if got := resp.Header.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want true; without it the "+
			"session cookie is not sent and every call looks logged out", got)
	}
	// Vary: Origin, or a cache hands one origin's answer to another.
	if vary := resp.Header.Get("Vary"); !strings.Contains(vary, "Origin") {
		t.Errorf("Vary = %q, want it to include Origin", vary)
	}
}

// TestTheSessionCookieCrossesPorts. app on :8080 and api on :5080 are two
// ORIGINS and one SITE, so a Lax cookie travels between them — which is why
// lax stays the default and "none" exists only for a genuinely cross-site
// split.
func TestTheSessionCookieCrossesPorts(t *testing.T) {
	env := newTestEnv(t)
	client := env.newClient(t)

	resp := env.call(t, client, "devauth", "dev-login",
		csil.EncodeDevLoginRequest(csil.DevLoginRequest{Handle: "ada", Domain: "example.test"}))
	requireReply(t, resp, "UserProfile", "devauth/dev-login")

	jar := client.Jar
	if jar == nil {
		t.Fatal("the test client has no cookie jar")
	}
	parsed, err := url.Parse(env.Server.URL)
	if err != nil {
		t.Fatalf("parsing the server URL: %v", err)
	}
	cookies := jar.Cookies(parsed)
	if len(cookies) == 0 {
		t.Fatal("no session cookie was set")
	}
}
