package csilservices

import (
	"context"
	"errors"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/store"
	"github.com/catalystcommunity/tinku/api/internal/webhooks"
)

// GatheringService implements csil.GatheringService: a gathering is what
// people join and what every event hangs off.
//
// Its one structural difference from an organization is that ownership is
// many-to-many over BOTH individuals and organizations, and reaches a person
// through an organization they own. store.GatheringAccessFor answers that whole
// question in one query; nothing in this file re-derives it.
type GatheringService struct {
	Store        store.Store
	OriginDomain string
	// Notify queues webhook deliveries for what this service changes.
	// Nil in a test that does not care, which is why every call goes
	// through Dispatch rather than touching the field.
	Notify *webhooks.Dispatcher
}

var _ csil.GatheringService = (*GatheringService)(nil)

func (s *GatheringService) viewerFor(ctx context.Context, c caller, gatheringID string) (csil.ViewerContext, error) {
	access, err := s.Store.GatheringAccessFor(ctx, gatheringID, c.ID)
	if err != nil {
		return csil.ViewerContext{}, err
	}
	return gatheringViewer(c, access), nil
}

func (s *GatheringService) ListGatherings(ctx context.Context, req csil.ListGatheringsRequest) (csil.GatheringList, error) {
	c := callerOf(ctx)
	f := store.GatheringFilter{Page: pageOf(req.Page)}
	if req.Mine != nil && *req.Mine {
		if c.ID == "" {
			return csil.GatheringList{Gatherings: []csil.Gathering{}}, nil
		}
		f.MemberID = c.ID
	}
	if req.OwnedByOrganization != nil {
		f.OwnedByOrganization = string(*req.OwnedByOrganization)
	}

	gatherings, total, err := s.Store.ListGatherings(ctx, f)
	if err != nil {
		return csil.GatheringList{}, err
	}
	out := make([]csil.Gathering, 0, len(gatherings))
	for i := range gatherings {
		viewer, err := s.viewerFor(ctx, c, gatherings[i].ID)
		if err != nil {
			return csil.GatheringList{}, err
		}
		publish, err := publishDecisionFor(ctx, s.Store, &gatherings[i])
		if err != nil {
			return csil.GatheringList{}, err
		}
		out = append(out, toGathering(&gatherings[i], viewer, publish, s.OriginDomain))
	}
	return csil.GatheringList{Gatherings: out, Total: uint64(total)}, nil
}

func (s *GatheringService) GetGathering(ctx context.Context, req csil.GetGatheringRequest) (csil.Gathering, error) {
	return s.read(ctx, callerOf(ctx), string(req.Id))
}

// read is the shared tail of every op that answers with a gathering: fetch,
// resolve the viewer, convert. The mutating ops end here so that what they
// return is read back through the same path a plain get uses.
func (s *GatheringService) read(ctx context.Context, c caller, id string) (csil.Gathering, error) {
	gathering, err := s.Store.GatheringByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return csil.Gathering{}, NotFound("gathering", "no gathering with that id")
	}
	if err != nil {
		return csil.Gathering{}, err
	}
	viewer, err := s.viewerFor(ctx, c, id)
	if err != nil {
		return csil.Gathering{}, err
	}
	publish, err := publishDecisionFor(ctx, s.Store, gathering)
	if err != nil {
		return csil.Gathering{}, err
	}
	return toGathering(gathering, viewer, publish, s.OriginDomain), nil
}

