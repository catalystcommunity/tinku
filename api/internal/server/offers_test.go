package server_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/csil"
)

// The rules about a gathering moving under an organization. There are two
// paths and they exist for different reasons — see csilservices/offers.go.

func (e *testEnv) createOrganization(t *testing.T, client *http.Client, name string) csil.Organization {
	t.Helper()
	resp := e.call(t, client, "organization", "create-organization",
		csil.EncodeCreateOrganizationRequest(csil.CreateOrganizationRequest{
			Name: name, Blurb: "a blurb", Description: "a longer description",
		}))
	requireReply(t, resp, "Organization", "organization/create-organization")
	organization, err := csil.DecodeOrganization(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Organization: %v", err)
	}
	return organization
}

func (e *testEnv) offer(t *testing.T, client *http.Client, gatheringID csil.GatheringID, organizationID csil.OrganizationID) csil.GatheringOffer {
	t.Helper()
	resp := e.call(t, client, "gathering", "offer-gathering",
		csil.EncodeOfferGatheringRequest(csil.OfferGatheringRequest{
			GatheringId: gatheringID, OrganizationId: organizationID, Note: "we would like a home",
		}))
	requireReply(t, resp, "GatheringOffer", "gathering/offer-gathering")
	offer, err := csil.DecodeGatheringOffer(resp.Payload)
	if err != nil {
		t.Fatalf("decoding GatheringOffer: %v", err)
	}
	return offer
}

// TestAnOfferNeedsBothSides is the whole point of the offer: neither party
// can move a gathering into an organization alone.
func TestAnOfferNeedsBothSides(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada") // owns the gathering
	bob, _ := env.login(t, "bob") // owns the organization
	eve, _ := env.login(t, "eve") // owns neither

	gathering := env.createGathering(t, ada, "Thursday Bouldering")
	organization := env.createOrganization(t, bob, "Front Range Climbers")

	// Bob cannot help himself to Ada's gathering.
	resp := env.call(t, bob, "gathering", "offer-gathering",
		csil.EncodeOfferGatheringRequest(csil.OfferGatheringRequest{
			GatheringId: gathering.Id, OrganizationId: organization.Id,
		}))
	requireServiceError(t, resp, 3, "an organization owner offering somebody else's gathering")

	offer := env.offer(t, ada, gathering.Id, organization.Id)
	if offer.Status != "pending" {
		t.Fatalf("a new offer is %q, want pending", offer.Status)
	}

	// Ada cannot accept her own offer: she is not the receiving side.
	resp = env.call(t, ada, "gathering", "respond-to-gathering-offer",
		csil.EncodeRespondToGatheringOfferRequest(csil.RespondToGatheringOfferRequest{
			OfferId: offer.Id, Accept: true,
		}))
	requireServiceError(t, resp, 3, "the offerer accepting their own offer")

	// Nor can a bystander.
	resp = env.call(t, eve, "gathering", "respond-to-gathering-offer",
		csil.EncodeRespondToGatheringOfferRequest(csil.RespondToGatheringOfferRequest{
			OfferId: offer.Id, Accept: true,
		}))
	requireServiceError(t, resp, 3, "a bystander accepting an offer")

	// The receiving side can.
	resp = env.call(t, bob, "gathering", "respond-to-gathering-offer",
		csil.EncodeRespondToGatheringOfferRequest(csil.RespondToGatheringOfferRequest{
			OfferId: offer.Id, Accept: true,
		}))
	requireReply(t, resp, "GatheringOffer", "gathering/respond-to-gathering-offer")

	// Answering twice is refused rather than racing: the status check is in
	// the UPDATE.
	resp = env.call(t, bob, "gathering", "respond-to-gathering-offer",
		csil.EncodeRespondToGatheringOfferRequest(csil.RespondToGatheringOfferRequest{
			OfferId: offer.Id, Accept: true,
		}))
	requireServiceError(t, resp, 1, "answering an offer twice")
}

