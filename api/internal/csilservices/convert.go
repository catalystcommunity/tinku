package csilservices

import (
	"strings"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/store"
)

// This file converts store rows to wire types and back. It holds no policy
// except one, and that one is here because it must not be forgotten: a
// locked event's description and online URL are dropped on the way out (see
// toEvent). Every other decision is made before a value reaches here.

// localOrigin describes a record this instance owns.
//
// `isExternal` is computed by comparing the record's own domain with the
// instance's current one, rather than assumed false. A row keeps the domain
// it was created under, so an instance that is later renamed holds records
// whose domain is no longer its own — and those genuinely are external now.
func originOf(recordDomain, instanceDomain string) csil.Origin {
	return csil.Origin{
		Domain:     recordDomain,
		IsExternal: !strings.EqualFold(recordDomain, instanceDomain),
	}
}

// remoteOrigin describes a record a peer sent. It is always external, and
// it names the peer it arrived from, so a screen can say not merely "not
// yours" but "theirs".
func remoteOrigin(recordDomain, peerAddress string) csil.Origin {
	address := peerAddress
	return csil.Origin{Domain: recordDomain, IsExternal: true, PeerAddress: &address}
}

func pageOf(p *csil.Page) store.Page {
	if p == nil {
		return store.Page{}
	}
	var page store.Page
	if p.Limit != nil {
		page.Limit = int64(*p.Limit)
	}
	if p.Offset != nil {
		page.Offset = int64(*p.Offset)
	}
	return page
}

func toOrganization(g *store.Organization, viewer csil.ViewerContext, instanceDomain string) csil.Organization {
	return csil.Organization{
		Id:            csil.OrganizationID(g.ID),
		Slug:          g.Slug,
		OriginDomain:  g.OriginDomain,
		Name:          g.Name,
		Blurb:         g.Blurb,
		Description:   g.Description,
		PublishEvents: wirePublishSetting(g.PublishEvents),
		MemberCount:   uint64(g.MemberCount),
		OwnerCount:    uint64(g.OwnerCount),
		Origin:        originOf(g.OriginDomain, instanceDomain),
		Viewer:        viewer,
		CreatedAt:     g.CreatedAt,
		UpdatedAt:     g.UpdatedAt,
	}
}

func toOrganizationMember(m *store.OrganizationMember) csil.OrganizationMember {
	return csil.OrganizationMember{
		UserId:         csil.UserID(m.UserID),
		Handle:         m.Handle,
		LinkkeysDomain: m.LinkkeysDomain,
		DisplayName:    m.DisplayName,
		Role:           csil.OrganizationRole(m.Role),
		JoinedAt:       m.JoinedAt,
	}
}

func toOwnerRef(o store.OwnerRef) csil.OwnerRef {
	return csil.OwnerRef{
		Kind:         csil.OwnerKind(o.Kind),
		Id:           o.ID,
		DisplayName:  o.DisplayName,
		Handle:       o.Handle,
		OriginDomain: o.OriginDomain,
	}
}

func toGathering(g *store.Gathering, viewer csil.ViewerContext, publish csil.PublishDecision, instanceDomain string) csil.Gathering {
	owners := make([]csil.OwnerRef, 0, len(g.Owners))
	for _, o := range g.Owners {
		owners = append(owners, toOwnerRef(o))
	}
	return csil.Gathering{
		Id:            csil.GatheringID(g.ID),
		Slug:          g.Slug,
		OriginDomain:  g.OriginDomain,
		Name:          g.Name,
		Blurb:         g.Blurb,
		Description:   g.Description,
		Owners:        owners,
		PublishEvents: wirePublishSetting(g.PublishEvents),
		Publish:       publish,
		MemberCount:   uint64(g.MemberCount),
		EventCount:    uint64(g.EventCount),
		NextEventAt:   g.NextEventAt,
		Origin:        originOf(g.OriginDomain, instanceDomain),
		Viewer:        viewer,
		CreatedAt:     g.CreatedAt,
		UpdatedAt:     g.UpdatedAt,
	}
}

func toGatheringMember(m *store.GatheringMember) csil.GatheringMember {
	return csil.GatheringMember{
		UserId:         csil.UserID(m.UserID),
		Handle:         m.Handle,
		LinkkeysDomain: m.LinkkeysDomain,
		DisplayName:    m.DisplayName,
		JoinedAt:       m.JoinedAt,
	}
}

// toLocation returns nil for a location nothing was said about, so a
// caller can tell "no location" from "a location with empty fields".
func toLocation(l store.Location) *csil.Location {
	if l.IsZero() {
		return nil
	}
	return &csil.Location{
		Name:       l.Name,
		Address:    l.Address,
		Locality:   l.Locality,
		Region:     l.Region,
		PostalCode: l.PostalCode,
		Country:    l.Country,
		Latitude:   l.Latitude,
		Longitude:  l.Longitude,
	}
}

func fromLocation(l *csil.Location) store.Location {
	if l == nil {
		return store.Location{}
	}
	return store.Location{
		Name:       l.Name,
		Address:    l.Address,
		Locality:   l.Locality,
		Region:     l.Region,
		PostalCode: l.PostalCode,
		Country:    l.Country,
		Latitude:   l.Latitude,
		Longitude:  l.Longitude,
	}
}

func toRecurrence(r store.Recurrence) csil.RecurrenceRule {
	out := csil.RecurrenceRule{
		Freq:       csil.RecurrenceFreq(r.Freq),
		Interval:   uint64(r.Interval),
		Ordinal:    r.Ordinal,
		DayOfMonth: nil,
	}
	if r.Weekday != nil {
		w := csil.Weekday(*r.Weekday)
		out.Weekday = &w
	}
	if r.DayOfMonth != nil {
		d := uint64(*r.DayOfMonth)
		out.DayOfMonth = &d
	}
	return out
}

