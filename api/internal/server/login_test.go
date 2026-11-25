package server_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	lk "github.com/catalystcommunity/linkkeys/sdks/regular-rp/go/generated"

	"github.com/catalystcommunity/tinku/api/internal/config"
	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/csilservices"
	"github.com/catalystcommunity/tinku/api/internal/linkkeys"
	"github.com/catalystcommunity/tinku/api/internal/server"
)

// fakeRP is a linkkeys RP server at the generated-client transport seam.
// The real linkkeys.Client encodes every request with the generated codec,
// so this test sees the bytes that a real RP server would see. The Rp ops
// declare an RpError arm, so the client calls CallReply and the fake names
// the variant with the generated reply encoders.
type fakeRP struct {
	mu          sync.Mutex
	signed      []lk.RpSignRequest
	userinfo    []lk.RpUserInfoRequest
	domain      string
	audience    string
	userID      string
	application string // the assertion's application ID; empty means "tinku"
	claims      map[string]string
	nonceToSign string
}

func (f *fakeRP) Call(_ context.Context, service, op string, _ []byte) ([]byte, error) {
	return nil, fmt.Errorf("unexpected Call %s/%s: the Rp ops use CallReply", service, op)
}

func (f *fakeRP) CallReply(_ context.Context, service, op string, req []byte) (lk.WireReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if service != "Rp" {
		return lk.WireReply{}, fmt.Errorf("unexpected service %s", service)
	}
	reply := func(variant string, payload []byte, err error) (lk.WireReply, error) {
		if err != nil {
			return lk.WireReply{}, err
		}
		return lk.WireReply{Variant: &variant, Payload: payload}, nil
	}
	switch op {
	case "sign-request":
		in, err := lk.DecodeRpSignRequest(req)
		if err != nil {
			return lk.WireReply{}, err
		}
		f.signed = append(f.signed, in)
		f.nonceToSign = in.Nonce
		return reply(lk.EncodeRpSignRequestReply(lk.RpSignResponse{SignedRequest: "signed-by-rp"}, nil))
	case "decrypt-token":
		return reply(lk.EncodeRpDecryptTokenReply(lk.RpDecryptResponse{SignedAssertion: "signed-assertion"}, nil))
	case "verify-assertion":
		now := time.Now().UTC().Truncate(time.Second)
		application := f.application
		if application == "" {
			application = "tinku"
		}
		return reply(lk.EncodeRpVerifyAssertionReply(lk.RpVerifyResponse{
			Verified: true,
			Assertion: lk.IdentityAssertion{
				UserId:           f.userID,
				Domain:           f.domain,
				Audience:         f.audience,
				Nonce:            f.nonceToSign,
				IssuedAt:         now,
				ExpiresAt:        now.Add(5 * time.Minute),
				AuthorizedClaims: []string{"handle"},
				Application: lk.ApplicationRef{
					SubjectUserId: "rp-account", SubjectDomain: "rp.test", ApplicationId: application,
				},
			},
		}, nil))
	case "userinfo-fetch":
		in, err := lk.DecodeRpUserInfoRequest(req)
		if err != nil {
			return lk.WireReply{}, err
		}
		f.userinfo = append(f.userinfo, in)
		info := lk.UserInfo{UserId: f.userID, Domain: f.domain, DisplayName: "Ada L."}
		for claimType, value := range f.claims {
			info.Claims = append(info.Claims, lk.Claim{ClaimId: "c-" + claimType, UserId: f.userID, ClaimType: claimType, ClaimValue: []byte(value)})
		}
		return reply(lk.EncodeRpUserinfoFetchReply(info, nil))
	}
	return lk.WireReply{}, fmt.Errorf("unexpected op %s", op)
}