// TestAcceptingAnOfferKeepsTheOldOwnerAndTheMembers covers what acceptance
// MEANS. It is the rule most likely to be broken by a later change, because
// "move it into the organization" sounds like a handover and is not one.
func TestAcceptingAnOfferKeepsTheOldOwnerAndTheMembers(t *testing.T) {
	env := newTestEnv(t)
	ada, adaProfile := env.login(t, "ada")
	bob, _ := env.login(t, "bob")
	carol, carolProfile := env.login(t, "carol")

	gathering := env.createGathering(t, ada, "Thursday Bouldering")
	organization := env.createOrganization(t, bob, "Front Range Climbers")

	// Carol is a MEMBER of the gathering. She joined a gathering; she did
	// not apply to an organization.
	resp := env.call(t, carol, "gathering", "join-gathering",
		csil.EncodeJoinGatheringRequest(csil.JoinGatheringRequest{GatheringId: gathering.Id}))
	requireReply(t, resp, "Gathering", "gathering/join-gathering")

	offer := env.offer(t, ada, gathering.Id, organization.Id)
	resp = env.call(t, bob, "gathering", "respond-to-gathering-offer",
		csil.EncodeRespondToGatheringOfferRequest(csil.RespondToGatheringOfferRequest{
			OfferId: offer.Id, Accept: true,
		}))
	requireReply(t, resp, "GatheringOffer", "gathering/respond-to-gathering-offer")

	// The organization is now AN owner, and Ada is still one.
	resp = env.call(t, ada, "gathering", "get-gathering",
		csil.EncodeGetGatheringRequest(csil.GetGatheringRequest{Id: gathering.Id}))
	requireReply(t, resp, "Gathering", "gathering/get-gathering")
	after, err := csil.DecodeGathering(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Gathering: %v", err)
	}
	var hasOrganization, hasAda bool
	for _, owner := range after.Owners {
		if owner.Kind == "organization" && owner.Id == string(organization.Id) {
			hasOrganization = true
		}
		if owner.Kind == "user" && owner.Id == string(adaProfile.Id) {
			hasAda = true
		}
	}
	if !hasOrganization {
		t.Error("after accepting, the organization does not own the gathering")
	}
	if !hasAda {
		t.Error("accepting an offer removed the owner who made it; an offer is not a surrender")
	}
	if !after.Viewer.CanEdit {
		t.Error("the original owner lost their own gathering")
	}

	// Ada is on the organization's roster now, because her ownership runs
	// through it. Carol is NOT: she joined a gathering.
	resp = env.call(t, bob, "organization", "list-organization-members",
		csil.EncodeListOrganizationMembersRequest(csil.ListOrganizationMembersRequest{
			OrganizationId: organization.Id,
		}))
	requireReply(t, resp, "OrganizationMemberList", "organization/list-organization-members")
	roster, err := csil.DecodeOrganizationMemberList(resp.Payload)
	if err != nil {
		t.Fatalf("decoding OrganizationMemberList: %v", err)
	}
	var adaOnRoster, carolOnRoster bool
	for _, m := range roster.Members {
		if m.UserId == adaProfile.Id {
			adaOnRoster = true
			if m.Role == "owner" {
				t.Error("accepting a gathering made its owner an OWNER of the organization")
			}
		}
		if m.UserId == carolProfile.Id {
			carolOnRoster = true
		}
	}
	if !adaOnRoster {
		t.Error("the gathering's owner is not on the organization's roster")
	}
	if carolOnRoster {
		t.Error("a member of the gathering was enrolled into the organization; members join gatherings, not organizations")
	}
}

// TestOnlyAnAdministratorAdopts: adoption is the one-sided path, and it is
// deliberately not available to somebody who could otherwise take a
// gathering that is not theirs.
func TestOnlyAnAdministratorAdopts(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	bob, bobProfile := env.login(t, "bob")

	gathering := env.createGathering(t, ada, "Loose Gathering")
	organization := env.createOrganization(t, bob, "Front Range Climbers")

	// Bob owns the organization and still cannot take a gathering into it.
	resp := env.call(t, bob, "gathering", "adopt-gathering",
		csil.EncodeAdoptGatheringRequest(csil.AdoptGatheringRequest{
			GatheringId: gathering.Id, OrganizationId: organization.Id,
		}))
	requireServiceError(t, resp, 3, "an organization owner adopting a gathering")

	env.makeAdmin(t, string(bobProfile.Id))
	resp = env.call(t, bob, "gathering", "adopt-gathering",
		csil.EncodeAdoptGatheringRequest(csil.AdoptGatheringRequest{
			GatheringId: gathering.Id, OrganizationId: organization.Id,
		}))
	requireReply(t, resp, "Gathering", "gathering/adopt-gathering")
	adopted, err := csil.DecodeGathering(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Gathering: %v", err)
	}
	var owned bool
	for _, owner := range adopted.Owners {
		if owner.Kind == "organization" && owner.Id == string(organization.Id) {
			owned = true
		}
	}
	if !owned {
		t.Error("an administrator adopted a gathering and the organization does not own it")
	}
}

