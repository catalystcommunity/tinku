package csilservices

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/federation"
	"github.com/catalystcommunity/tinku/api/internal/store"
)

// EventService implements csil.EventService — events, series, attendance
// and the roles held on either.
//
// The rule that shapes this file more than any other is the start-time
// lock: once an event's starts_at has passed, nothing about it changes, no
// attendance is added or withdrawn, and its description is not sent. Every
// mutating op below checks it, and toEvent enforces the withholding half on
// the way out so no response can leak a started event's description by
// forgetting.
type EventService struct {
	Store store.Store
	// OriginDomain is this instance's own name, which is what decides
	// whether a record's domain is foreign.
	OriginDomain string
	// Publisher queues an event for the peers this instance publishes to.
	// Nil when federation is off, which is the common case — every call
	// site checks, so nothing else has to know whether federation exists.
	Publisher *federation.Publisher
	Now       func() time.Time
}

// publish queues an event, or its tombstone, for every outbound peer.
//
// A failure here is LOGGED, not returned. The event is already written and
// the caller's request succeeded; refusing it afterwards because a queue
// insert failed would report a failure that did not happen. The queue is
// the durable part, and a missed enqueue is a missed delivery, not a lost
// event — which is why the failure is loud in the log rather than silent.
func (s *EventService) publish(ctx context.Context, event *store.Event, deleted bool) {
	if s.Publisher == nil {
		return
	}
	// A started event is not news. Publishing one would put something on a
	// directory that nobody can act on.
	if !deleted && lockedAt(event.StartsAt, s.now()) {
		return
	}

	in := federation.FederatedEventInput{
		EventID:    event.ID,
		Title:      event.Title,
		IsOnline:   event.IsOnline,
		IsInPerson: event.IsInPerson,
		Location:   event.Location,
		StartsAt:   event.StartsAt,
		EndsAt:     event.EndsAt,
		Timezone:   event.Timezone,
	}
	// The two names give the event context at the far end, where the
	// gathering it belongs to cannot be looked up.
	gathering, err := s.Store.GatheringByID(ctx, event.GatheringID)
	if err != nil {
		log.WithError(err).WithField("event", event.ID).
			Error("federation: could not read the gathering to decide whether to publish")
		return
	}
	// The three-level choice: instance, organization, gathering. Checked on
	// every publish rather than cached, so switching an organization to
	// `out` stops the next event rather than the next restart.
	//
	// A TOMBSTONE ignores the decision. If an event was published and the
	// setting then changed to `out`, the deletion still has to travel — the
	// alternative is leaving it on a peer's site permanently.
	if !deleted {
		decision, err := publishDecisionFor(ctx, s.Store, gathering)
		if err != nil {
			log.WithError(err).WithField("event", event.ID).
				Error("federation: could not resolve whether to publish")
			return
		}
		if !decision.Publishing {
			return
		}
	}
	in.GatheringName = gathering.Name
	for _, owner := range gathering.Owners {
		if owner.Kind == store.OwnerKindOrganization {
			in.OrganizationName = owner.DisplayName
			break
		}
	}

	if err := s.Publisher.PublishEvent(ctx, in, deleted); err != nil {
		log.WithError(err).WithField("event", event.ID).
			Error("federation: could not queue an event for delivery")
	}
}

var _ csil.EventService = (*EventService)(nil)