func newLoginServer(t *testing.T, rp *fakeRP) *httptest.Server {
	t.Helper()
	env := newTestEnv(t)
	cfg := env.cfg
	cfg.LinkkeysIDPDomain = "idp.test"
	cfg.LinkkeysIDPURL = "https://idp.test"
	cfg.LinkkeysDomain = "tinku.test"
	cfg.AppCallbackURL = "https://tinku.test/auth/callback"
	pki := linkkeys.NewWithTransport(rp, "tinku", cfg.LinkkeysIDPURL)

	sink := server.NewSessionSink()
	st := env.Store
	svcs := server.Services{
		Auth:         &csilservices.AuthService{Store: st, Cfg: cfg, Sink: sink, PKI: pki},
		Greeting:     &csilservices.GreetingService{Store: st},
		Organization: &csilservices.OrganizationService{Store: st, OriginDomain: cfg.OriginDomain()},
		Gathering:    &csilservices.GatheringService{Store: st, OriginDomain: cfg.OriginDomain()},
		Event:        &csilservices.EventService{Store: st, OriginDomain: cfg.OriginDomain()},
		Search:       &csilservices.SearchService{Store: st, OriginDomain: cfg.OriginDomain()},
		Admin:        &csilservices.AdminService{Store: st},
		Webhook:      &csilservices.WebhookService{Store: st, Cfg: cfg},
	}
	ts := httptest.NewServer(server.New(cfg, st, pki, svcs).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func loginClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Jar: jar,
		// The callback answers with a redirect to the SPA. The test reads
		// that answer; it does not follow it.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func beginLogin(t *testing.T, client *http.Client, ts *httptest.Server) string {
	t.Helper()
	resp := call(t, client, ts.URL, "auth", "begin-login",
		mustEncode(csil.EncodeBeginLoginRequest(csil.BeginLoginRequest{Domain: "idp.test"})))
	requireReply(t, resp, "BeginLoginResponse", "auth/begin-login")
	out, err := csil.DecodeBeginLoginResponse(resp.Payload)
	if err != nil {
		t.Fatalf("decoding begin-login reply: %v", err)
	}
	return out.RedirectUrl
}

// TestLoginSendsItsClaimRequest pins the sign-request contract. From
// linkkeys v0.16.11 the RP takes the application from the API-key account,
// so the request names no application. The shared RP servers have no
// claims config, so tinku sends its own claim request, with handle
// required.
func TestLoginSendsItsClaimRequest(t *testing.T) {
	rp := &fakeRP{domain: "idp.test", audience: "tinku.test", userID: "6f1c-opaque-id",
		claims: map[string]string{"handle": "Ada", "display_name": "Ada Lovelace"}}
	ts := newLoginServer(t, rp)
	client := loginClient(t)

	redirect := beginLogin(t, client, ts)
	if !strings.HasPrefix(redirect, "https://idp.test/auth/authorize?") ||
		!strings.Contains(redirect, "signed_request=signed-by-rp") {
		t.Fatalf("redirect = %q", redirect)
	}

	if len(rp.signed) != 1 {
		t.Fatalf("sign-request calls = %d, want 1", len(rp.signed))
	}
	got := rp.signed[0]
	if got.CallbackUrl != "https://tinku.test/auth/callback" {
		t.Errorf("callback_url = %q", got.CallbackUrl)
	}
	if got.RequestedClaims == nil {
		t.Fatal("requested_claims is absent: the shared RP would release nothing")
	}
	if len(got.RequestedClaims.Required) != 1 || got.RequestedClaims.Required[0].ClaimType != "handle" {
		t.Errorf("required claims = %+v, want handle only", got.RequestedClaims.Required)
	}
	if len(got.RequestedClaims.Optional) != 1 || got.RequestedClaims.Optional[0].ClaimType != "display_name" {
		t.Errorf("optional claims = %+v, want display_name only", got.RequestedClaims.Optional)
	}

	res, err := client.Get(ts.URL + "/auth/callback?encrypted_token=" + url.QueryEscape("sealed"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", res.StatusCode)
	}

	if len(rp.userinfo) != 1 || rp.userinfo[0].ApiBase != "https://idp.test" || rp.userinfo[0].Domain != "idp.test" {
		t.Errorf("userinfo-fetch = %+v, want api_base https://idp.test and domain idp.test", rp.userinfo)
	}

	who := call(t, client, ts.URL, "auth", "whoami", mustEncode(csil.EncodeEmpty(csil.Empty{})))
	requireReply(t, who, "UserProfile", "auth/whoami after login")
	profile, err := csil.DecodeUserProfile(who.Payload)
	if err != nil {
		t.Fatal(err)
	}
	// The handle is the released claim, lowercased, not the opaque user ID.
	if profile.Handle != "ada" || profile.LinkkeysDomain != "idp.test" || profile.DisplayName != "Ada Lovelace" {
		t.Errorf("profile = %+v, want ada@idp.test named Ada Lovelace", profile)
	}
}

// TestLoginWithoutAHandleIsRefused: handle is required. Tinku enforces it
// after userinfo-fetch and does not fall back to the opaque user ID.
func TestLoginWithoutAHandleIsRefused(t *testing.T) {
	rp := &fakeRP{domain: "idp.test", audience: "tinku.test", userID: "6f1c-opaque-id",
		claims: map[string]string{"display_name": "Ada Lovelace"}}
	ts := newLoginServer(t, rp)
	client := loginClient(t)
	beginLogin(t, client, ts)

	res, err := client.Get(ts.URL + "/auth/callback?encrypted_token=sealed")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("callback status = %d, want 400", res.StatusCode)
	}
}

// TestLoginFromAnotherDomainIsRefused: the RP checks the signature against
// the expected domain's keys but does not compare the domain field.
func TestLoginFromAnotherDomainIsRefused(t *testing.T) {
	rp := &fakeRP{domain: "evil.test", audience: "tinku.test", userID: "u1",
		claims: map[string]string{"handle": "ada"}}
	ts := newLoginServer(t, rp)
	client := loginClient(t)
	beginLogin(t, client, ts)

	res, err := client.Get(ts.URL + "/auth/callback?encrypted_token=sealed")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("callback status = %d, want 403", res.StatusCode)
	}
}

// TestLoginForAnotherApplicationIsRefused: the assertion names the RP
// account's application. An assertion for any other application means the
// RP account is not tinku's, and tinku refuses it.
func TestLoginForAnotherApplicationIsRefused(t *testing.T) {
	rp := &fakeRP{domain: "idp.test", audience: "tinku.test", userID: "u1", application: "someone-else",
		claims: map[string]string{"handle": "ada"}}
	ts := newLoginServer(t, rp)
	client := loginClient(t)
	beginLogin(t, client, ts)

	res, err := client.Get(ts.URL + "/auth/callback?encrypted_token=sealed")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode == http.StatusFound {
		t.Fatal("callback logged in with an assertion for another application")
	}
	if len(rp.userinfo) != 0 {
		t.Errorf("userinfo-fetch ran %d times for a refused assertion", len(rp.userinfo))
	}
}

func TestApplicationIDRule(t *testing.T) {
	for id, ok := range map[string]bool{
		"tinku": true, "tinku.prod_1-a": true, "": false, "has space": false,
		strings.Repeat("a", 128): true, strings.Repeat("a", 129): false, "tínku": false,
	} {
		if err := linkkeys.ValidApplicationID(id); (err == nil) != ok {
			t.Errorf("ValidApplicationID(%q) error = %v, want ok=%v", id, err, ok)
		}
	}
	cfg := config.Config{FederationApplicationID: "tinku"}
	if cfg.LinkkeysAppID() != "tinku" {
		t.Errorf("LinkkeysAppID falls back to %q, want the federation application ID", cfg.LinkkeysAppID())
	}
}