func (s *GatheringService) CreateGathering(ctx context.Context, req csil.CreateGatheringRequest) (csil.Gathering, error) {
	c, err := authenticated(ctx, "start a gathering")
	if err != nil {
		return csil.Gathering{}, err
	}
	name, blurb, description, err := validateOrganizationText(req.Name, req.Blurb, req.Description)
	if err != nil {
		return csil.Gathering{}, err
	}

	// The first owner is the caller, unless they name an organization — and naming
	// an organization they do not own would hand ownership to a set of people who
	// never agreed to it.
	owner := store.OwnerRef{Kind: store.OwnerKindUser, ID: c.ID}
	if req.Owner != nil {
		owner, err = s.resolveOwner(ctx, c, *req.Owner)
		if err != nil {
			return csil.Gathering{}, err
		}
	}

	in := store.GatheringInput{
		OriginDomain: s.OriginDomain,
		Name:         name,
		Blurb:        blurb,
		Description:  description,
		SearchText:   searchText(name, blurb, description),
	}
	in.Slug = uniqueSlug(name, store.NewID(), func(candidate string) bool {
		return s.slugTaken(ctx, candidate)
	})

	gathering, err := s.Store.CreateGathering(ctx, in, owner)
	if err != nil {
		return csil.Gathering{}, err
	}
	organizations := []string{}
	if owner.Kind == store.OwnerKindOrganization {
		organizations = append(organizations, owner.ID)
	}
	s.notifyGathering(ctx, webhooks.ActionCreated, gathering, organizations)
	return s.read(ctx, c, gathering.ID)
}

// resolveOwner turns a wire OwnerRefInput into a store OwnerRef, refusing
// any reference the caller is not entitled to make.
func (s *GatheringService) resolveOwner(ctx context.Context, c caller, in csil.OwnerRefInput) (store.OwnerRef, error) {
	switch store.OwnerKind(in.Kind) {
	case store.OwnerKindUser:
		if _, err := s.Store.UserByID(ctx, in.Id); errors.Is(err, store.ErrNotFound) {
			return store.OwnerRef{}, NotFound("user", "no user with that id")
		} else if err != nil {
			return store.OwnerRef{}, err
		}
		return store.OwnerRef{Kind: store.OwnerKindUser, ID: in.Id}, nil

	case store.OwnerKindOrganization:
		role, onRoster, err := s.Store.OrganizationRoleFor(ctx, in.Id, c.ID)
		if err != nil {
			return store.OwnerRef{}, err
		}
		if !onRoster || role != store.OrganizationRoleOwner {
			if !c.IsAdmin {
				return store.OwnerRef{}, Forbidden("only an owner of that organization can make it an owner here")
			}
		}
		if _, err := s.Store.OrganizationByID(ctx, in.Id); errors.Is(err, store.ErrNotFound) {
			return store.OwnerRef{}, NotFound("organization", "no organization with that id")
		} else if err != nil {
			return store.OwnerRef{}, err
		}
		return store.OwnerRef{Kind: store.OwnerKindOrganization, ID: in.Id}, nil

	default:
		return store.OwnerRef{}, Invalid("owner", "an owner is a user or an organization")
	}
}

// slugTaken reports whether an address is already in use on this node. See
// OrganizationService.slugTaken for why this is a lookup rather than a search.
func (s *GatheringService) slugTaken(ctx context.Context, slug string) bool {
	_, err := s.Store.GatheringBySlug(ctx, s.OriginDomain, slug)
	return !errors.Is(err, store.ErrNotFound)
}