func (s *EventService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// ---- Reading events -------------------------------------------------------

func (s *EventService) GetEvent(ctx context.Context, req csil.GetEventRequest) (csil.Event, error) {
	c := callerOf(ctx)
	event, err := s.loadEvent(ctx, string(req.Id))
	if err != nil {
		return csil.Event{}, err
	}
	return s.present(ctx, c, event)
}

// present resolves the caller's permissions on one event and converts it.
// Every op that answers with an event ends here, so the lock and the
// permission block are applied in exactly one place.
func (s *EventService) present(ctx context.Context, c caller, event *store.Event) (csil.Event, error) {
	viewer, err := s.eventViewerFor(ctx, c, event)
	if err != nil {
		return csil.Event{}, err
	}
	if c.ID != "" {
		attending, err := s.Store.AttendanceFor(ctx, c.ID, []string{event.ID})
		if err != nil {
			return csil.Event{}, err
		}
		event.ViewerAttending = attending[event.ID]
	}
	return toEvent(event, viewer, s.now(), s.OriginDomain), nil
}

func (s *EventService) eventViewerFor(ctx context.Context, c caller, event *store.Event) (csil.ViewerContext, error) {
	access, err := s.Store.GatheringAccessFor(ctx, event.GatheringID, c.ID)
	if err != nil {
		return csil.ViewerContext{}, err
	}
	organizer, presenter, err := s.Store.EventRolesFor(ctx, event.ID, c.ID)
	if err != nil {
		return csil.ViewerContext{}, err
	}
	return eventViewer(c, access, organizer, presenter, lockedAt(event.StartsAt, s.now())), nil
}

func (s *EventService) loadEvent(ctx context.Context, id string) (*store.Event, error) {
	event, err := s.Store.EventByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, NotFound("event", "no event with that id")
	}
	if err != nil {
		return nil, err
	}
	return event, nil
}

func (s *EventService) ListEvents(ctx context.Context, req csil.ListEventsRequest) (csil.EventList, error) {
	c := callerOf(ctx)
	f := store.EventFilter{Now: s.now(), Page: pageOf(req.Page)}
	if req.GatheringId != nil {
		f.GatheringID = string(*req.GatheringId)
	}
	if req.StartsAfter != nil {
		f.StartsAfter = *req.StartsAfter
	}
	if req.StartsBefore != nil {
		f.StartsBefore = *req.StartsBefore
	}
	if req.IncludeStarted != nil {
		f.IncludeStarted = *req.IncludeStarted
	}
	if req.AttendingOnly != nil && *req.AttendingOnly {
		if c.ID == "" {
			return csil.EventList{Events: []csil.Event{}}, nil
		}
		f.AttendeeID = c.ID
	}

	events, total, err := s.Store.ListEvents(ctx, f)
	if err != nil {
		return csil.EventList{}, err
	}
	out, err := s.presentAll(ctx, c, events)
	if err != nil {
		return csil.EventList{}, err
	}
	return csil.EventList{Events: out, Total: uint64(total)}, nil
}

// presentAll converts a whole page. Attendance is resolved for the page in
// one query rather than once per row; the permission block still costs two
// queries per event, which is the honest price of a per-row answer and the
// first thing to batch if a listing ever gets long.
func (s *EventService) presentAll(ctx context.Context, c caller, events []store.Event) ([]csil.Event, error) {
	ids := make([]string, 0, len(events))
	for i := range events {
		ids = append(ids, events[i].ID)
	}
	attending, err := s.Store.AttendanceFor(ctx, c.ID, ids)
	if err != nil {
		return nil, err
	}
	out := make([]csil.Event, 0, len(events))
	for i := range events {
		events[i].ViewerAttending = attending[events[i].ID]
		viewer, err := s.eventViewerFor(ctx, c, &events[i])
		if err != nil {
			return nil, err
		}
		out = append(out, toEvent(&events[i], viewer, s.now(), s.OriginDomain))
	}
	return out, nil
}

// ---- Writing events -------------------------------------------------------

