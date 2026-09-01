package csilservices

import (
	"context"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/reqctx"
	"github.com/catalystcommunity/tinku/api/internal/store"
)

// This file is the whole permission model. Every op consults it and nothing
// else decides who may do what, so a rule that changes changes in one place.
//
// The model, stated once:
//
//   admin      global; the ONLY role that can delete an organization, or an event
//              that has already started. Everything else an admin can do,
//              some owner can also do.
//   owner      of an organization: edits it and its roster.
//              of a gathering: edits it, its owners, and every event under
//              it. Held directly, or through being an owner of an organization that
//              owns the gathering.
//   organizer  of one event or one series: edits that event or series. An
//              organizer of a series organizes each of its occurrences.
//   presenter  of one event or one series: billed as presenting. Carries a
//              member's permissions and nothing more, by design.
//   member     of an organization: on the roster, no authority.
//              of a gathering: may mark attendance on its events.
//
// The other rule that cuts across all of it is the start-time lock: once an
// event's starts_at has passed it is frozen for everyone, and the only power
// that still applies to it is an admin's power to delete it.

// caller is who is asking. An anonymous caller has an empty ID and is not an
// admin, so every check below reads false without a special case.
type caller struct {
	ID      string
	IsAdmin bool
}

// callerOf reads the authenticated user off the request context.
func callerOf(ctx context.Context) caller {
	user, ok := reqctx.User(ctx)
	if !ok {
		return caller{}
	}
	return caller{ID: user.ID, IsAdmin: user.IsAdmin}
}

// authenticated returns the caller, or the unauthenticated failure with the
// message the op wants to give.
func authenticated(ctx context.Context, action string) (caller, error) {
	c := callerOf(ctx)
	if c.ID == "" {
		return c, Unauthenticated("log in to " + action)
	}
	return c, nil
}

// organizationViewer resolves what the caller may do with one organization.
func organizationViewer(c caller, role store.OrganizationRole, onRoster bool) csil.ViewerContext {
	isOwner := onRoster && role == store.OrganizationRoleOwner
	return csil.ViewerContext{
		IsAdmin: c.IsAdmin,
		IsOwner: isOwner,
		// An organization has no organizers and no presenters. The flags stay
		// false rather than being absent, so a client renders one shape.
		IsMember:         onRoster,
		HasJoined:        onRoster,
		CanEdit:          isOwner,
		CanDelete:        c.IsAdmin, // admins alone, per the domain
		CanManageMembers: isOwner,
	}
}

// gatheringViewer resolves what the caller may do with one gathering.
func gatheringViewer(c caller, access store.GatheringAccess) csil.ViewerContext {
	return csil.ViewerContext{
		IsAdmin:  c.IsAdmin,
		IsOwner:  access.IsOwner,
		IsMember: access.IsMember || access.IsOwner,
		// Roster membership alone. An owner who never joined has a
		// member's access and is not on the roster, and a Join/Leave
		// control that cannot tell the two apart never changes.
		HasJoined: access.IsMember,
		CanEdit:   access.IsOwner,
		// Unlike an organization, a gathering can be deleted by its own owners: it
		// is theirs, and nobody else's records point at it the way a
		// organization's ownership rows point at an organization.
		CanDelete:        access.IsOwner || c.IsAdmin,
		CanManageMembers: access.IsOwner,
	}
}

// eventViewer resolves what the caller may do with one event.
//
// `locked` is the start-time rule: a started event is frozen for everyone,
// so it clears every permission except an admin's power to delete history.
func eventViewer(c caller, access store.GatheringAccess, isOrganizer, isPresenter, locked bool) csil.ViewerContext {
	canWrite := (access.IsOwner || isOrganizer) && !locked
	canDelete := access.IsOwner || isOrganizer || c.IsAdmin
	if locked {
		canDelete = c.IsAdmin
	}
	return csil.ViewerContext{
		IsAdmin:          c.IsAdmin,
		IsOwner:          access.IsOwner,
		IsOrganizer:      isOrganizer,
		IsPresenter:      isPresenter,
		IsMember:         access.IsMember || access.IsOwner,
		HasJoined:        access.IsMember,
		CanEdit:          canWrite,
		CanDelete:        canDelete,
		CanManageMembers: access.IsOwner && !locked,
	}
}

// seriesViewer resolves what the caller may do with one series. A series has
// no start-time lock of its own: the lock is a property of a dated event,
// and editing a series only ever rewrites the occurrences that have not
// started.
func seriesViewer(c caller, access store.GatheringAccess, isOrganizer, isPresenter bool) csil.ViewerContext {
	canWrite := access.IsOwner || isOrganizer
	return csil.ViewerContext{
		IsAdmin:          c.IsAdmin,
		IsOwner:          access.IsOwner,
		IsOrganizer:      isOrganizer,
		IsPresenter:      isPresenter,
		IsMember:         access.IsMember || access.IsOwner,
		HasJoined:        access.IsMember,
		CanEdit:          canWrite,
		CanDelete:        canWrite || c.IsAdmin,
		CanManageMembers: access.IsOwner,
	}
}

// locked reports whether an event has started. It is the single definition
// of the rule: once starts_at has passed, nothing about the event can
// change and its description is not shown.
//
// The boundary is inclusive of the start instant — an event "starts" at
// starts_at, so at exactly that moment it is already started.
func lockedAt(startsAt, now time.Time) bool { return !now.Before(startsAt) }
