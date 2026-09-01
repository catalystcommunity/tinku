package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

// seriesColumnsSQL reads one series with the two derived facts a listing
// needs: how many occurrences are materialized, and when the next one that
// has not started begins. The clock is read in SQL rather than bound — see
// gatheringColumnsSQL for why a bind here breaks the shared count query.
const seriesColumnsSQL = `
SELECT es.id, es.gathering_id, es.title, es.description,
       es.is_online, es.is_in_person, es.online_url,
       es.loc_name, es.loc_address, es.loc_locality, es.loc_region, es.loc_postal_code, es.loc_country,
       es.loc_latitude, es.loc_longitude,
       es.recurrence_freq, es.recurrence_interval, es.recurrence_weekday,
       es.recurrence_ordinal, es.recurrence_day_of_month,
       es.starts_on, es.ends_on, es.start_time, es.duration_minutes, es.timezone,
       es.materialized_through, es.created_at, es.updated_at,
       (SELECT count(*) FROM events e WHERE e.series_id = es.id),
       (SELECT min(e.starts_at) FROM events e WHERE e.series_id = es.id
          AND e.starts_at > strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
FROM event_series es`

func scanSeries(row rowScanner) (*store.EventSeries, error) {
	var s store.EventSeries
	var freq, startsOn, materialized, createdAt, updatedAt string
	var weekday, endsOn, nextOccurrence *string
	err := row.Scan(&s.ID, &s.GatheringID, &s.Title, &s.Description,
		&s.IsOnline, &s.IsInPerson, &s.OnlineURL,
		&s.Location.Name, &s.Location.Address, &s.Location.Locality, &s.Location.Region,
		&s.Location.PostalCode, &s.Location.Country, &s.Location.Latitude, &s.Location.Longitude,
		&freq, &s.Recurrence.Interval, &weekday, &s.Recurrence.Ordinal, &s.Recurrence.DayOfMonth,
		&startsOn, &endsOn, &s.StartTime, &s.DurationMinutes, &s.Timezone,
		&materialized, &createdAt, &updatedAt,
		&s.OccurrenceCount, &nextOccurrence)
	if err != nil {
		return nil, notFoundIfNoRows(err, "scanning event series")
	}
	s.Recurrence.Freq = store.RecurrenceFreq(freq)
	if weekday != nil {
		w := store.Weekday(*weekday)
		s.Recurrence.Weekday = &w
	}
	for _, conv := range []struct {
		raw string
		dst *time.Time
	}{{startsOn, &s.StartsOn}, {materialized, &s.MaterializedThrough},
		{createdAt, &s.CreatedAt}, {updatedAt, &s.UpdatedAt}} {
		t, err := parseTime(conv.raw)
		if err != nil {
			return nil, err
		}
		*conv.dst = t
	}
	if s.EndsOn, err = parseOptionalTime(endsOn); err != nil {
		return nil, err
	}
	if s.NextOccurrenceAt, err = parseOptionalTime(nextOccurrence); err != nil {
		return nil, err
	}
	return &s, nil
}

const seriesInsertColumns = `id, gathering_id, title, description, search_text,
 is_online, is_in_person, online_url,
 loc_name, loc_address, loc_locality, loc_region, loc_postal_code, loc_country, loc_latitude, loc_longitude,
 recurrence_freq, recurrence_interval, recurrence_weekday, recurrence_ordinal, recurrence_day_of_month,
 starts_on, ends_on, start_time, duration_minutes, timezone, materialized_through`

// seriesValues is the bind list for both the insert and the update, in the
// order seriesInsertColumns names — so the two statements cannot drift.
func seriesValues(in store.EventSeriesInput) []any {
	var weekday *string
	if in.Recurrence.Weekday != nil {
		w := string(*in.Recurrence.Weekday)
		weekday = &w
	}
	var endsOn *string
	if in.EndsOn != nil {
		t := formatTime(*in.EndsOn)
		endsOn = &t
	}
	return []any{
		in.GatheringID, in.Title, in.Description, in.SearchText,
		in.IsOnline, in.IsInPerson, in.OnlineURL,
		in.Location.Name, in.Location.Address, in.Location.Locality, in.Location.Region,
		in.Location.PostalCode, in.Location.Country, in.Location.Latitude, in.Location.Longitude,
		string(in.Recurrence.Freq), in.Recurrence.Interval, weekday,
		in.Recurrence.Ordinal, in.Recurrence.DayOfMonth,
		formatTime(in.StartsOn), endsOn, in.StartTime, in.DurationMinutes, in.Timezone,
		formatTime(in.MaterializedThrough),
	}
}