// CreateEvent is a gathering owner's power. Organizer is a role held on an
// event that already exists, so it cannot be what authorizes making one;
// owners appoint organizers afterwards.
func (s *EventService) CreateEvent(ctx context.Context, req csil.CreateEventRequest) (csil.Event, error) {
	c, err := authenticated(ctx, "schedule an event")
	if err != nil {
		return csil.Event{}, err
	}
	access, err := s.Store.GatheringAccessFor(ctx, string(req.GatheringId), c.ID)
	if err != nil {
		return csil.Event{}, err
	}
	if _, err := s.Store.GatheringByID(ctx, string(req.GatheringId)); errors.Is(err, store.ErrNotFound) {
		return csil.Event{}, NotFound("gathering", "no gathering with that id")
	} else if err != nil {
		return csil.Event{}, err
	}
	if !access.IsOwner {
		return csil.Event{}, Forbidden("only an owner of this gathering can schedule an event")
	}

	in := store.EventInput{
		GatheringID: string(req.GatheringId),
		Title:       strings.TrimSpace(req.Title),
		Description: strings.TrimSpace(req.Description),
		IsOnline:    req.IsOnline,
		IsInPerson:  req.IsInPerson,
		Location:    fromLocation(req.Location),
		StartsAt:    req.StartsAt.UTC(),
		EndsAt:      req.EndsAt.UTC(),
		Timezone:    req.Timezone,
	}
	if req.OnlineUrl != nil {
		in.OnlineURL = strings.TrimSpace(*req.OnlineUrl)
	}
	if err := validateEvent(&in); err != nil {
		return csil.Event{}, err
	}
	// A new event in the past would be born locked — uneditable and
	// unattendable from the moment it exists. Refused as a mistake rather
	// than stored as one.
	if lockedAt(in.StartsAt, s.now()) {
		return csil.Event{}, Invalid("starts_at", "an event cannot be scheduled to start in the past")
	}

	event, err := s.Store.CreateEvent(ctx, in)
	if err != nil {
		return csil.Event{}, err
	}
	s.publish(ctx, event, false)
	return s.present(ctx, c, event)
}

func (s *EventService) UpdateEvent(ctx context.Context, req csil.UpdateEventRequest) (csil.Event, error) {
	c, err := authenticated(ctx, "change an event")
	if err != nil {
		return csil.Event{}, err
	}
	event, err := s.loadEvent(ctx, string(req.Id))
	if err != nil {
		return csil.Event{}, err
	}
	viewer, err := s.eventViewerFor(ctx, c, event)
	if err != nil {
		return csil.Event{}, err
	}
	if lockedAt(event.StartsAt, s.now()) {
		return csil.Event{}, Forbidden("this event has started and can no longer be changed")
	}
	if !viewer.CanEdit {
		return csil.Event{}, Forbidden("only an owner or an organizer can change this event")
	}

	in := store.EventInput{
		GatheringID: event.GatheringID,
		SeriesID:    event.SeriesID,
		Title:       event.Title,
		Description: event.Description,
		IsOnline:    event.IsOnline,
		IsInPerson:  event.IsInPerson,
		OnlineURL:   event.OnlineURL,
		Location:    event.Location,
		StartsAt:    event.StartsAt,
		EndsAt:      event.EndsAt,
		Timezone:    event.Timezone,
	}
	if req.Title != nil {
		in.Title = strings.TrimSpace(*req.Title)
	}
	if req.Description != nil {
		in.Description = strings.TrimSpace(*req.Description)
	}
	if req.IsOnline != nil {
		in.IsOnline = *req.IsOnline
	}
	if req.IsInPerson != nil {
		in.IsInPerson = *req.IsInPerson
	}
	if req.OnlineUrl != nil {
		in.OnlineURL = strings.TrimSpace(*req.OnlineUrl)
	}
	if req.Location != nil {
		in.Location = fromLocation(req.Location)
	}
	if req.StartsAt != nil {
		in.StartsAt = req.StartsAt.UTC()
	}
	if req.EndsAt != nil {
		in.EndsAt = req.EndsAt.UTC()
	}
	if req.Timezone != nil {
		in.Timezone = *req.Timezone
	}
	if err := validateEvent(&in); err != nil {
		return csil.Event{}, err
	}
	// Moving an event into the past would lock it on the way out of this
	// call, which is a way of deleting it that nobody asked for.
	if lockedAt(in.StartsAt, s.now()) {
		return csil.Event{}, Invalid("starts_at", "an event cannot be moved to a time that has passed")
	}

	updated, err := s.Store.UpdateEvent(ctx, event.ID, in)
	if err != nil {
		return csil.Event{}, err
	}
	s.publish(ctx, updated, false)
	return s.present(ctx, c, updated)
}

