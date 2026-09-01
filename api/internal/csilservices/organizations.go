package csilservices

import (
	"context"
	"errors"
	"strings"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/store"
)

// OrganizationService implements csil.OrganizationService: an organization
// is a named set of people that can hold ownership of a gathering.
//
// Reads are anonymous. Writes need a session, and every write except
// creation needs the caller to own the organization — except deletion, which needs
// the global admin role and nothing less. See perms.go for the whole model.
type OrganizationService struct {
	Store store.Store
	// OriginDomain is the domain half of every organization address this node
	// mints.
	OriginDomain string
}

var _ csil.OrganizationService = (*OrganizationService)(nil)

// viewerFor resolves the caller's standing in one organization. It is one query,
// and every op that returns a Organization runs it, so no response is ever built
// with a permission block guessed from context.
func (s *OrganizationService) viewerFor(ctx context.Context, c caller, organizationID string) (csil.ViewerContext, error) {
	if c.ID == "" {
		return organizationViewer(c, "", false), nil
	}
	role, onRoster, err := s.Store.OrganizationRoleFor(ctx, organizationID, c.ID)
	if err != nil {
		return csil.ViewerContext{}, err
	}
	return organizationViewer(c, role, onRoster), nil
}

func (s *OrganizationService) ListOrganizations(ctx context.Context, req csil.ListOrganizationsRequest) (csil.OrganizationList, error) {
	c := callerOf(ctx)
	f := store.OrganizationFilter{Page: pageOf(req.Page)}
	if req.Mine != nil && *req.Mine {
		// An anonymous caller asking for "mine" gets an empty list rather
		// than an error: they asked a well-formed question whose answer is
		// nothing.
		if c.ID == "" {
			return csil.OrganizationList{Organizations: []csil.Organization{}}, nil
		}
		f.MemberID = c.ID
	}

	organizations, total, err := s.Store.ListOrganizations(ctx, f)
	if err != nil {
		return csil.OrganizationList{}, err
	}
	out := make([]csil.Organization, 0, len(organizations))
	for i := range organizations {
		viewer, err := s.viewerFor(ctx, c, organizations[i].ID)
		if err != nil {
			return csil.OrganizationList{}, err
		}
		out = append(out, toOrganization(&organizations[i], viewer, s.OriginDomain))
	}
	return csil.OrganizationList{Organizations: out, Total: uint64(total)}, nil
}

func (s *OrganizationService) GetOrganization(ctx context.Context, req csil.GetOrganizationRequest) (csil.Organization, error) {
	organization, err := s.Store.OrganizationByID(ctx, string(req.Id))
	if errors.Is(err, store.ErrNotFound) {
		return csil.Organization{}, NotFound("organization", "no organization with that id")
	}
	if err != nil {
		return csil.Organization{}, err
	}
	viewer, err := s.viewerFor(ctx, callerOf(ctx), organization.ID)
	if err != nil {
		return csil.Organization{}, err
	}
	return toOrganization(organization, viewer, s.OriginDomain), nil
}

func (s *OrganizationService) CreateOrganization(ctx context.Context, req csil.CreateOrganizationRequest) (csil.Organization, error) {
	c, err := authenticated(ctx, "start an organization")
	if err != nil {
		return csil.Organization{}, err
	}
	name, blurb, description, err := validateOrganizationText(req.Name, req.Blurb, req.Description)
	if err != nil {
		return csil.Organization{}, err
	}

	in := store.OrganizationInput{
		OriginDomain: s.OriginDomain,
		Name:         name,
		Blurb:        blurb,
		Description:  description,
		SearchText:   searchText(name, blurb, description),
	}
	in.Slug = uniqueSlug(name, store.NewID(), func(candidate string) bool {
		return s.slugTaken(ctx, candidate)
	})

	organization, err := s.Store.CreateOrganization(ctx, in, c.ID)
	if err != nil {
		return csil.Organization{}, err
	}
	return toOrganization(organization, organizationViewer(c, store.OrganizationRoleOwner, true), s.OriginDomain), nil
}

// slugTaken reports whether an address is already in use on this node.
//
// A direct lookup, not a text search: two people naming an organization "Board
// Games" is normal, and the two rows must get two addresses. A failed
// lookup falls back to "taken", which sends the caller down the suffixed
// path — a slightly uglier address beats a failed creation.
func (s *OrganizationService) slugTaken(ctx context.Context, slug string) bool {
	_, err := s.Store.OrganizationBySlug(ctx, s.OriginDomain, slug)
	return !errors.Is(err, store.ErrNotFound)
}

