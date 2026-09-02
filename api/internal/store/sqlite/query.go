package sqlite

import (
	"strings"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

// args accumulates bind values and hands back the placeholder that names
// each one. SQLite's placeholder carries no position, so next ignores it —
// that, and the timestamp representation, is the whole difference between
// this file and the Postgres sibling's query.go.
type args struct{ vals []any }

func (a *args) next(v any) string {
	a.vals = append(a.vals, v)
	return "?"
}

// nextTime records a timestamp as the RFC3339 UTC text every timestamp
// column in this schema holds. Postgres binds a time.Time here instead.
func (a *args) nextTime(t time.Time) string { return a.next(formatTime(t)) }

// list records every value in vs and returns "(?, ?, ...)". Callers must not
// pass an empty slice: an empty IN list is not valid SQL, and the caller
// always knows to skip the predicate instead.
func (a *args) list(vs []string) string {
	placeholders := make([]string, 0, len(vs))
	for _, v := range vs {
		placeholders = append(placeholders, a.next(v))
	}
	return "(" + strings.Join(placeholders, ", ") + ")"
}

// nowSQL is what a column defaults to and what an UPDATE sets updated_at
// to. It has to produce exactly what timeLayout parses, which is why it is
// written once here rather than repeated in every statement.
const nowSQL = `strftime('%Y-%m-%dT%H:%M:%SZ', 'now')`

const (
	defaultLimit = 25
	maxLimit     = 100
)

// clamp bounds a page, so no request can ask for an unbounded result set.
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
// search_text, escaping the three characters LIKE treats as syntax.
func like(query string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)
	return "%" + escaped + "%"
}

// likePrefix and likeFold are like() for RANKING rather than filtering: both
// fold case, because they are compared against lower(name), and likePrefix
// anchors at the start — a name the query BEGINS is what a person typing
// into a typeahead means before they mean anything else.
func likePrefix(query string) string {
	return likeFoldBody(query) + "%"
}

func likeFold(query string) string {
	return "%" + likeFoldBody(query) + "%"
}

func likeFoldBody(query string) string {
	return strings.NewReplacer(BS, BS+BS, "%", BS+"%", "_", BS+"_").Replace(strings.ToLower(query))
}

// BS is a single backslash, named so the replacer above reads without a
// thicket of escapes.
const BS = "\\"

// likePrefix is like() anchored at the start: it matches a name the query
// BEGINS, which is what a person typing into a typeahead means before they
// mean anything else.

// placePredicates appends the place half of a filter. The caller has
// already lowercased the filter values, so these fold the stored column to
// match.
//
// This is the ONE query in either backend that can answer differently
// across the two: SQLite's lower() folds ASCII only, so a place name with a
// non-ASCII capital ("Ãrhus") matches case-sensitively here and
// case-insensitively on Postgres. It is documented rather than worked
// around because the fix costs a duplicate lowercase column per place
// field, and this backend exists for local runs and tests — see
// docs/OPERATING.md.
func placePredicates(where *[]string, a *args, alias string, p store.PlaceFilter) {
	if p.Locality != "" {
		*where = append(*where, "lower("+alias+".loc_locality) = "+a.next(p.Locality))
	}
	if p.Region != "" {
		*where = append(*where, "lower("+alias+".loc_region) = "+a.next(p.Region))
	}
	if p.Country != "" {
		*where = append(*where, "lower("+alias+".loc_country) = "+a.next(p.Country))
	}
	if p.Box != nil {
		*where = append(*where,
			alias+".loc_latitude BETWEEN "+a.next(p.Box.MinLatitude)+" AND "+a.next(p.Box.MaxLatitude)+
				" AND "+alias+".loc_longitude BETWEEN "+a.next(p.Box.MinLongitude)+" AND "+a.next(p.Box.MaxLongitude))
	}
}

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