// DeleteEvent is where the admin role earns its existence. Before an event
// starts, its gathering's owners and its own organizers may remove it.
// After it starts it is history, and only an admin removes history.
func (s *EventService) DeleteEvent(ctx context.Context, req csil.DeleteEventRequest) (csil.Empty, error) {
	c, err := authenticated(ctx, "delete an event")
	if err != nil {
		return csil.Empty{}, err
	}
	event, err := s.loadEvent(ctx, string(req.Id))
	if err != nil {
		return csil.Empty{}, err
	}
	viewer, err := s.eventViewerFor(ctx, c, event)
	if err != nil {
		return csil.Empty{}, err
	}
	if !viewer.CanDelete {
		if lockedAt(event.StartsAt, s.now()) {
			return csil.Empty{}, Forbidden("this event has started; only an administrator can delete it")
		}
		return csil.Empty{}, Forbidden("only an owner or an organizer can delete this event")
	}
	if err := s.Store.DeleteEvent(ctx, event.ID); err != nil {
		return csil.Empty{}, err
	}
	// The tombstone goes out AFTER the row is gone, so a peer never holds
	// an event this instance no longer has.
	s.publish(ctx, event, true)
	return csil.Empty{}, nil
}

// ---- Attendance -----------------------------------------------------------

// AttendEvent needs membership of the event's gathering. A person joins a
// gathering, then answers its events one at a time — which is why there is
// no way to attend a series: committing in advance to every future instance
// of an open-ended rule is not something anybody can honestly do.
func (s *EventService) AttendEvent(ctx context.Context, req csil.AttendEventRequest) (csil.Event, error) {
	c, err := authenticated(ctx, "mark attendance")
	if err != nil {
		return csil.Event{}, err
	}
	event, err := s.loadEvent(ctx, string(req.EventId))
	if err != nil {
		return csil.Event{}, err
	}
	if lockedAt(event.StartsAt, s.now()) {
		return csil.Event{}, Forbidden("this event has started; attendance can no longer change")
	}
	access, err := s.Store.GatheringAccessFor(ctx, event.GatheringID, c.ID)
	if err != nil {
		return csil.Event{}, err
	}
	if !access.IsMember && !access.IsOwner {
		return csil.Event{}, Forbidden("join this gathering before marking attendance on its events")
	}
	if err := s.Store.AttendEvent(ctx, event.ID, c.ID); err != nil {
		return csil.Event{}, err
	}
	return s.reread(ctx, c, event.ID)
}

func (s *EventService) UnattendEvent(ctx context.Context, req csil.UnattendEventRequest) (csil.Event, error) {
	c, err := authenticated(ctx, "withdraw attendance")
	if err != nil {
		return csil.Event{}, err
	}
	event, err := s.loadEvent(ctx, string(req.EventId))
	if err != nil {
		return csil.Event{}, err
	}
	if lockedAt(event.StartsAt, s.now()) {
		return csil.Event{}, Forbidden("this event has started; attendance can no longer change")
	}
	if err := s.Store.UnattendEvent(ctx, event.ID, c.ID); err != nil {
		return csil.Event{}, err
	}
	return s.reread(ctx, c, event.ID)
}

// reread fetches the row again so a mutating op answers with the same
// values a plain get would, counts included.
func (s *EventService) reread(ctx context.Context, c caller, id string) (csil.Event, error) {
	event, err := s.loadEvent(ctx, id)
	if err != nil {
		return csil.Event{}, err
	}
	return s.present(ctx, c, event)
}

