package csilservices

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/store"
)

// The series half of EventService.
//
// A series is a rule plus a template. Its occurrences are ordinary events
// carrying series_id, materialized out to a bounded horizon and extended as
// the horizon rolls forward — see csil/types/events.csil for why an
// occurrence is an event rather than a second kind of thing.

func (s *EventService) seriesViewerFor(ctx context.Context, c caller, series *store.EventSeries) (csil.ViewerContext, error) {
	access, err := s.Store.GatheringAccessFor(ctx, series.GatheringID, c.ID)
	if err != nil {
		return csil.ViewerContext{}, err
	}
	organizer, presenter, err := s.Store.SeriesRolesFor(ctx, series.ID, c.ID)
	if err != nil {
		return csil.ViewerContext{}, err
	}
	return seriesViewer(c, access, organizer, presenter), nil
}

func (s *EventService) loadSeries(ctx context.Context, id string) (*store.EventSeries, error) {
	series, err := s.Store.EventSeriesByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, NotFound("event_series", "no event series with that id")
	}
	if err != nil {
		return nil, err
	}
	return series, nil
}

func (s *EventService) GetEventSeries(ctx context.Context, req csil.GetEventSeriesRequest) (csil.EventSeries, error) {
	c := callerOf(ctx)
	series, err := s.loadSeries(ctx, string(req.Id))
	if err != nil {
		return csil.EventSeries{}, err
	}
	// Reading a series keeps its horizon rolling. Without this an
	// open-ended series would quietly run out of occurrences a year after
	// it was made and nobody would be told.
	if err := s.ensureHorizon(ctx, series, s.now().Add(defaultHorizon)); err != nil {
		return csil.EventSeries{}, err
	}
	if series, err = s.loadSeries(ctx, series.ID); err != nil {
		return csil.EventSeries{}, err
	}
	viewer, err := s.seriesViewerFor(ctx, c, series)
	if err != nil {
		return csil.EventSeries{}, err
	}
	return toEventSeries(series, viewer, s.OriginDomain), nil
}

func (s *EventService) ListEventSeries(ctx context.Context, req csil.ListEventSeriesRequest) (csil.EventSeriesList, error) {
	c := callerOf(ctx)
	f := store.EventSeriesFilter{Page: pageOf(req.Page)}
	if req.GatheringId != nil {
		f.GatheringID = string(*req.GatheringId)
	}
	series, total, err := s.Store.ListEventSeries(ctx, f)
	if err != nil {
		return csil.EventSeriesList{}, err
	}
	out := make([]csil.EventSeries, 0, len(series))
	for i := range series {
		viewer, err := s.seriesViewerFor(ctx, c, &series[i])
		if err != nil {
			return csil.EventSeriesList{}, err
		}
		out = append(out, toEventSeries(&series[i], viewer, s.OriginDomain))
	}
	return csil.EventSeriesList{Series: out, Total: uint64(total)}, nil
}