func (s *GatheringService) UpdateGathering(ctx context.Context, req csil.UpdateGatheringRequest) (csil.Gathering, error) {
	c, err := authenticated(ctx, "change a gathering")
	if err != nil {
		return csil.Gathering{}, err
	}
	gathering, viewer, err := s.load(ctx, c, string(req.Id))
	if err != nil {
		return csil.Gathering{}, err
	}
	if !viewer.CanEdit {
		return csil.Gathering{}, Forbidden("only an owner can change this gathering")
	}

	name, blurb, description := gathering.Name, gathering.Blurb, gathering.Description
	if req.Name != nil {
		name = *req.Name
	}
	if req.Blurb != nil {
		blurb = *req.Blurb
	}
	if req.Description != nil {
		description = *req.Description
	}
	name, blurb, description, err = validateOrganizationText(name, blurb, description)
	if err != nil {
		return csil.Gathering{}, err
	}

	publishEvents := gathering.PublishEvents
	if req.PublishEvents != nil {
		value, ok := publishSettingOf(req.PublishEvents)
		if !ok {
			return csil.Gathering{}, Invalid("publish_events", "a publish setting is unset, in or out")
		}
		// Refuse rather than accept-and-ignore. A caller that sets a value
		// the instance will not honour deserves to be told, not to discover
		// later that nothing changed.
		settings, err := s.Store.InstanceSettings(ctx)
		if err != nil {
			return csil.Gathering{}, err
		}
		if !settings.GatheringOverrideAllowed && value != store.PublishUnset {
			return csil.Gathering{}, Forbidden("this instance does not let a gathering change whether its events are published")
		}
		publishEvents = value
	}

	if _, err := s.Store.UpdateGathering(ctx, gathering.ID, store.GatheringInput{
		Slug:          gathering.Slug,
		OriginDomain:  gathering.OriginDomain,
		Name:          name,
		Blurb:         blurb,
		Description:   description,
		SearchText:    searchText(name, blurb, description),
		PublishEvents: publishEvents,
	}); err != nil {
		return csil.Gathering{}, err
	}
	updated, err := s.Store.GatheringByID(ctx, gathering.ID)
	if err != nil {
		return csil.Gathering{}, err
	}
	s.notifyGathering(ctx, webhooks.ActionUpdated, updated, organizationsOwning(ctx, s.Store, gathering.ID))
	return s.read(ctx, c, gathering.ID)
}

// load fetches a gathering and the caller's standing in it together, since
// every mutating op needs both before it can decide anything.
func (s *GatheringService) load(ctx context.Context, c caller, id string) (*store.Gathering, csil.ViewerContext, error) {
	gathering, err := s.Store.GatheringByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, csil.ViewerContext{}, NotFound("gathering", "no gathering with that id")
	}
	if err != nil {
		return nil, csil.ViewerContext{}, err
	}
	viewer, err := s.viewerFor(ctx, c, id)
	if err != nil {
		return nil, csil.ViewerContext{}, err
	}
	return gathering, viewer, nil
}

// DeleteGathering takes every event under it too. An owner may do this,
// unlike an organization: a gathering is theirs, and nothing outside it points at it
// the way an organization's ownership rows point at an organization.
func (s *GatheringService) DeleteGathering(ctx context.Context, req csil.DeleteGatheringRequest) (csil.Empty, error) {
	c, err := authenticated(ctx, "delete a gathering")
	if err != nil {
		return csil.Empty{}, err
	}
	gathering, viewer, err := s.load(ctx, c, string(req.Id))
	if err != nil {
		return csil.Empty{}, err
	}
	if !viewer.CanDelete {
		return csil.Empty{}, Forbidden("only an owner or an administrator can delete this gathering")
	}
	// The organizations are read BEFORE the delete: afterwards there is no
	// gathering to read them from, and they are who has to be told.
	organizations := organizationsOwning(ctx, s.Store, string(req.Id))
	deleted := *gathering
	if err := s.Store.DeleteGathering(ctx, string(req.Id)); err != nil {
		return csil.Empty{}, err
	}
	// This gathering's own webhooks go with it — they belong to the thing
	// that is gone — but the organizations above it are still listening,
	// and a gathering disappearing is exactly what they want to hear.
	if err := s.Store.DeleteWebhooksFor(ctx, store.WebhookOwnerGathering, string(req.Id)); err != nil {
		return csil.Empty{}, err
	}
	s.notifyGathering(ctx, webhooks.ActionDeleted, &deleted, organizations)
	return csil.Empty{}, nil
}

func (s *GatheringService) ListGatheringMembers(ctx context.Context, req csil.ListGatheringMembersRequest) (csil.GatheringMemberList, error) {
	members, total, err := s.Store.ListGatheringMembers(ctx, string(req.GatheringId), pageOf(req.Page))
	if err != nil {
		return csil.GatheringMemberList{}, err
	}
	out := make([]csil.GatheringMember, 0, len(members))
	for i := range members {
		out = append(out, toGatheringMember(&members[i]))
	}
	return csil.GatheringMemberList{
		GatheringId: req.GatheringId,
		Members:     out,
		Total:       uint64(total),
	}, nil
}