func (s *Store) CreateEventSeries(ctx context.Context, in store.EventSeriesInput) (*store.EventSeries, error) {
	id := store.NewID()
	vals := append([]any{id}, seriesValues(in)...)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO event_series (`+seriesInsertColumns+`) VALUES (`+bindList(len(vals))+`)`,
		vals...); err != nil {
		return nil, fmt.Errorf("creating event series: %w", err)
	}
	return s.EventSeriesByID(ctx, id)
}

func (s *Store) UpdateEventSeries(ctx context.Context, id string, in store.EventSeriesInput) (*store.EventSeries, error) {
	vals := seriesValues(in)
	// gathering_id is vals[0] and never moves, so it is dropped rather than
	// updated: an event series does not change which gathering it is under.
	res, err := s.db.ExecContext(ctx, `
UPDATE event_series SET title = ?, description = ?, search_text = ?,
                        is_online = ?, is_in_person = ?, online_url = ?,
                        loc_name = ?, loc_address = ?, loc_locality = ?, loc_region = ?,
                        loc_postal_code = ?, loc_country = ?, loc_latitude = ?, loc_longitude = ?,
                        recurrence_freq = ?, recurrence_interval = ?, recurrence_weekday = ?,
                        recurrence_ordinal = ?, recurrence_day_of_month = ?,
                        starts_on = ?, ends_on = ?, start_time = ?, duration_minutes = ?,
                        timezone = ?, materialized_through = ?, updated_at = `+nowSQL+`
WHERE id = ?`, append(vals[1:], id)...)
	if err != nil {
		return nil, fmt.Errorf("updating event series: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("updating event series: %w", err)
	}
	if affected == 0 {
		return nil, store.ErrNotFound
	}
	return s.EventSeriesByID(ctx, id)
}

func (s *Store) EventSeriesByID(ctx context.Context, id string) (*store.EventSeries, error) {
	return scanSeries(s.db.QueryRowContext(ctx, seriesColumnsSQL+` WHERE es.id = ?`, id))
}

func (s *Store) ListEventSeries(ctx context.Context, f store.EventSeriesFilter) ([]store.EventSeries, int64, error) {
	a := &args{}
	where := []string{}
	if f.Query != "" {
		where = append(where, "es.search_text LIKE "+a.next(like(f.Query))+` ESCAPE '\'`)
	}
	if f.GatheringID != "" {
		where = append(where, "es.gathering_id = "+a.next(f.GatheringID))
	}
	placePredicates(&where, a, "es", f.Place)
	clause := whereClause(where)

	var total int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM event_series es`+clause, a.vals...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting event series: %w", err)
	}

	limit, offset := clamp(f.Page)
	rows, err := s.db.QueryContext(ctx, seriesColumnsSQL+clause+
		` ORDER BY es.created_at DESC, es.id DESC LIMIT `+a.next(limit)+` OFFSET `+a.next(offset), a.vals...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing event series: %w", err)
	}
	defer rows.Close()

	series := []store.EventSeries{}
	for rows.Next() {
		one, err := scanSeries(rows)
		if err != nil {
			return nil, 0, err
		}
		series = append(series, *one)
	}
	return series, total, rows.Err()
}

func (s *Store) DeleteEventSeries(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM event_series WHERE id = ?`, id); err != nil {
		return fmt.Errorf("deleting event series: %w", err)
	}
	return nil
}

func (s *Store) DeleteOccurrencesFrom(ctx context.Context, seriesID string, notBefore time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM events WHERE series_id = ? AND starts_at >= ?`,
		seriesID, formatTime(notBefore)); err != nil {
		return fmt.Errorf("deleting series occurrences: %w", err)
	}
	return nil
}

// UpsertOccurrences is idempotent through the partial unique index on
// (series_id, starts_at): an instant the series already has stays as it is,
// so re-expanding a horizon adds only what is genuinely new.
func (s *Store) UpsertOccurrences(ctx context.Context, seriesID string, occurrences []store.EventInput) (int64, error) {
	if len(occurrences) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("beginning occurrence transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has run

	var inserted int64
	for _, in := range occurrences {
		in.SeriesID = &seriesID
		vals := eventInsertValues(store.NewID(), in)
		res, err := tx.ExecContext(ctx,
			`INSERT INTO events (`+eventInsertColumns+`) VALUES (`+bindList(len(vals))+`)
ON CONFLICT (series_id, starts_at) WHERE series_id IS NOT NULL DO NOTHING`, vals...)
		if err != nil {
			return 0, fmt.Errorf("materializing occurrence: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("materializing occurrence: %w", err)
		}
		inserted += n
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing occurrences: %w", err)
	}
	return inserted, nil
}

func (s *Store) SetMaterializedThrough(ctx context.Context, seriesID string, through time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE event_series SET materialized_through = ? WHERE id = ?`,
		formatTime(through), seriesID); err != nil {
		return fmt.Errorf("recording materialized horizon: %w", err)
	}
	return nil
}

// ---- Roles on events and series -------------------------------------------

// scopeColumn names the column a RoleScope constrains, and the value to
// compare it with. The event_roles CHECK guarantees exactly one is set, so
// there is never a third case.
func scopeColumn(scope store.RoleScope) (column, value string) {
	if scope.EventID != "" {
		return "event_id", scope.EventID
	}
	return "series_id", scope.SeriesID
}