func (s *EventService) ListAttendees(ctx context.Context, req csil.ListAttendeesRequest) (csil.AttendeeList, error) {
	attendees, total, err := s.Store.ListAttendees(ctx, string(req.EventId), pageOf(req.Page))
	if err != nil {
		return csil.AttendeeList{}, err
	}
	out := make([]csil.Attendee, 0, len(attendees))
	for i := range attendees {
		out = append(out, toAttendee(&attendees[i]))
	}
	return csil.AttendeeList{EventId: req.EventId, Attendees: out, Total: uint64(total)}, nil
}

// ---- Validation -----------------------------------------------------------

// validateEvent applies what the schema cannot state: the online/in-person
// rule, the location rule that follows from it, and the ordering of the two
// instants.
func validateEvent(in *store.EventInput) error {
	if in.Title == "" {
		return Invalid("title", "an event needs a title")
	}
	if len([]rune(in.Title)) > 200 {
		return Invalid("title", "a title is at most 200 characters")
	}
	if len([]rune(in.Description)) > 10000 {
		return Invalid("description", "a description is at most 10000 characters")
	}
	if !in.IsOnline && !in.IsInPerson {
		return Invalid("is_online", "an event is online, in person, or both")
	}
	// In person means somewhere. An in-person event with no location is a
	// listing nobody can act on.
	if in.IsInPerson && in.Location.IsZero() {
		return Invalid("location", "an in-person event needs a location")
	}
	// The online URL is rendered as a link on a page this instance serves,
	// so the scheme is checked here rather than trusted. A browser runs
	// `javascript:` out of an href against THIS origin, with the reader's
	// session cookie — an owner typing one would be an XSS on their own
	// members. See isWebURL.
	if in.OnlineURL != "" && !isWebURL(in.OnlineURL) {
		return Invalid("online_url", "an online link is an http or https URL")
	}
	if in.EndsAt.Before(in.StartsAt) {
		return Invalid("ends_at", "an event cannot end before it starts")
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return Invalid("timezone", "unknown timezone: "+in.Timezone)
	}
	in.SearchText = searchText(append([]string{in.Title, in.Description}, locationSearchParts(in.Location)...)...)
	return nil
}

// validateRecurrence checks the rule can actually produce occurrences. A
// rule that never yields anything is a silent failure otherwise: the series
// stores fine and simply has no events, and nobody can tell why.
func validateRecurrence(r store.Recurrence) error {
	switch r.Freq {
	case store.FreqWeekly:
		if r.Weekday == nil {
			return Invalid("recurrence", "a weekly rule needs a weekday")
		}
	case store.FreqMonthly, store.FreqQuarterly, store.FreqYearly:
		if r.Weekday == nil && r.DayOfMonth == nil {
			return Invalid("recurrence", "this rule needs a weekday with an ordinal, or a day of the month")
		}
		if r.Weekday != nil && r.Ordinal == nil {
			return Invalid("recurrence", "a weekday rule over months or longer needs an ordinal (1-5, or -1 for last)")
		}
	default:
		return Invalid("recurrence", "a frequency is weekly, monthly, quarterly or yearly")
	}
	if r.Weekday != nil {
		if _, ok := store.TimeWeekday(*r.Weekday); !ok {
			return Invalid("recurrence", fmt.Sprintf("unknown weekday %q", *r.Weekday))
		}
	}
	if r.Ordinal != nil && (*r.Ordinal < -1 || *r.Ordinal == 0 || *r.Ordinal > 5) {
		return Invalid("recurrence", "an ordinal is 1 to 5, or -1 for the last")
	}
	if r.DayOfMonth != nil && (*r.DayOfMonth < 1 || *r.DayOfMonth > 31) {
		return Invalid("recurrence", "a day of the month is 1 to 31")
	}
	if r.Interval < 1 {
		return Invalid("recurrence", "an interval is 1 or more")
	}
	return nil
}