func (s *OrganizationService) UpdateOrganization(ctx context.Context, req csil.UpdateOrganizationRequest) (csil.Organization, error) {
	c, err := authenticated(ctx, "change an organization")
	if err != nil {
		return csil.Organization{}, err
	}
	organization, err := s.Store.OrganizationByID(ctx, string(req.Id))
	if errors.Is(err, store.ErrNotFound) {
		return csil.Organization{}, NotFound("organization", "no organization with that id")
	}
	if err != nil {
		return csil.Organization{}, err
	}
	viewer, err := s.viewerFor(ctx, c, organization.ID)
	if err != nil {
		return csil.Organization{}, err
	}
	if !viewer.CanEdit {
		return csil.Organization{}, Forbidden("only an owner can change this organization")
	}

	// Read-modify-write: search_text is derived from all three fields at
	// once, so a partial update still has to know the other two.
	name, blurb, description := organization.Name, organization.Blurb, organization.Description
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
		return csil.Organization{}, err
	}

	publishEvents := organization.PublishEvents
	if req.PublishEvents != nil {
		value, ok := publishSettingOf(req.PublishEvents)
		if !ok {
			return csil.Organization{}, Invalid("publish_events", "a publish setting is unset, in or out")
		}
		settings, err := s.Store.InstanceSettings(ctx)
		if err != nil {
			return csil.Organization{}, err
		}
		if !settings.OrganizationOverrideAllowed && value != store.PublishUnset {
			return csil.Organization{}, Forbidden("this instance does not let an organization change whether its events are published")
		}
		publishEvents = value
	}

	updated, err := s.Store.UpdateOrganization(ctx, organization.ID, store.OrganizationInput{
		Slug:          organization.Slug,
		OriginDomain:  organization.OriginDomain,
		Name:          name,
		Blurb:         blurb,
		Description:   description,
		SearchText:    searchText(name, blurb, description),
		PublishEvents: publishEvents,
	})
	if err != nil {
		return csil.Organization{}, err
	}
	return toOrganization(updated, viewer, s.OriginDomain), nil
}

// DeleteOrganization is admin-only. An organization's ownership rows point
// at gatherings other people run, so destroying one reaches further than the
// organization's own members can see — which is the reason the domain
// reserves it.
func (s *OrganizationService) DeleteOrganization(ctx context.Context, req csil.DeleteOrganizationRequest) (csil.Empty, error) {
	c, err := authenticated(ctx, "delete an organization")
	if err != nil {
		return csil.Empty{}, err
	}
	if !c.IsAdmin {
		return csil.Empty{}, Forbidden("only an administrator can delete an organization")
	}
	if _, err := s.Store.OrganizationByID(ctx, string(req.Id)); errors.Is(err, store.ErrNotFound) {
		return csil.Empty{}, NotFound("organization", "no organization with that id")
	} else if err != nil {
		return csil.Empty{}, err
	}
	if err := s.Store.DeleteOrganization(ctx, string(req.Id)); err != nil {
		return csil.Empty{}, err
	}
	return csil.Empty{}, nil
}

func (s *OrganizationService) ListOrganizationMembers(ctx context.Context, req csil.ListOrganizationMembersRequest) (csil.OrganizationMemberList, error) {
	return s.memberList(ctx, string(req.OrganizationId), pageOf(req.Page))
}

func (s *OrganizationService) memberList(ctx context.Context, organizationID string, page store.Page) (csil.OrganizationMemberList, error) {
	members, total, err := s.Store.ListOrganizationMembers(ctx, organizationID, page)
	if err != nil {
		return csil.OrganizationMemberList{}, err
	}
	out := make([]csil.OrganizationMember, 0, len(members))
	for i := range members {
		out = append(out, toOrganizationMember(&members[i]))
	}
	return csil.OrganizationMemberList{OrganizationId: csil.OrganizationID(organizationID), Members: out, Total: uint64(total)}, nil
}