func (s *Store) SetEventRole(ctx context.Context, scope store.RoleScope, userID string, role store.EventRole) error {
	if !scope.Valid() {
		return fmt.Errorf("role scope names %s", scopeProblem(scope))
	}
	var eventID, seriesID *string
	if scope.EventID != "" {
		id := scope.EventID
		eventID = &id
	} else {
		id := scope.SeriesID
		seriesID = &id
	}
	column, _ := scopeColumn(scope)
	conflict := fmt.Sprintf("(%s, user_id, role) WHERE %s IS NOT NULL", column, column)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO event_roles (id, event_id, series_id, user_id, role) VALUES (?, ?, ?, ?, ?)
ON CONFLICT `+conflict+` DO NOTHING`, store.NewID(), eventID, seriesID, userID, string(role))
	if err != nil {
		return fmt.Errorf("setting event role: %w", err)
	}
	return nil
}

func (s *Store) RemoveEventRole(ctx context.Context, scope store.RoleScope, userID string, role store.EventRole) error {
	if !scope.Valid() {
		return fmt.Errorf("role scope names %s", scopeProblem(scope))
	}
	column, value := scopeColumn(scope)
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM event_roles WHERE `+column+` = ? AND user_id = ? AND role = ?`,
		value, userID, string(role)); err != nil {
		return fmt.Errorf("removing event role: %w", err)
	}
	return nil
}

func (s *Store) ListEventRoles(ctx context.Context, scope store.RoleScope) ([]store.EventRoleAssignment, error) {
	if !scope.Valid() {
		return nil, fmt.Errorf("role scope names %s", scopeProblem(scope))
	}
	column, value := scopeColumn(scope)
	rows, err := s.db.QueryContext(ctx, `
SELECT r.event_id, r.series_id, r.user_id, u.handle, u.linkkeys_domain, u.display_name, r.role, r.assigned_at
FROM event_roles r JOIN users u ON u.id = r.user_id
WHERE r.`+column+` = ?
ORDER BY CASE WHEN r.role = 'organizer' THEN 0 ELSE 1 END, r.assigned_at, u.handle`, value)
	if err != nil {
		return nil, fmt.Errorf("listing event roles: %w", err)
	}
	defer rows.Close()

	assignments := []store.EventRoleAssignment{}
	for rows.Next() {
		var as store.EventRoleAssignment
		var role, assignedAt string
		if err := rows.Scan(&as.EventID, &as.SeriesID, &as.UserID, &as.Handle, &as.LinkkeysDomain,
			&as.DisplayName, &role, &assignedAt); err != nil {
			return nil, fmt.Errorf("scanning event role: %w", err)
		}
		as.Role = store.EventRole(role)
		var err error
		if as.AssignedAt, err = parseTime(assignedAt); err != nil {
			return nil, err
		}
		assignments = append(assignments, as)
	}
	return assignments, rows.Err()
}

// EventRolesFor counts a role held on the event's parent series as held on
// the event: an organizer of "every second Thursday" organizes each second
// Thursday, and making every caller remember that is how one of them
// forgets.
func (s *Store) EventRolesFor(ctx context.Context, eventID, userID string) (bool, bool, error) {
	if userID == "" {
		return false, false, nil
	}
	var organizer, presenter bool
	err := s.db.QueryRowContext(ctx, `
SELECT
  EXISTS (SELECT 1 FROM event_roles r WHERE r.user_id = ? AND r.role = 'organizer'
            AND (r.event_id = ? OR r.series_id = (SELECT series_id FROM events WHERE id = ?))),
  EXISTS (SELECT 1 FROM event_roles r WHERE r.user_id = ? AND r.role = 'presenter'
            AND (r.event_id = ? OR r.series_id = (SELECT series_id FROM events WHERE id = ?)))`,
		userID, eventID, eventID, userID, eventID, eventID).Scan(&organizer, &presenter)
	if err != nil {
		return false, false, fmt.Errorf("reading event roles: %w", err)
	}
	return organizer, presenter, nil
}

func (s *Store) SeriesRolesFor(ctx context.Context, seriesID, userID string) (bool, bool, error) {
	if userID == "" {
		return false, false, nil
	}
	var organizer, presenter bool
	err := s.db.QueryRowContext(ctx, `
SELECT
  EXISTS (SELECT 1 FROM event_roles r WHERE r.series_id = ? AND r.user_id = ? AND r.role = 'organizer'),
  EXISTS (SELECT 1 FROM event_roles r WHERE r.series_id = ? AND r.user_id = ? AND r.role = 'presenter')`,
		seriesID, userID, seriesID, userID).Scan(&organizer, &presenter)
	if err != nil {
		return false, false, fmt.Errorf("reading series roles: %w", err)
	}
	return organizer, presenter, nil
}

// scopeProblem names which invariant a bad RoleScope broke, so the error a
// developer sees says what to fix.
func scopeProblem(scope store.RoleScope) string {
	if scope.EventID == "" && scope.SeriesID == "" {
		return "neither an event nor a series"
	}
	return "both an event and a series"
}
