package csilservices

import (
	"context"
	"errors"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/store"
)

// Moving a gathering under an organization.
//
// There are two ways, and they are different on purpose:
//
//   - An OFFER is two-sided. The gathering's owner offers, an owner of the
//     organization accepts. Neither side can do it alone, because both are
//     taking on something: the organization takes on a gathering it will be
//     answerable for, and the gathering takes on owners who can now delete
//     it. A one-sided version of this is somebody helping themselves to
//     somebody else's work, in whichever direction it runs.
//
//   - ADOPTION is one-sided and administrators only. It is the tool for a
//     gathering that was started loose and belongs to an organization that
//     already exists — a tidying job, done by the one role the instance
//     already trusts with deletion.
//
// Accepting ADDS the organization as an owner rather than replacing anybody:
// an offer is not a surrender, and the person who started the gathering
// keeps their standing in it unless they drop it themselves.

// offerViewer says what the caller may do with one offer. It is the same
// pattern as every other viewer block: the server decides, the client
// renders the answer.
//
// CanEdit is the receiving side's power to answer. CanDelete is the
// offering side's power to withdraw. They are never both true, because
// nobody should be able to accept their own offer.
func offerViewer(c caller, o *store.GatheringOffer, ownsOrganization, ownsGathering bool) csil.ViewerContext {
	pending := o.Status == store.OfferPending
	return csil.ViewerContext{
		IsAdmin:   c.IsAdmin,
		IsOwner:   ownsGathering,
		IsMember:  ownsOrganization || ownsGathering,
		CanEdit:   pending && ownsOrganization,
		CanDelete: pending && ownsGathering,
	}
}

// ownsOrganization reports whether the caller is an owner of one
// organization. An administrator is not: the admin role governs deletion and
// the instance, not somebody else's roster decisions.
func (s *GatheringService) ownsOrganization(ctx context.Context, c caller, organizationID string) (bool, error) {
	role, onRoster, err := s.Store.OrganizationRoleFor(ctx, organizationID, c.ID)
	if err != nil {
		return false, err
	}
	return onRoster && role == store.OrganizationRoleOwner, nil
}

func (s *GatheringService) offerReply(ctx context.Context, c caller, o *store.GatheringOffer) (csil.GatheringOffer, error) {
	ownsOrg, err := s.ownsOrganization(ctx, c, o.OrganizationID)
	if err != nil {
		return csil.GatheringOffer{}, err
	}
	access, err := s.Store.GatheringAccessFor(ctx, o.GatheringID, c.ID)
	if err != nil {
		return csil.GatheringOffer{}, err
	}
	return toGatheringOffer(o, offerViewer(c, o, ownsOrg, access.IsOwner)), nil
}

func (s *GatheringService) OfferGathering(ctx context.Context, req csil.OfferGatheringRequest) (csil.GatheringOffer, error) {
	c, err := authenticated(ctx, "offer a gathering")
	if err != nil {
		return csil.GatheringOffer{}, err
	}
	_, viewer, err := s.load(ctx, c, string(req.GatheringId))
	if err != nil {
		return csil.GatheringOffer{}, err
	}
	if !viewer.IsOwner {
		return csil.GatheringOffer{}, Forbidden("only an owner of this gathering can offer it")
	}

	organization, err := s.Store.OrganizationByID(ctx, string(req.OrganizationId))
	if errors.Is(err, store.ErrNotFound) {
		return csil.GatheringOffer{}, NotFound("organization", "no organization with that id")
	}
	if err != nil {
		return csil.GatheringOffer{}, err
	}

	// Already an owner: there is nothing to offer, and a pending offer that
	// can never change anything is a message the receiving side has to read
	// and dismiss for no reason.
	gathering, err := s.Store.GatheringByID(ctx, string(req.GatheringId))
	if err != nil {
		return csil.GatheringOffer{}, err
	}
	for _, owner := range gathering.Owners {
		if owner.Kind == store.OwnerKindOrganization && owner.ID == organization.ID {
			return csil.GatheringOffer{}, Invalid("organization_id", "that organization already owns this gathering")
		}
	}

	offer, err := s.Store.CreateGatheringOffer(ctx, string(req.GatheringId), organization.ID, c.ID, req.Note, time.Now())
	if err != nil {
		return csil.GatheringOffer{}, err
	}
	return s.offerReply(ctx, c, offer)
}

func (s *GatheringService) ListGatheringOffers(ctx context.Context, req csil.ListGatheringOffersRequest) (csil.GatheringOfferList, error) {
	c, err := authenticated(ctx, "read offers")
	if err != nil {
		return csil.GatheringOfferList{}, err
	}

	f := store.GatheringOfferFilter{ViewerID: c.ID}
	if req.OrganizationId != nil {
		f.OrganizationID = string(*req.OrganizationId)
	}
	if req.GatheringId != nil {
		f.GatheringID = string(*req.GatheringId)
	}
	if req.IncludeResolved != nil {
		f.IncludeResolved = *req.IncludeResolved
	}

	offers, total, err := s.Store.ListGatheringOffers(ctx, f)
	if err != nil {
		return csil.GatheringOfferList{}, err
	}
	out := make([]csil.GatheringOffer, 0, len(offers))
	for i := range offers {
		reply, err := s.offerReply(ctx, c, &offers[i])
		if err != nil {
			return csil.GatheringOfferList{}, err
		}
		out = append(out, reply)
	}
	return csil.GatheringOfferList{Offers: out, Total: uint64(total)}, nil
}

