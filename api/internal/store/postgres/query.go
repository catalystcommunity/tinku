package postgres

import (
	"fmt"
	"strings"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

// args accumulates bind values and hands back the placeholder that names
// each one. It is the whole of the placeholder difference between this
// backend and the SQLite one: there, next returns "?" and ignores the
// position. Every dynamic WHERE clause in this package is built through it,
// so no query concatenates a value into SQL text.
type args struct{ vals []any }

// next records v and returns its placeholder.
func (a *args) next(v any) string {
	a.vals = append(a.vals, v)
	return fmt.Sprintf("$%d", len(a.vals))
}

// nextTime records a timestamp. Postgres keeps timestamptz, so this is a
// plain bind; the SQLite sibling formats to RFC3339 text here instead.
func (a *args) nextTime(t time.Time) string { return a.next(t.UTC()) }

// list records every value in vs and returns "($1, $2, ...)". Callers must
// not call it with an empty slice — an empty IN list is not valid SQL, and
// the caller always knows to skip the whole predicate instead.
func (a *args) list(vs []string) string {
	placeholders := make([]string, 0, len(vs))
	for _, v := range vs {
		placeholders = append(placeholders, a.next(v))
	}
	return "(" + strings.Join(placeholders, ", ") + ")"
}

// clamp bounds a page. Every list op runs through it, so no request can ask
// for an unbounded result set however the client is written.
const (
	defaultLimit = 25
	maxLimit     = 100
)

func clamp(p store.Page) (limit, offset int64) {
	limit = p.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	offset = p.Offset
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// like turns a lowercased search term into a LIKE pattern against
// search_text, escaping the three characters LIKE treats as syntax. Without
// the escape, a query containing '%' would match everything, which is a
// wrong answer rather than a slow one.
func like(query string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)
	return "%" + escaped + "%"
}

// placePredicates appends the place half of a filter. The text fields
// compare against the stored value directly because the store keeps them
// lowercased; the box is a plain range test, refined to a circle by the
// caller in Go — see csil/types/search.csil.
func placePredicates(where *[]string, a *args, alias string, p store.PlaceFilter) {
	if p.Locality != "" {
		*where = append(*where, fmt.Sprintf("lower(%s.loc_locality) = %s", alias, a.next(p.Locality)))
	}
	if p.Region != "" {
		*where = append(*where, fmt.Sprintf("lower(%s.loc_region) = %s", alias, a.next(p.Region)))
	}
	if p.Country != "" {
		*where = append(*where, fmt.Sprintf("lower(%s.loc_country) = %s", alias, a.next(p.Country)))
	}
	if p.Box != nil {
		*where = append(*where, fmt.Sprintf(
			"%s.loc_latitude BETWEEN %s AND %s AND %s.loc_longitude BETWEEN %s AND %s",
			alias, a.next(p.Box.MinLatitude), a.next(p.Box.MaxLatitude),
			alias, a.next(p.Box.MinLongitude), a.next(p.Box.MaxLongitude)))
	}
}

// whereClause joins predicates, or returns the empty string when there are
// none — "WHERE" with nothing after it does not parse.
func whereClause(where []string) string {
	if len(where) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(where, " AND ")
}

// joinAnd joins predicates for a nested EXISTS, where the caller supplies
// its own "WHERE" and so cannot use whereClause.
func joinAnd(where []string) string { return strings.Join(where, " AND ") }

// nullablePublish turns the unset setting into a NULL. "Unset" is a third
// state — the level above decides — so it has to reach the database as
// absence rather than as an empty string the CHECK would reject.
func nullablePublish(p store.PublishSetting) *string {
	if p == store.PublishUnset {
		return nil
	}
	value := string(p)
	return &value
}