func fromRecurrence(r csil.RecurrenceRule) store.Recurrence {
	out := store.Recurrence{
		Freq:     store.RecurrenceFreq(r.Freq),
		Interval: int64(r.Interval),
		Ordinal:  r.Ordinal,
	}
	if out.Interval < 1 {
		out.Interval = 1
	}
	if r.Weekday != nil {
		w := store.Weekday(*r.Weekday)
		out.Weekday = &w
	}
	if r.DayOfMonth != nil {
		d := int64(*r.DayOfMonth)
		out.DayOfMonth = &d
	}
	return out
}

// toEvent applies the start-time lock on the way out. A locked event's
// description and online URL are simply not sent — "absent" and "empty"
// read the same to a client, which is what lets the rule be implemented by
// withholding rather than by a second shape.
//
// The lock applies to every caller. There is no admin exemption: the domain
// says the description is unavailable once the event starts, and an
// exemption for the one role that can delete the event would not make it
// more available to anybody who wanted to read it.
func toEvent(e *store.Event, viewer csil.ViewerContext, now time.Time, instanceDomain string) csil.Event {
	locked := lockedAt(e.StartsAt, now)
	out := csil.Event{
		Id:              csil.EventID(e.ID),
		GatheringId:     csil.GatheringID(e.GatheringID),
		Title:           e.Title,
		IsOnline:        e.IsOnline,
		IsInPerson:      e.IsInPerson,
		Location:        toLocation(e.Location),
		StartsAt:        e.StartsAt,
		EndsAt:          e.EndsAt,
		Timezone:        e.Timezone,
		Locked:          locked,
		AttendeeCount:   uint64(e.AttendeeCount),
		ViewerAttending: e.ViewerAttending,
		// Always local: a foreign event is a RemoteEvent and never an
		// Event. The block is filled anyway so a client rendering a mixed
		// list reads one shape rather than two.
		Origin:    csil.Origin{Domain: instanceDomain, IsExternal: false},
		Viewer:    viewer,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
	if e.SeriesID != nil {
		id := csil.EventSeriesID(*e.SeriesID)
		out.SeriesId = &id
	}
	if !locked {
		description := e.Description
		out.Description = &description
		if e.OnlineURL != "" {
			url := e.OnlineURL
			out.OnlineUrl = &url
		}
	}
	return out
}

func toEventSeries(s *store.EventSeries, viewer csil.ViewerContext, instanceDomain string) csil.EventSeries {
	out := csil.EventSeries{
		Id:               csil.EventSeriesID(s.ID),
		GatheringId:      csil.GatheringID(s.GatheringID),
		Title:            s.Title,
		Description:      s.Description,
		IsOnline:         s.IsOnline,
		IsInPerson:       s.IsInPerson,
		Location:         toLocation(s.Location),
		Recurrence:       toRecurrence(s.Recurrence),
		StartsOn:         s.StartsOn,
		EndsOn:           s.EndsOn,
		StartTime:        s.StartTime,
		DurationMinutes:  uint64(s.DurationMinutes),
		Timezone:         s.Timezone,
		OccurrenceCount:  uint64(s.OccurrenceCount),
		NextOccurrenceAt: s.NextOccurrenceAt,
		Origin:           csil.Origin{Domain: instanceDomain, IsExternal: false},
		Viewer:           viewer,
		CreatedAt:        s.CreatedAt,
		UpdatedAt:        s.UpdatedAt,
	}
	if s.OnlineURL != "" {
		url := s.OnlineURL
		out.OnlineUrl = &url
	}
	return out
}

func toAttendee(a *store.Attendee) csil.Attendee {
	return csil.Attendee{
		UserId:         csil.UserID(a.UserID),
		Handle:         a.Handle,
		LinkkeysDomain: a.LinkkeysDomain,
		DisplayName:    a.DisplayName,
		MarkedAt:       a.MarkedAt,
	}
}

func toRoleAssignment(a *store.EventRoleAssignment) csil.EventRoleAssignment {
	out := csil.EventRoleAssignment{
		UserId:         csil.UserID(a.UserID),
		Handle:         a.Handle,
		LinkkeysDomain: a.LinkkeysDomain,
		DisplayName:    a.DisplayName,
		Role:           csil.EventRole(a.Role),
		AssignedAt:     a.AssignedAt,
	}
	if a.EventID != nil {
		id := csil.EventID(*a.EventID)
		out.EventId = &id
	}
	if a.SeriesID != nil {
		id := csil.EventSeriesID(*a.SeriesID)
		out.SeriesId = &id
	}
	return out
}

func toUserRef(u *store.User) csil.UserRef {
	return csil.UserRef{
		UserId:         csil.UserID(u.ID),
		Handle:         u.Handle,
		LinkkeysDomain: u.LinkkeysDomain,
		DisplayName:    u.DisplayName,
		IsAdmin:        u.IsAdmin,
	}
}

func toAdminUser(u *store.User) csil.AdminUser {
	admin := csil.AdminUser{
		UserId:         csil.UserID(u.ID),
		Handle:         u.Handle,
		LinkkeysDomain: u.LinkkeysDomain,
		DisplayName:    u.DisplayName,
	}
	if u.AdminGrantedAt != nil {
		admin.GrantedAt = *u.AdminGrantedAt
	}
	return admin
}
