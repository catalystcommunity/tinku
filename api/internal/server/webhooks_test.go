package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/webhooks"
)

// The rules a webhook has to keep: who may set one, how many, what each one
// hears about, and what a delivery carries.

func (e *testEnv) createWebhook(t *testing.T, client *http.Client, kind csil.WebhookOwnerKind, ownerID, url string, scope csil.WebhookScope) csil.WebhookWithSecret {
	t.Helper()
	return e.createWebhookWith(t, client, csil.CreateWebhookRequest{
		OwnerKind: kind, OwnerId: ownerID, Url: url, Scope: scope, Note: "a note",
	})
}

func (e *testEnv) createWebhookWith(t *testing.T, client *http.Client, req csil.CreateWebhookRequest) csil.WebhookWithSecret {
	t.Helper()
	resp := e.call(t, client, "webhook", "create-webhook", csil.EncodeCreateWebhookRequest(req))
	requireReply(t, resp, "WebhookWithSecret", "webhook/create-webhook")
	created, err := csil.DecodeWebhookWithSecret(resp.Payload)
	if err != nil {
		t.Fatalf("decoding WebhookWithSecret: %v", err)
	}
	return created
}

// queued reads the deliveries waiting to go out, which is what a dispatch
// actually produces. Nothing is sent in a test: the sender is a separate
// loop and this asserts on the queue it drains.
func (e *testEnv) queued(t *testing.T) []webhookPayload {
	t.Helper()
	due, err := e.Store.DueWebhookDeliveries(context.Background(), time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatalf("reading due deliveries: %v", err)
	}
	out := make([]webhookPayload, 0, len(due))
	for _, d := range due {
		var p webhookPayload
		if err := json.Unmarshal(d.Payload, &p); err != nil {
			t.Fatalf("decoding a queued payload: %v", err)
		}
		out = append(out, p)
	}
	return out
}