func (s *OrganizationService) SetOrganizationMember(ctx context.Context, req csil.SetOrganizationMemberRequest) (csil.OrganizationMemberList, error) {
	c, err := authenticated(ctx, "change an organization's roster")
	if err != nil {
		return csil.OrganizationMemberList{}, err
	}
	viewer, err := s.viewerFor(ctx, c, string(req.OrganizationId))
	if err != nil {
		return csil.OrganizationMemberList{}, err
	}
	if !viewer.CanManageMembers {
		return csil.OrganizationMemberList{}, Forbidden("only an owner can change this organization's roster")
	}
	role := store.OrganizationRole(req.Role)
	if role != store.OrganizationRoleOwner && role != store.OrganizationRoleMember {
		return csil.OrganizationMemberList{}, Invalid("role", "an organization role is owner or member")
	}
	if _, err := s.Store.UserByID(ctx, string(req.UserId)); errors.Is(err, store.ErrNotFound) {
		return csil.OrganizationMemberList{}, NotFound("user", "no user with that id")
	} else if err != nil {
		return csil.OrganizationMemberList{}, err
	}

	// Demoting the last owner would leave an organization nobody can edit and
	// nobody can give an owner to. Refused for the same reason removing the
	// last owner is.
	if role == store.OrganizationRoleMember {
		if err := s.refuseLastOwnerChange(ctx, string(req.OrganizationId), string(req.UserId)); err != nil {
			return csil.OrganizationMemberList{}, err
		}
	}
	if err := s.Store.SetOrganizationMember(ctx, string(req.OrganizationId), string(req.UserId), role); err != nil {
		return csil.OrganizationMemberList{}, err
	}
	return s.memberList(ctx, string(req.OrganizationId), store.Page{})
}

func (s *OrganizationService) RemoveOrganizationMember(ctx context.Context, req csil.RemoveOrganizationMemberRequest) (csil.OrganizationMemberList, error) {
	c, err := authenticated(ctx, "change an organization's roster")
	if err != nil {
		return csil.OrganizationMemberList{}, err
	}
	viewer, err := s.viewerFor(ctx, c, string(req.OrganizationId))
	if err != nil {
		return csil.OrganizationMemberList{}, err
	}
	// Anybody may take themselves off a roster. Leaving is not an
	// administrative act, and needing an owner's help to leave an organization is
	// not a thing a person should have to do.
	if !viewer.CanManageMembers && string(req.UserId) != c.ID {
		return csil.OrganizationMemberList{}, Forbidden("only an owner can remove somebody else")
	}
	if err := s.refuseLastOwnerChange(ctx, string(req.OrganizationId), string(req.UserId)); err != nil {
		return csil.OrganizationMemberList{}, err
	}
	if err := s.Store.RemoveOrganizationMember(ctx, string(req.OrganizationId), string(req.UserId)); err != nil {
		return csil.OrganizationMemberList{}, err
	}
	return s.memberList(ctx, string(req.OrganizationId), store.Page{})
}

// refuseLastOwnerChange stops the roster reaching zero owners. An organization with
// no owner cannot be edited and cannot be given an owner, so it would need
// an admin to delete it and nothing else could be done with it.
func (s *OrganizationService) refuseLastOwnerChange(ctx context.Context, organizationID, userID string) error {
	role, onRoster, err := s.Store.OrganizationRoleFor(ctx, organizationID, userID)
	if err != nil {
		return err
	}
	if !onRoster || role != store.OrganizationRoleOwner {
		return nil
	}
	organization, err := s.Store.OrganizationByID(ctx, organizationID)
	if err != nil {
		return err
	}
	if organization.OwnerCount <= 1 {
		return Invalid("user_id", "an organization must keep at least one owner")
	}
	return nil
}

// validateOrganizationText applies the rules the schema cannot: a name that is not
// only whitespace, and the blurb's 300-WORD limit. The character ceilings in
// the schema are re-checked here too, because a server never trusts a client
// to have run the client half of a contract.
func validateOrganizationText(name, blurb, description string) (string, string, string, error) {
	name = strings.TrimSpace(name)
	blurb = strings.TrimSpace(blurb)
	description = strings.TrimSpace(description)

	if name == "" {
		return "", "", "", Invalid("name", "an organization needs a name")
	}
	if len([]rune(name)) > 120 {
		return "", "", "", Invalid("name", "a name is at most 120 characters")
	}
	if n := wordCount(blurb); n > maxBlurbWords {
		return "", "", "", Invalid("blurb", "a blurb is at most 300 words")
	}
	if len([]rune(description)) > 10000 {
		return "", "", "", Invalid("description", "a description is at most 10000 characters")
	}
	return name, blurb, description, nil
}