// TestWithdrawingAnOfferIsTheOfferersOwn: the side that made an offer can
// take it back while it is pending, and the receiving side cannot withdraw
// on their behalf (they decline instead, which is a different record).
func TestWithdrawingAnOfferIsTheOfferersOwn(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	bob, _ := env.login(t, "bob")

	gathering := env.createGathering(t, ada, "Thursday Bouldering")
	organization := env.createOrganization(t, bob, "Front Range Climbers")
	offer := env.offer(t, ada, gathering.Id, organization.Id)

	resp := env.call(t, bob, "gathering", "withdraw-gathering-offer",
		csil.EncodeWithdrawGatheringOfferRequest(csil.WithdrawGatheringOfferRequest{OfferId: offer.Id}))
	requireServiceError(t, resp, 3, "the receiving side withdrawing an offer")

	resp = env.call(t, ada, "gathering", "withdraw-gathering-offer",
		csil.EncodeWithdrawGatheringOfferRequest(csil.WithdrawGatheringOfferRequest{OfferId: offer.Id}))
	requireReply(t, resp, "Empty", "gathering/withdraw-gathering-offer")

	// And the organization has nothing left to answer.
	resp = env.call(t, bob, "gathering", "list-gathering-offers",
		csil.EncodeListGatheringOffersRequest(csil.ListGatheringOffersRequest{
			OrganizationId: &organization.Id,
		}))
	requireReply(t, resp, "GatheringOfferList", "gathering/list-gathering-offers")
	list, err := csil.DecodeGatheringOfferList(resp.Payload)
	if err != nil {
		t.Fatalf("decoding GatheringOfferList: %v", err)
	}
	if len(list.Offers) != 0 {
		t.Errorf("a withdrawn offer is still pending: %d offers", len(list.Offers))
	}
}

// TestOffersAreOnlyVisibleToASide: an offer names two parties and says what
// one of them wants. Nobody else has business reading it.
func TestOffersAreOnlyVisibleToASide(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	bob, _ := env.login(t, "bob")
	eve, _ := env.login(t, "eve")

	gathering := env.createGathering(t, ada, "Thursday Bouldering")
	organization := env.createOrganization(t, bob, "Front Range Climbers")
	env.offer(t, ada, gathering.Id, organization.Id)

	for _, side := range []struct {
		name   string
		client *http.Client
		want   int
	}{
		{"the offering owner", ada, 1},
		{"the receiving organization's owner", bob, 1},
		{"a bystander", eve, 0},
	} {
		resp := env.call(t, side.client, "gathering", "list-gathering-offers",
			csil.EncodeListGatheringOffersRequest(csil.ListGatheringOffersRequest{}))
		requireReply(t, resp, "GatheringOfferList", "gathering/list-gathering-offers")
		list, err := csil.DecodeGatheringOfferList(resp.Payload)
		if err != nil {
			t.Fatalf("decoding GatheringOfferList: %v", err)
		}
		if len(list.Offers) != side.want {
			t.Errorf("%s sees %d offers, want %d", side.name, len(list.Offers), side.want)
		}
	}
}

// TestCreatingAGatheringUnderAnOrganization: the owner field on creation is
// the ordinary way a gathering starts inside an organization, and it is
// still gated on owning that organization.
func TestCreatingAGatheringUnderAnOrganization(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	eve, _ := env.login(t, "eve")

	organization := env.createOrganization(t, ada, "Front Range Climbers")
	owner := csil.OwnerRefInput{Kind: "organization", Id: string(organization.Id)}

	resp := env.call(t, eve, "gathering", "create-gathering",
		csil.EncodeCreateGatheringRequest(csil.CreateGatheringRequest{
			Name: "Not Yours", Owner: &owner,
		}))
	requireServiceError(t, resp, 3, "starting a gathering under somebody else's organization")

	resp = env.call(t, ada, "gathering", "create-gathering",
		csil.EncodeCreateGatheringRequest(csil.CreateGatheringRequest{
			Name: "Thursday Bouldering", Owner: &owner,
		}))
	requireReply(t, resp, "Gathering", "gathering/create-gathering")
	gathering, err := csil.DecodeGathering(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Gathering: %v", err)
	}
	if len(gathering.Owners) != 1 || gathering.Owners[0].Kind != "organization" {
		t.Fatalf("owners = %+v, want the organization alone", gathering.Owners)
	}

	// And the organization's owner owns it, without a row naming them.
	if !gathering.Viewer.CanEdit {
		t.Error("the organization's owner cannot edit a gathering their organization owns")
	}

	// The list filter finds it, which is what the organization's page uses.
	resp = env.call(t, ada, "gathering", "list-gatherings",
		csil.EncodeListGatheringsRequest(csil.ListGatheringsRequest{
			OwnedByOrganization: &organization.Id,
		}))
	requireReply(t, resp, "GatheringList", "gathering/list-gatherings")
	list, err := csil.DecodeGatheringList(resp.Payload)
	if err != nil {
		t.Fatalf("decoding GatheringList: %v", err)
	}
	if list.Total != 1 {
		t.Errorf("the organization owns %d gatherings, want 1", list.Total)
	}
}