type webhookPayload struct {
	Action    string          `json:"action"`
	Subject   string          `json:"subject"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Gathering string          `json:"gathering_id"`
	Instance  string          `json:"instance"`
	Details   *webhookDetails `json:"details"`
}

type webhookDetails struct {
	Description string `json:"description"`
	Timezone    string `json:"timezone"`
	OnlineURL   string `json:"online_url"`
}

// TestOnlyAnOwnerManagesWebhooks. A webhook's URL and its failure history
// say where somebody's systems are and whether they are healthy, so the list
// is an owner's to read, not a member's.
func TestOnlyAnOwnerManagesWebhooks(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	eve, _ := env.login(t, "eve")

	gathering := env.createGathering(t, ada, "Thursday Bouldering")

	resp := env.call(t, eve, "webhook", "create-webhook",
		csil.EncodeCreateWebhookRequest(csil.CreateWebhookRequest{
			OwnerKind: "gathering", OwnerId: string(gathering.Id),
			Url: "https://example.test/hook", Scope: "all",
		}))
	requireServiceError(t, resp, 3, "a bystander adding a webhook")

	resp = env.call(t, eve, "webhook", "list-webhooks",
		csil.EncodeListWebhooksRequest(csil.ListWebhooksRequest{
			OwnerKind: "gathering", OwnerId: string(gathering.Id),
		}))
	requireServiceError(t, resp, 3, "a bystander reading webhooks")

	created := env.createWebhook(t, ada, "gathering", string(gathering.Id), "https://example.test/hook", "all")
	if created.Secret == "" {
		t.Error("no signing secret was returned; a receiver cannot tell a delivery from anybody else's POST")
	}

	// The secret is returned once and never again.
	resp = env.call(t, ada, "webhook", "list-webhooks",
		csil.EncodeListWebhooksRequest(csil.ListWebhooksRequest{
			OwnerKind: "gathering", OwnerId: string(gathering.Id),
		}))
	requireReply(t, resp, "WebhookList", "webhook/list-webhooks")
	list, err := csil.DecodeWebhookList(resp.Payload)
	if err != nil {
		t.Fatalf("decoding WebhookList: %v", err)
	}
	if len(list.Webhooks) != 1 {
		t.Fatalf("%d webhooks, want 1", len(list.Webhooks))
	}
	if list.Limit == 0 {
		t.Error("the list does not say what the limit is, so a client has to discover it by being refused")
	}
}

// TestFiveWebhooksPerLevel. The limit is enforced in the INSERT, not only
// checked before it.
func TestFiveWebhooksPerLevel(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	gathering := env.createGathering(t, ada, "Thursday Bouldering")

	for i := range 5 {
		env.createWebhook(t, ada, "gathering", string(gathering.Id),
			"https://example.test/hook/"+string(rune('a'+i)), "all")
	}

	resp := env.call(t, ada, "webhook", "create-webhook",
		csil.EncodeCreateWebhookRequest(csil.CreateWebhookRequest{
			OwnerKind: "gathering", OwnerId: string(gathering.Id),
			Url: "https://example.test/hook/six", Scope: "all",
		}))
	requireServiceError(t, resp, 1, "a sixth webhook")
}

// TestAWebhookHearsWhatIsUnderIt is the routing rule: a gathering's webhook
// hears about its events, and an organization's webhook hears about the
// gatherings it owns and their events.
func TestAWebhookHearsWhatIsUnderIt(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")

	organization := env.createOrganization(t, ada, "Front Range Climbers")
	owner := csil.OwnerRefInput{Kind: "organization", Id: string(organization.Id)}
	resp := env.call(t, ada, "gathering", "create-gathering",
		csil.EncodeCreateGatheringRequest(csil.CreateGatheringRequest{
			Name: "Thursday Bouldering", Owner: &owner,
		}))
	requireReply(t, resp, "Gathering", "gathering/create-gathering")
	gathering, err := csil.DecodeGathering(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Gathering: %v", err)
	}

	env.createWebhook(t, ada, "organization", string(organization.Id), "https://example.test/org", "all")
	env.createWebhook(t, ada, "gathering", string(gathering.Id), "https://example.test/gathering", "all")

	// Both webhooks were added after the gathering existed, so the queue is
	// empty until something else changes.
	if got := env.queued(t); len(got) != 0 {
		t.Fatalf("%d deliveries queued before any change", len(got))
	}

	event := env.createEvent(t, ada, gathering.Id, "Bouldering night", time.Hour)

	queued := env.queued(t)
	if len(queued) != 2 {
		t.Fatalf("%d deliveries for one event, want 2 (the gathering's webhook and the organization's)", len(queued))
	}
	for _, p := range queued {
		if p.Subject != "event" || p.Action != "created" {
			t.Errorf("payload is %s/%s, want event/created", p.Subject, p.Action)
		}
		if p.ID != string(event.Id) {
			t.Errorf("payload names %s, want the event %s", p.ID, event.Id)
		}
		if p.Gathering != string(gathering.Id) {
			t.Error("the payload does not say which gathering the event is under")
		}
		if p.Instance == "" {
			t.Error("the payload does not name the instance; a receiver listening to two of them cannot tell which sent this")
		}
	}
}

// TestStructureOnlyDropsEvents. The narrowed scope is for an integration
// that tracks WHAT EXISTS rather than what is scheduled — "every event under
// every gathering we own" is a firehose.
func TestStructureOnlyDropsEvents(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")

	organization := env.createOrganization(t, ada, "Front Range Climbers")
	owner := csil.OwnerRefInput{Kind: "organization", Id: string(organization.Id)}
	env.createWebhook(t, ada, "organization", string(organization.Id), "https://example.test/org", "structure_only")

	resp := env.call(t, ada, "gathering", "create-gathering",
		csil.EncodeCreateGatheringRequest(csil.CreateGatheringRequest{
			Name: "Thursday Bouldering", Owner: &owner,
		}))
	requireReply(t, resp, "Gathering", "gathering/create-gathering")
	gathering, err := csil.DecodeGathering(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Gathering: %v", err)
	}

	// The gathering itself IS structure, so it is reported.
	queued := env.queued(t)
	if len(queued) != 1 || queued[0].Subject != "gathering" {
		t.Fatalf("queued %+v, want one gathering delivery", queued)
	}

	// Its events are not.
	env.createEvent(t, ada, gathering.Id, "Bouldering night", time.Hour)
	queued = env.queued(t)
	for _, p := range queued {
		if p.Subject == "event" {
			t.Error("a structure_only webhook was sent an event")
		}
	}
}

// TestASwitchedOffWebhookIsNotDelivered. Switching one off has to stop
// deliveries that were already queued, or an owner who turns off a noisy
// endpoint keeps hearing from it until the backlog drains.
func TestASwitchedOffWebhookIsNotDelivered(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	gathering := env.createGathering(t, ada, "Thursday Bouldering")
	created := env.createWebhook(t, ada, "gathering", string(gathering.Id), "https://example.test/hook", "all")

	env.createEvent(t, ada, gathering.Id, "Bouldering night", time.Hour)
	if got := env.queued(t); len(got) != 1 {
		t.Fatalf("%d deliveries queued, want 1", len(got))
	}

	off := false
	resp := env.call(t, ada, "webhook", "update-webhook",
		csil.EncodeUpdateWebhookRequest(csil.UpdateWebhookRequest{Id: created.Webhook.Id, Active: &off}))
	requireReply(t, resp, "Webhook", "webhook/update-webhook")

	if got := env.queued(t); len(got) != 0 {
		t.Errorf("%d deliveries are still due for a webhook that is switched off", len(got))
	}
}

// TestAWebhookUrlMustBeHttps outside a dev environment. A delivery carries a
// signature and the names of things that are not always public.
func TestAWebhookUrlIsCheckedForScheme(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	gathering := env.createGathering(t, ada, "Thursday Bouldering")

	// This env is dev, where http is allowed so a local receiver can be
	// tried at all. Anything that is not http(s) is refused everywhere.
	resp := env.call(t, ada, "webhook", "create-webhook",
		csil.EncodeCreateWebhookRequest(csil.CreateWebhookRequest{
			OwnerKind: "gathering", OwnerId: string(gathering.Id),
			Url: "ftp://example.test/hook", Scope: "all",
		}))
	requireServiceError(t, resp, 1, "a webhook URL that is not http(s)")

	resp = env.call(t, ada, "webhook", "create-webhook",
		csil.EncodeCreateWebhookRequest(csil.CreateWebhookRequest{
			OwnerKind: "gathering", OwnerId: string(gathering.Id),
			Url: "not a url at all", Scope: "all",
		}))
	requireServiceError(t, resp, 1, "a webhook URL that is not a URL")
}

// TestTheSignatureIsOverTheExactBytes. A receiver checks the signature
// against the body as it arrived; signing anything else — a re-encoded
// structure, say — makes an honest delivery read as a forgery.
func TestTheSignatureIsOverTheExactBytes(t *testing.T) {
	body := []byte(`{"action":"created","subject":"event"}`)
	first := webhooks.Sign("a-secret", body)
	if first == "" {
		t.Fatal("no signature")
	}
	if again := webhooks.Sign("a-secret", body); again != first {
		t.Error("the same body and secret produced two signatures")
	}
	if other := webhooks.Sign("another-secret", body); other == first {
		t.Error("two different secrets produced the same signature")
	}
	changed := append(append([]byte{}, body...), ' ')
	if after := webhooks.Sign("a-secret", changed); after == first {
		t.Error("a changed body produced the same signature")
	}
}

// TestDetailsGoOnlyToAWebhookThatAskedForThem.
//
// The default is a pointer: the URL belongs to somebody this server has no
// say over. An owner can switch the record on — it is their description to
// send — and that choice reaches one webhook without reaching the one beside
// it.
func TestDetailsGoOnlyToAWebhookThatAskedForThem(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	gathering := env.createGathering(t, ada, "Thursday Bouldering")

	env.createWebhookWith(t, ada, csil.CreateWebhookRequest{
		OwnerKind: "gathering", OwnerId: string(gathering.Id),
		Url: "https://example.test/quiet", Scope: "all",
	})
	env.createWebhookWith(t, ada, csil.CreateWebhookRequest{
		OwnerKind: "gathering", OwnerId: string(gathering.Id),
		Url: "https://example.test/full", Scope: "all", IncludeDetails: true,
	})

	env.createEvent(t, ada, gathering.Id, "Bouldering night", time.Hour)

	queued := env.queued(t)
	if len(queued) != 2 {
		t.Fatalf("%d deliveries, want one for each webhook", len(queued))
	}
	var withDetails, without int
	for _, p := range queued {
		if p.Details == nil {
			without++
			continue
		}
		withDetails++
		if p.Details.Description != "what happens at Bouldering night" {
			t.Errorf("details carry description %q, want the event's own", p.Details.Description)
		}
		if p.Details.Timezone == "" {
			t.Error("details carry no timezone, so a receiver cannot say when the event is")
		}
	}
	if withDetails != 1 || without != 1 {
		t.Errorf("%d detailed and %d pointer deliveries, want one of each", withDetails, without)
	}
}

// TestSwitchingDetailsOnLater. The choice is a setting, not a decision taken
// once at creation: an owner who set a webhook up before they had a receiver
// for the record must be able to change their mind.
func TestSwitchingDetailsOnLater(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	gathering := env.createGathering(t, ada, "Thursday Bouldering")
	created := env.createWebhook(t, ada, "gathering", string(gathering.Id), "https://example.test/hook", "all")
	if created.Webhook.IncludeDetails {
		t.Error("a new webhook sends the record by default; the safe answer is a pointer")
	}

	on := true
	resp := env.call(t, ada, "webhook", "update-webhook",
		csil.EncodeUpdateWebhookRequest(csil.UpdateWebhookRequest{Id: created.Webhook.Id, IncludeDetails: &on}))
	requireReply(t, resp, "Webhook", "webhook/update-webhook")
	updated, err := csil.DecodeWebhook(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Webhook: %v", err)
	}
	if !updated.IncludeDetails {
		t.Fatal("switching the record on did not take")
	}

	env.createEvent(t, ada, gathering.Id, "Bouldering night", time.Hour)
	queued := env.queued(t)
	if len(queued) != 1 || queued[0].Details == nil {
		t.Fatalf("queued %+v, want one delivery carrying the record", queued)
	}
}