// JoinGathering is open to any authenticated caller. Membership is not a
// privilege a gathering grants; it is what makes marking attendance on that
// gathering's events possible, and gatekeeping it would be gatekeeping
// attendance.
func (s *GatheringService) JoinGathering(ctx context.Context, req csil.JoinGatheringRequest) (csil.Gathering, error) {
	c, err := authenticated(ctx, "join a gathering")
	if err != nil {
		return csil.Gathering{}, err
	}
	if _, _, err := s.load(ctx, c, string(req.GatheringId)); err != nil {
		return csil.Gathering{}, err
	}
	if err := s.Store.JoinGathering(ctx, string(req.GatheringId), c.ID); err != nil {
		return csil.Gathering{}, err
	}
	return s.read(ctx, c, string(req.GatheringId))
}

// LeaveGathering does not withdraw attendance already marked on events that
// have started: those are frozen, and leaving is not a way around the lock.
// Attendance on events still to come is left alone too — a person who
// leaves and rejoins should not silently lose their plans.
func (s *GatheringService) LeaveGathering(ctx context.Context, req csil.LeaveGatheringRequest) (csil.Gathering, error) {
	c, err := authenticated(ctx, "leave a gathering")
	if err != nil {
		return csil.Gathering{}, err
	}
	if _, _, err := s.load(ctx, c, string(req.GatheringId)); err != nil {
		return csil.Gathering{}, err
	}
	if err := s.Store.LeaveGathering(ctx, string(req.GatheringId), c.ID); err != nil {
		return csil.Gathering{}, err
	}
	return s.read(ctx, c, string(req.GatheringId))
}

func (s *GatheringService) AddGatheringOwner(ctx context.Context, req csil.AddGatheringOwnerRequest) (csil.Gathering, error) {
	c, err := authenticated(ctx, "change a gathering's owners")
	if err != nil {
		return csil.Gathering{}, err
	}
	_, viewer, err := s.load(ctx, c, string(req.GatheringId))
	if err != nil {
		return csil.Gathering{}, err
	}
	if !viewer.CanManageMembers {
		return csil.Gathering{}, Forbidden("only an owner can add an owner")
	}
	owner, err := s.resolveOwner(ctx, c, req.Owner)
	if err != nil {
		return csil.Gathering{}, err
	}
	if err := s.Store.AddGatheringOwner(ctx, string(req.GatheringId), owner); err != nil {
		return csil.Gathering{}, err
	}
	return s.read(ctx, c, string(req.GatheringId))
}

func (s *GatheringService) RemoveGatheringOwner(ctx context.Context, req csil.RemoveGatheringOwnerRequest) (csil.Gathering, error) {
	c, err := authenticated(ctx, "change a gathering's owners")
	if err != nil {
		return csil.Gathering{}, err
	}
	_, viewer, err := s.load(ctx, c, string(req.GatheringId))
	if err != nil {
		return csil.Gathering{}, err
	}
	if !viewer.CanManageMembers {
		return csil.Gathering{}, Forbidden("only an owner can remove an owner")
	}
	// A gathering with no owner can never be edited and can never be given
	// one, so the last one does not come off.
	count, err := s.Store.CountGatheringOwners(ctx, string(req.GatheringId))
	if err != nil {
		return csil.Gathering{}, err
	}
	if count <= 1 {
		return csil.Gathering{}, Invalid("owner", "a gathering must keep at least one owner")
	}
	owner := store.OwnerRef{Kind: store.OwnerKind(req.Owner.Kind), ID: req.Owner.Id}
	if owner.Kind != store.OwnerKindUser && owner.Kind != store.OwnerKindOrganization {
		return csil.Gathering{}, Invalid("owner", "an owner is a user or an organization")
	}
	if err := s.Store.RemoveGatheringOwner(ctx, string(req.GatheringId), owner); err != nil {
		return csil.Gathering{}, err
	}
	return s.read(ctx, c, string(req.GatheringId))
}