// TestTheOrganizationTypeaheadPrefersTheName. The query matches the
// description too — a person may remember what an organization does rather
// than what it is called — but a name match ranks above it, because the
// list shows names alone and an answer whose reason is invisible reads as a
// wrong answer.
func TestTheOrganizationTypeaheadPrefersTheName(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")

	// "climbing" is in one name and in the other's description.
	env.createOrganization(t, ada, "Front Range Climbing")
	resp := env.call(t, ada, "organization", "create-organization",
		csil.EncodeCreateOrganizationRequest(csil.CreateOrganizationRequest{
			Name: "Boulder Society", Blurb: "we do climbing on Thursdays",
			Description: "climbing, mostly",
		}))
	requireReply(t, resp, "Organization", "organization/create-organization")

	query := "climbing"
	resp = env.call(t, ada, "organization", "list-organizations",
		csil.EncodeListOrganizationsRequest(csil.ListOrganizationsRequest{Query: &query}))
	requireReply(t, resp, "OrganizationList", "organization/list-organizations")
	list, err := csil.DecodeOrganizationList(resp.Payload)
	if err != nil {
		t.Fatalf("decoding OrganizationList: %v", err)
	}
	if list.Total != 2 {
		t.Fatalf("%d matches, want 2: a description match is still a match", list.Total)
	}
	if list.Organizations[0].Name != "Front Range Climbing" {
		t.Errorf("first match is %q, want the one with the word in its NAME", list.Organizations[0].Name)
	}
}

// TestTheCalendarFiltersEvents covers what the calendar asks for: a window,
// what is on for my groups, and one organization's events.
func TestTheCalendarFiltersEvents(t *testing.T) {
	env := newTestEnv(t)
	ada, _ := env.login(t, "ada")
	eve, _ := env.login(t, "eve")

	organization := env.createOrganization(t, ada, "Front Range Climbers")
	owner := csil.OwnerRefInput{Kind: "organization", Id: string(organization.Id)}
	resp := env.call(t, ada, "gathering", "create-gathering",
		csil.EncodeCreateGatheringRequest(csil.CreateGatheringRequest{Name: "Under The Org", Owner: &owner}))
	requireReply(t, resp, "Gathering", "gathering/create-gathering")
	underOrg, err := csil.DecodeGathering(resp.Payload)
	if err != nil {
		t.Fatalf("decoding Gathering: %v", err)
	}
	loose := env.createGathering(t, eve, "Somebody Else's")

	env.createEvent(t, ada, underOrg.Id, "Org night", 24*time.Hour)
	env.createEvent(t, eve, loose.Id, "Other night", 24*time.Hour)

	list := func(client *http.Client, req csil.ListEventsRequest) csil.EventList {
		t.Helper()
		resp := env.call(t, client, "event", "list-events", csil.EncodeListEventsRequest(req))
		requireReply(t, resp, "EventList", "event/list-events")
		out, err := csil.DecodeEventList(resp.Payload)
		if err != nil {
			t.Fatalf("decoding EventList: %v", err)
		}
		return out
	}

	// Everything, for a caller who filters nothing.
	if all := list(ada, csil.ListEventsRequest{}); all.Total != 2 {
		t.Errorf("unfiltered list has %d events, want 2", all.Total)
	}

	// One organization's events.
	byOrg := list(ada, csil.ListEventsRequest{OwnedByOrganization: &organization.Id})
	if byOrg.Total != 1 || byOrg.Events[0].Title != "Org night" {
		t.Errorf("the organization filter gave %+v, want the one event under it", byOrg.Events)
	}

	// "Mine" is ownership or membership, and Ada owns the organization that
	// owns the gathering — an ownership that no row states directly.
	mine := true
	forAda := list(ada, csil.ListEventsRequest{Mine: &mine})
	if forAda.Total != 1 || forAda.Events[0].Title != "Org night" {
		t.Errorf("mine gave %+v for Ada, want the event under her organization", forAda.Events)
	}

	// An anonymous caller belongs to nothing: an empty list, not an error.
	anon := list(&http.Client{}, csil.ListEventsRequest{Mine: &mine})
	if anon.Total != 0 || len(anon.Events) != 0 {
		t.Errorf("mine for an anonymous caller gave %d events, want none", len(anon.Events))
	}

	// A window bounds it.
	after := env.clock().Add(48 * time.Hour)
	none := list(ada, csil.ListEventsRequest{StartsAfter: &after})
	if none.Total != 0 {
		t.Errorf("a window past both events matched %d", none.Total)
	}
}
