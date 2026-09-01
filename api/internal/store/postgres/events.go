package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

// eventColumnsSQL reads one event with its attendee count. ViewerAttending
// is not here: it depends on who is asking, and AttendanceFor fills it for
// a whole page in one further query rather than per row.
const eventColumnsSQL = `
SELECT e.id, e.gathering_id, e.series_id, e.title, e.description,
       e.is_online, e.is_in_person, e.online_url,
       e.loc_name, e.loc_address, e.loc_locality, e.loc_region, e.loc_postal_code, e.loc_country,
       e.loc_latitude, e.loc_longitude,
       e.starts_at, e.ends_at, e.timezone, e.created_at, e.updated_at,
       (SELECT count(*) FROM event_attendance a WHERE a.event_id = e.id)
FROM events e`

func scanEvent(row rowScanner) (*store.Event, error) {
	var e store.Event
	err := row.Scan(&e.ID, &e.GatheringID, &e.SeriesID, &e.Title, &e.Description,
		&e.IsOnline, &e.IsInPerson, &e.OnlineURL,
		&e.Location.Name, &e.Location.Address, &e.Location.Locality, &e.Location.Region,
		&e.Location.PostalCode, &e.Location.Country, &e.Location.Latitude, &e.Location.Longitude,
		&e.StartsAt, &e.EndsAt, &e.Timezone, &e.CreatedAt, &e.UpdatedAt, &e.AttendeeCount)
	if err != nil {
		return nil, notFoundIfNoRows(err, "scanning event")
	}
	return &e, nil
}

// eventInsertColumns and eventInsertValues are shared by CreateEvent and
// UpsertOccurrences: materializing a series is inserting events, so the two
// paths write the same columns in the same order by construction.
const eventInsertColumns = `id, gathering_id, series_id, title, description, search_text,
 is_online, is_in_person, online_url,
 loc_name, loc_address, loc_locality, loc_region, loc_postal_code, loc_country, loc_latitude, loc_longitude,
 starts_at, ends_at, timezone`

func eventInsertValues(id string, in store.EventInput) []any {
	return []any{
		id, in.GatheringID, in.SeriesID, in.Title, in.Description, in.SearchText,
		in.IsOnline, in.IsInPerson, in.OnlineURL,
		in.Location.Name, in.Location.Address, in.Location.Locality, in.Location.Region,
		in.Location.PostalCode, in.Location.Country, in.Location.Latitude, in.Location.Longitude,
		in.StartsAt.UTC(), in.EndsAt.UTC(), in.Timezone,
	}
}