// CreateEventSeries is a gathering owner's power, for the same reason
// CreateEvent is: organizer is a role held on something that already exists.
func (s *EventService) CreateEventSeries(ctx context.Context, req csil.CreateEventSeriesRequest) (csil.EventSeries, error) {
	c, err := authenticated(ctx, "schedule a recurring event")
	if err != nil {
		return csil.EventSeries{}, err
	}
	if _, err := s.Store.GatheringByID(ctx, string(req.GatheringId)); errors.Is(err, store.ErrNotFound) {
		return csil.EventSeries{}, NotFound("gathering", "no gathering with that id")
	} else if err != nil {
		return csil.EventSeries{}, err
	}
	access, err := s.Store.GatheringAccessFor(ctx, string(req.GatheringId), c.ID)
	if err != nil {
		return csil.EventSeries{}, err
	}
	if !access.IsOwner {
		return csil.EventSeries{}, Forbidden("only an owner of this gathering can schedule a recurring event")
	}

	in := store.EventSeriesInput{
		GatheringID:     string(req.GatheringId),
		Title:           strings.TrimSpace(req.Title),
		Description:     strings.TrimSpace(req.Description),
		IsOnline:        req.IsOnline,
		IsInPerson:      req.IsInPerson,
		Location:        fromLocation(req.Location),
		Recurrence:      fromRecurrence(req.Recurrence),
		StartsOn:        req.StartsOn.UTC(),
		EndsOn:          utcOrNil(req.EndsOn),
		StartTime:       req.StartTime,
		DurationMinutes: int64(req.DurationMinutes),
		Timezone:        req.Timezone,
	}
	if req.OnlineUrl != nil {
		in.OnlineURL = strings.TrimSpace(*req.OnlineUrl)
	}
	if err := validateSeries(&in); err != nil {
		return csil.EventSeries{}, err
	}
	// Nothing is materialized yet; ensureHorizon below does that and
	// records how far it got.
	in.MaterializedThrough = in.StartsOn

	series, err := s.Store.CreateEventSeries(ctx, in)
	if err != nil {
		return csil.EventSeries{}, err
	}
	if err := s.ensureHorizon(ctx, series, s.now().Add(defaultHorizon)); err != nil {
		return csil.EventSeries{}, err
	}
	return s.rereadSeries(ctx, c, series.ID)
}