func (s *GatheringService) RespondToGatheringOffer(ctx context.Context, req csil.RespondToGatheringOfferRequest) (csil.GatheringOffer, error) {
	c, err := authenticated(ctx, "answer an offer")
	if err != nil {
		return csil.GatheringOffer{}, err
	}
	offer, err := s.Store.GatheringOfferByID(ctx, string(req.OfferId))
	if errors.Is(err, store.ErrNotFound) {
		return csil.GatheringOffer{}, NotFound("offer", "no offer with that id")
	}
	if err != nil {
		return csil.GatheringOffer{}, err
	}

	ownsOrg, err := s.ownsOrganization(ctx, c, offer.OrganizationID)
	if err != nil {
		return csil.GatheringOffer{}, err
	}
	if !ownsOrg {
		return csil.GatheringOffer{}, Forbidden("only an owner of the receiving organization can answer this offer")
	}

	status := store.OfferDeclined
	if req.Accept {
		status = store.OfferAccepted
	}
	// The status check lives in the UPDATE, so answering an offer somebody
	// else just answered is refused rather than racing them.
	resolved, err := s.Store.ResolveGatheringOffer(ctx, offer.ID, status, time.Now())
	if err != nil {
		return csil.GatheringOffer{}, err
	}
	if !resolved {
		return csil.GatheringOffer{}, Invalid("offer_id", "this offer has already been answered")
	}

	if req.Accept {
		if err := s.acceptOffer(ctx, offer); err != nil {
			return csil.GatheringOffer{}, err
		}
	}

	updated, err := s.Store.GatheringOfferByID(ctx, offer.ID)
	if err != nil {
		return csil.GatheringOffer{}, err
	}
	return s.offerReply(ctx, c, updated)
}

// acceptOffer is what accepting MEANS, in one place.
//
// The organization becomes an owner, and the gathering's individual owners
// join the organization's roster, because they now hold their ownership
// through it and an organization whose roster does not list them would show
// a gathering nobody there can account for.
//
// They arrive as members, not owners: being trusted with one gathering is
// not being trusted with the organization. And the gathering's MEMBERS are
// untouched — people join gatherings and attend events, and nobody is
// enrolled into an organization by a decision two other people made.
func (s *GatheringService) acceptOffer(ctx context.Context, offer *store.GatheringOffer) error {
	gathering, err := s.Store.GatheringByID(ctx, offer.GatheringID)
	if err != nil {
		return err
	}
	owners := gathering.Owners

	if err := s.Store.AddGatheringOwner(ctx, offer.GatheringID, store.OwnerRef{
		Kind: store.OwnerKindOrganization,
		ID:   offer.OrganizationID,
	}); err != nil {
		return err
	}

	for _, owner := range owners {
		if owner.Kind != store.OwnerKindUser {
			continue
		}
		// Already on the roster in some role: leave it alone. Writing
		// "member" over an owner would demote somebody as a side effect of
		// accepting a gathering.
		_, onRoster, err := s.Store.OrganizationRoleFor(ctx, offer.OrganizationID, owner.ID)
		if err != nil {
			return err
		}
		if onRoster {
			continue
		}
		if err := s.Store.SetOrganizationMember(ctx, offer.OrganizationID, owner.ID, store.OrganizationRoleMember); err != nil {
			return err
		}
	}
	return nil
}

func (s *GatheringService) WithdrawGatheringOffer(ctx context.Context, req csil.WithdrawGatheringOfferRequest) (csil.Empty, error) {
	c, err := authenticated(ctx, "withdraw an offer")
	if err != nil {
		return csil.Empty{}, err
	}
	offer, err := s.Store.GatheringOfferByID(ctx, string(req.OfferId))
	if errors.Is(err, store.ErrNotFound) {
		return csil.Empty{}, NotFound("offer", "no offer with that id")
	}
	if err != nil {
		return csil.Empty{}, err
	}

	access, err := s.Store.GatheringAccessFor(ctx, offer.GatheringID, c.ID)
	if err != nil {
		return csil.Empty{}, err
	}
	if !access.IsOwner {
		return csil.Empty{}, Forbidden("only an owner of the offered gathering can withdraw this")
	}

	resolved, err := s.Store.ResolveGatheringOffer(ctx, offer.ID, store.OfferWithdrawn, time.Now())
	if err != nil {
		return csil.Empty{}, err
	}
	if !resolved {
		return csil.Empty{}, Invalid("offer_id", "this offer has already been answered")
	}
	return csil.Empty{}, nil
}

func (s *GatheringService) AdoptGathering(ctx context.Context, req csil.AdoptGatheringRequest) (csil.Gathering, error) {
	c, err := authenticated(ctx, "adopt a gathering")
	if err != nil {
		return csil.Gathering{}, err
	}
	if !c.IsAdmin {
		return csil.Gathering{}, Forbidden("only an administrator can put a gathering under an organization without an offer")
	}
	if _, _, err := s.load(ctx, c, string(req.GatheringId)); err != nil {
		return csil.Gathering{}, err
	}
	if _, err := s.Store.OrganizationByID(ctx, string(req.OrganizationId)); errors.Is(err, store.ErrNotFound) {
		return csil.Gathering{}, NotFound("organization", "no organization with that id")
	} else if err != nil {
		return csil.Gathering{}, err
	}

	if err := s.Store.AddGatheringOwner(ctx, string(req.GatheringId), store.OwnerRef{
		Kind: store.OwnerKindOrganization,
		ID:   string(req.OrganizationId),
	}); err != nil {
		return csil.Gathering{}, err
	}
	return s.read(ctx, c, string(req.GatheringId))
}