func (s *Store) CreateEvent(ctx context.Context, in store.EventInput) (*store.Event, error) {
	id := store.NewID()
	vals := eventInsertValues(id, in)
	placeholders := make([]string, len(vals))
	for i := range vals {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO events (`+eventInsertColumns+`) VALUES (`+strings.Join(placeholders, ", ")+`)`,
		vals...); err != nil {
		return nil, fmt.Errorf("creating event: %w", err)
	}
	return s.EventByID(ctx, id)
}

func (s *Store) UpdateEvent(ctx context.Context, id string, in store.EventInput) (*store.Event, error) {
	res, err := s.db.ExecContext(ctx, `
UPDATE events SET title = $1, description = $2, search_text = $3,
                  is_online = $4, is_in_person = $5, online_url = $6,
                  loc_name = $7, loc_address = $8, loc_locality = $9, loc_region = $10,
                  loc_postal_code = $11, loc_country = $12, loc_latitude = $13, loc_longitude = $14,
                  starts_at = $15, ends_at = $16, timezone = $17,
                  updated_at = now()
WHERE id = $18`,
		in.Title, in.Description, in.SearchText, in.IsOnline, in.IsInPerson, in.OnlineURL,
		in.Location.Name, in.Location.Address, in.Location.Locality, in.Location.Region,
		in.Location.PostalCode, in.Location.Country, in.Location.Latitude, in.Location.Longitude,
		in.StartsAt.UTC(), in.EndsAt.UTC(), in.Timezone, id)
	if err != nil {
		return nil, fmt.Errorf("updating event: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("updating event: %w", err)
	}
	if affected == 0 {
		return nil, store.ErrNotFound
	}
	return s.EventByID(ctx, id)
}

func (s *Store) EventByID(ctx context.Context, id string) (*store.Event, error) {
	return scanEvent(s.db.QueryRowContext(ctx, eventColumnsSQL+` WHERE e.id = $1`, id))
}

func (s *Store) ListEvents(ctx context.Context, f store.EventFilter) ([]store.Event, int64, error) {
	a := &args{}
	where := []string{}
	if f.Query != "" {
		where = append(where, "e.search_text LIKE "+a.next(like(f.Query))+` ESCAPE '\'`)
	}
	if f.GatheringID != "" {
		where = append(where, "e.gathering_id = "+a.next(f.GatheringID))
	}
	if f.SeriesID != "" {
		where = append(where, "e.series_id = "+a.next(f.SeriesID))
	}
	if f.AttendeeID != "" {
		where = append(where,
			"EXISTS (SELECT 1 FROM event_attendance att WHERE att.event_id = e.id AND att.user_id = "+
				a.next(f.AttendeeID)+")")
	}
	if !f.StartsAfter.IsZero() {
		where = append(where, "e.starts_at >= "+a.nextTime(f.StartsAfter))
	}
	if !f.StartsBefore.IsZero() {
		where = append(where, "e.starts_at <= "+a.nextTime(f.StartsBefore))
	}
	if !f.IncludeStarted {
		where = append(where, "e.starts_at > "+a.nextTime(f.Now))
	}
	if f.OnlineOnly {
		where = append(where, "e.is_online")
	}
	if f.InPersonOnly {
		where = append(where, "e.is_in_person")
	}
	placePredicates(&where, a, "e", f.Place)
	clause := whereClause(where)

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM events e`+clause, a.vals...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting events: %w", err)
	}

	limit, offset := clamp(f.Page)
	rows, err := s.db.QueryContext(ctx, eventColumnsSQL+clause+
		` ORDER BY e.starts_at, e.id LIMIT `+a.next(limit)+` OFFSET `+a.next(offset), a.vals...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing events: %w", err)
	}
	defer rows.Close()

	events := []store.Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, 0, err
		}
		events = append(events, *e)
	}
	return events, total, rows.Err()
}

func (s *Store) DeleteEvent(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE id = $1`, id); err != nil {
		return fmt.Errorf("deleting event: %w", err)
	}
	return nil
}

func (s *Store) AttendEvent(ctx context.Context, eventID, userID string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO event_attendance (event_id, user_id) VALUES ($1, $2)
ON CONFLICT (event_id, user_id) DO NOTHING`, eventID, userID)
	if err != nil {
		return fmt.Errorf("marking attendance: %w", err)
	}
	return nil
}

func (s *Store) UnattendEvent(ctx context.Context, eventID, userID string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM event_attendance WHERE event_id = $1 AND user_id = $2`, eventID, userID); err != nil {
		return fmt.Errorf("withdrawing attendance: %w", err)
	}
	return nil
}

func (s *Store) ListAttendees(ctx context.Context, eventID string, page store.Page) ([]store.Attendee, int64, error) {
	var total int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM event_attendance WHERE event_id = $1`, eventID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting attendees: %w", err)
	}

	limit, offset := clamp(page)
	rows, err := s.db.QueryContext(ctx, `
SELECT a.user_id, u.handle, u.linkkeys_domain, u.display_name, a.marked_at
FROM event_attendance a JOIN users u ON u.id = a.user_id
WHERE a.event_id = $1
ORDER BY a.marked_at, u.handle
LIMIT $2 OFFSET $3`, eventID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing attendees: %w", err)
	}
	defer rows.Close()

	attendees := []store.Attendee{}
	for rows.Next() {
		var at store.Attendee
		if err := rows.Scan(&at.UserID, &at.Handle, &at.LinkkeysDomain, &at.DisplayName, &at.MarkedAt); err != nil {
			return nil, 0, fmt.Errorf("scanning attendee: %w", err)
		}
		attendees = append(attendees, at)
	}
	return attendees, total, rows.Err()
}

func (s *Store) AttendanceFor(ctx context.Context, userID string, eventIDs []string) (map[string]bool, error) {
	attending := map[string]bool{}
	if userID == "" || len(eventIDs) == 0 {
		return attending, nil
	}
	a := &args{}
	user := a.next(userID)
	rows, err := s.db.QueryContext(ctx,
		`SELECT event_id FROM event_attendance WHERE user_id = `+user+` AND event_id IN `+a.list(eventIDs),
		a.vals...)
	if err != nil {
		return nil, fmt.Errorf("reading attendance: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning attendance: %w", err)
		}
		attending[id] = true
	}
	return attending, rows.Err()
}