// UpdateEventSeries rewrites the occurrences that have not started and
// leaves the ones that have exactly as they are. Being produced by a rule
// does not exempt an event from the start-time lock: a Thursday that has
// happened is history, whoever edits the rule afterwards.
func (s *EventService) UpdateEventSeries(ctx context.Context, req csil.UpdateEventSeriesRequest) (csil.EventSeries, error) {
	c, err := authenticated(ctx, "change a recurring event")
	if err != nil {
		return csil.EventSeries{}, err
	}
	series, err := s.loadSeries(ctx, string(req.Id))
	if err != nil {
		return csil.EventSeries{}, err
	}
	viewer, err := s.seriesViewerFor(ctx, c, series)
	if err != nil {
		return csil.EventSeries{}, err
	}
	if !viewer.CanEdit {
		return csil.EventSeries{}, Forbidden("only an owner or an organizer can change this recurring event")
	}

	in := store.EventSeriesInput{
		GatheringID:     series.GatheringID,
		Title:           series.Title,
		Description:     series.Description,
		IsOnline:        series.IsOnline,
		IsInPerson:      series.IsInPerson,
		OnlineURL:       series.OnlineURL,
		Location:        series.Location,
		Recurrence:      series.Recurrence,
		StartsOn:        series.StartsOn,
		EndsOn:          series.EndsOn,
		StartTime:       series.StartTime,
		DurationMinutes: series.DurationMinutes,
		Timezone:        series.Timezone,
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
	if req.Recurrence != nil {
		in.Recurrence = fromRecurrence(*req.Recurrence)
	}
	if req.StartsOn != nil {
		in.StartsOn = req.StartsOn.UTC()
	}
	if req.EndsOn != nil {
		in.EndsOn = utcOrNil(req.EndsOn)
	}
	if req.StartTime != nil {
		in.StartTime = *req.StartTime
	}
	if req.DurationMinutes != nil {
		in.DurationMinutes = int64(*req.DurationMinutes)
	}
	if req.Timezone != nil {
		in.Timezone = *req.Timezone
	}
	if err := validateSeries(&in); err != nil {
		return csil.EventSeries{}, err
	}
	// The horizon is reset so the rewrite below re-materializes the future
	// under the new rule. `now` is the floor, not the series' start: the
	// past is KEPT rather than rebuilt, and re-materializing from the start
	// under a changed rule would invent occurrences that never happened —
	// moving "every Thursday" to "every Friday" would add a Friday to every
	// week the series has already run.
	now := s.now()
	in.MaterializedThrough = maxTime(in.StartsOn, now)

	if _, err := s.Store.UpdateEventSeries(ctx, series.ID, in); err != nil {
		return csil.EventSeries{}, err
	}
	// Drop the future, keep the past, then rebuild the future from the new
	// rule. `now` is the cut, which is exactly the start-time lock.
	if err := s.Store.DeleteOccurrencesFrom(ctx, series.ID, now); err != nil {
		return csil.EventSeries{}, err
	}
	updated, err := s.loadSeries(ctx, series.ID)
	if err != nil {
		return csil.EventSeries{}, err
	}
	if err := s.ensureHorizon(ctx, updated, now.Add(defaultHorizon)); err != nil {
		return csil.EventSeries{}, err
	}
	return s.rereadSeries(ctx, c, series.ID)
}

// DeleteEventSeries removes the rule and the occurrences that have not
// started. Occurrences that HAVE started survive with a null series_id: they
// are history, and only an admin deletes history — one at a time, through
// DeleteEvent.
func (s *EventService) DeleteEventSeries(ctx context.Context, req csil.DeleteEventSeriesRequest) (csil.Empty, error) {
	c, err := authenticated(ctx, "delete a recurring event")
	if err != nil {
		return csil.Empty{}, err
	}
	series, err := s.loadSeries(ctx, string(req.Id))
	if err != nil {
		return csil.Empty{}, err
	}
	viewer, err := s.seriesViewerFor(ctx, c, series)
	if err != nil {
		return csil.Empty{}, err
	}
	if !viewer.CanDelete {
		return csil.Empty{}, Forbidden("only an owner or an organizer can delete this recurring event")
	}
	if err := s.Store.DeleteOccurrencesFrom(ctx, series.ID, s.now()); err != nil {
		return csil.Empty{}, err
	}
	if err := s.Store.DeleteEventSeries(ctx, series.ID); err != nil {
		return csil.Empty{}, err
	}
	return csil.Empty{}, nil
}

// ExpandEventSeries pushes the horizon out and answers with every
// occurrence the series now has. It needs a session — materializing rows is
// a write, however idempotent — but no particular role: anybody looking at
// a calendar has a legitimate reason to ask how far ahead it goes, and the
// rule already fixes what the answer is.
func (s *EventService) ExpandEventSeries(ctx context.Context, req csil.ExpandEventSeriesRequest) (csil.EventList, error) {
	c, err := authenticated(ctx, "expand a recurring event")
	if err != nil {
		return csil.EventList{}, err
	}
	series, err := s.loadSeries(ctx, string(req.SeriesId))
	if err != nil {
		return csil.EventList{}, err
	}

	through := s.now().Add(defaultHorizon)
	if req.Through != nil {
		through = req.Through.UTC()
	}
	if ceiling := s.now().Add(maxHorizon); through.After(ceiling) {
		through = ceiling
	}
	if err := s.ensureHorizon(ctx, series, through); err != nil {
		return csil.EventList{}, err
	}

	// Read the occurrences a page at a time up to the row bound. The store
	// clamps any single page well below maxOccurrencesPerExpansion, so one
	// query cannot answer this: asking for 500 and being handed 100 would
	// silently drop the rest of the series.
	var mine []store.Event
	for int64(len(mine)) < maxOccurrencesPerExpansion {
		page, _, err := s.Store.ListEvents(ctx, store.EventFilter{
			GatheringID:    series.GatheringID,
			SeriesID:       series.ID,
			Now:            s.now(),
			IncludeStarted: true,
			Page: store.Page{
				Limit:  maxOccurrencesPerExpansion - int64(len(mine)),
				Offset: int64(len(mine)),
			},
		})
		if err != nil {
			return csil.EventList{}, err
		}
		if len(page) == 0 {
			break
		}
		mine = append(mine, page...)
	}
	out, err := s.presentAll(ctx, c, mine)
	if err != nil {
		return csil.EventList{}, err
	}
	// The total is the filtered count, not the store's: this response
	// carries only this series' occurrences, and a larger total would
	// promise rows the caller cannot page to.
	return csil.EventList{Events: out, Total: uint64(len(out))}, nil
}

// ensureHorizon materializes every occurrence the rule produces between
// what is already on disk and `through`, then records the new horizon.
//
// It is safe to call at any time from any path: the partial unique index on
// (series_id, starts_at) makes the insert idempotent, so a concurrent caller
// doing the same work duplicates nothing.
func (s *EventService) ensureHorizon(ctx context.Context, series *store.EventSeries, through time.Time) error {
	through = through.UTC()
	if ceiling := s.now().Add(maxHorizon); through.After(ceiling) {
		through = ceiling
	}
	if !through.After(series.MaterializedThrough) {
		return nil
	}

	times, err := occurrenceTimes(series, series.MaterializedThrough, through)
	if err != nil {
		// A rule that cannot be evaluated is a bug in validation, not
		// something a caller can act on: it stays a transport-level failure.
		return err
	}

	occurrences := make([]store.EventInput, 0, len(times))
	for _, at := range times {
		in := store.EventInput{
			GatheringID: series.GatheringID,
			Title:       series.Title,
			Description: series.Description,
			IsOnline:    series.IsOnline,
			IsInPerson:  series.IsInPerson,
			OnlineURL:   series.OnlineURL,
			Location:    series.Location,
			StartsAt:    at,
			EndsAt:      at.Add(time.Duration(series.DurationMinutes) * time.Minute),
			Timezone:    series.Timezone,
		}
		in.SearchText = searchText(
			append([]string{in.Title, in.Description}, locationSearchParts(in.Location)...)...)
		occurrences = append(occurrences, in)
	}
	if _, err := s.Store.UpsertOccurrences(ctx, series.ID, occurrences); err != nil {
		return err
	}
	return s.Store.SetMaterializedThrough(ctx, series.ID, through)
}

func (s *EventService) rereadSeries(ctx context.Context, c caller, id string) (csil.EventSeries, error) {
	series, err := s.loadSeries(ctx, id)
	if err != nil {
		return csil.EventSeries{}, err
	}
	viewer, err := s.seriesViewerFor(ctx, c, series)
	if err != nil {
		return csil.EventSeries{}, err
	}
	return toEventSeries(series, viewer, s.OriginDomain), nil
}

// validateSeries applies the same shape rules an event has, plus the ones
// only a rule can break.
func validateSeries(in *store.EventSeriesInput) error {
	if strings.TrimSpace(in.Title) == "" {
		return Invalid("title", "a recurring event needs a title")
	}
	if len([]rune(in.Title)) > 200 {
		return Invalid("title", "a title is at most 200 characters")
	}
	if len([]rune(in.Description)) > 10000 {
		return Invalid("description", "a description is at most 10000 characters")
	}
	if !in.IsOnline && !in.IsInPerson {
		return Invalid("is_online", "a recurring event is online, in person, or both")
	}
	if in.IsInPerson && in.Location.IsZero() {
		return Invalid("location", "an in-person recurring event needs a location")
	}
	// Every occurrence inherits this URL and renders it as a link, so it is
	// checked here for the same reason validateEvent checks an event's.
	if in.OnlineURL != "" && !isWebURL(in.OnlineURL) {
		return Invalid("online_url", "an online link is an http or https URL")
	}
	if in.DurationMinutes <= 0 {
		return Invalid("duration_minutes", "an occurrence needs a duration")
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return Invalid("timezone", "unknown timezone: "+in.Timezone)
	}
	if _, _, err := parseClock(in.StartTime); err != nil {
		return Invalid("start_time", "a start time looks like 19:00")
	}
	if in.EndsOn != nil && in.EndsOn.Before(in.StartsOn) {
		return Invalid("ends_on", "a series cannot end before it starts")
	}
	if err := validateRecurrence(in.Recurrence); err != nil {
		return err
	}
	in.SearchText = searchText(
		append([]string{in.Title, in.Description}, locationSearchParts(in.Location)...)...)
	return nil
}

func utcOrNil(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	utc := t.UTC()
	return &utc
}

// ---- Roles on events and series -------------------------------------------

// SetEventRole is a gathering owner's power. Organizers and presenters are
// appointed by the people who run the gathering, and an organizer cannot
// appoint more organizers — that would make the role self-propagating.
func (s *EventService) SetEventRole(ctx context.Context, req csil.SetEventRoleRequest) (csil.EventRoleList, error) {
	c, err := authenticated(ctx, "assign a role")
	if err != nil {
		return csil.EventRoleList{}, err
	}
	scope, gatheringID, err := s.resolveScope(ctx, req.EventId, req.SeriesId)
	if err != nil {
		return csil.EventRoleList{}, err
	}
	access, err := s.Store.GatheringAccessFor(ctx, gatheringID, c.ID)
	if err != nil {
		return csil.EventRoleList{}, err
	}
	if !access.IsOwner {
		return csil.EventRoleList{}, Forbidden("only an owner of this gathering can assign a role")
	}
	role := store.EventRole(req.Role)
	if role != store.EventRoleOrganizer && role != store.EventRolePresenter {
		return csil.EventRoleList{}, Invalid("role", "a role is organizer or presenter")
	}
	if _, err := s.Store.UserByID(ctx, string(req.UserId)); errors.Is(err, store.ErrNotFound) {
		return csil.EventRoleList{}, NotFound("user", "no user with that id")
	} else if err != nil {
		return csil.EventRoleList{}, err
	}
	if err := s.Store.SetEventRole(ctx, scope, string(req.UserId), role); err != nil {
		return csil.EventRoleList{}, err
	}
	return s.roleList(ctx, scope)
}

func (s *EventService) RemoveEventRole(ctx context.Context, req csil.RemoveEventRoleRequest) (csil.EventRoleList, error) {
	c, err := authenticated(ctx, "remove a role")
	if err != nil {
		return csil.EventRoleList{}, err
	}
	scope, gatheringID, err := s.resolveScope(ctx, req.EventId, req.SeriesId)
	if err != nil {
		return csil.EventRoleList{}, err
	}
	access, err := s.Store.GatheringAccessFor(ctx, gatheringID, c.ID)
	if err != nil {
		return csil.EventRoleList{}, err
	}
	if !access.IsOwner {
		return csil.EventRoleList{}, Forbidden("only an owner of this gathering can remove a role")
	}
	if err := s.Store.RemoveEventRole(ctx, scope, string(req.UserId), store.EventRole(req.Role)); err != nil {
		return csil.EventRoleList{}, err
	}
	return s.roleList(ctx, scope)
}

func (s *EventService) ListEventRoles(ctx context.Context, req csil.ListEventRolesRequest) (csil.EventRoleList, error) {
	scope, _, err := s.resolveScope(ctx, req.EventId, req.SeriesId)
	if err != nil {
		return csil.EventRoleList{}, err
	}
	return s.roleList(ctx, scope)
}

func (s *EventService) roleList(ctx context.Context, scope store.RoleScope) (csil.EventRoleList, error) {
	assignments, err := s.Store.ListEventRoles(ctx, scope)
	if err != nil {
		return csil.EventRoleList{}, err
	}
	out := make([]csil.EventRoleAssignment, 0, len(assignments))
	for i := range assignments {
		out = append(out, toRoleAssignment(&assignments[i]))
	}
	return csil.EventRoleList{Roles: out}, nil
}

// resolveScope turns the request's two optional ids into a valid scope, and
// returns the gathering the scope belongs to — which is what the permission
// check needs, and what makes an id for a row that does not exist a
// not-found rather than a silently empty answer.
func (s *EventService) resolveScope(ctx context.Context, eventID *csil.EventID, seriesID *csil.EventSeriesID) (store.RoleScope, string, error) {
	switch {
	case eventID != nil && seriesID != nil:
		return store.RoleScope{}, "", Invalid("event_id", "name an event or a series, not both")
	case eventID != nil:
		event, err := s.loadEvent(ctx, string(*eventID))
		if err != nil {
			return store.RoleScope{}, "", err
		}
		return store.RoleScope{EventID: event.ID}, event.GatheringID, nil
	case seriesID != nil:
		series, err := s.loadSeries(ctx, string(*seriesID))
		if err != nil {
			return store.RoleScope{}, "", err
		}
		return store.RoleScope{SeriesID: series.ID}, series.GatheringID, nil
	default:
		return store.RoleScope{}, "", Invalid("event_id", "name an event or a series")
	}
}
