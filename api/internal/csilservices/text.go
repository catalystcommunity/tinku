package csilservices

import (
	"strings"
	"unicode"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

// maxBlurbWords is the limit the domain states for an organization's or a
// gathering's blurb. It is a WORD count, which no CSIL size constraint can
// express — the schema's byte ceiling only stops an abusive payload, and the
// real rule is enforced here.
const maxBlurbWords = 300

// wordCount counts runs of non-space, which is what a person means by
// "words" for a limit like this. It is deliberately not a linguistic word
// count: a rule a writer cannot predict is worse than one that is slightly
// crude.
func wordCount(s string) int {
	return len(strings.FieldsFunc(s, unicode.IsSpace))
}

// slugify derives the local part of a federated address from a name:
// `slug@origin_domain`, the same shape as a person's `handle@domain`.
//
// Lowercase ASCII letters, digits and single hyphens only. A name with no
// usable characters at all — every character non-ASCII, say — yields the
// empty string, and the caller substitutes an id rather than storing a slug
// that says nothing.
func slugify(name string) string {
	var b strings.Builder
	lastHyphen := true // leading hyphens are suppressed
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case !lastHyphen:
			b.WriteRune('-')
			lastHyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// maxSlugLength keeps an address short enough to say out loud. A name longer
// than this is truncated rather than refused: the slug is derived, and
// refusing a legal name because of a derived value would be surprising.
const maxSlugLength = 64

// uniqueSlug builds a slug that no existing row holds, falling back to the
// row's own id. The id suffix is a ULID's last six characters, which is
// enough to separate two gatherings called "Board Games" without making the
// address unreadable.
func uniqueSlug(name, id string, taken func(string) bool) string {
	base := slugify(name)
	if len(base) > maxSlugLength {
		base = strings.Trim(base[:maxSlugLength], "-")
	}
	if base == "" {
		base = strings.ToLower(id)
	}
	if !taken(base) {
		return base
	}
	suffix := strings.ToLower(id)
	if len(suffix) > 6 {
		suffix = suffix[len(suffix)-6:]
	}
	return base + "-" + suffix
}

// searchText builds the lowercase haystack a LIKE query matches against.
//
// The folding happens here, in Go, rather than in SQL: SQLite's lower()
// folds ASCII only, so a query for "ÅRHUS" would miss "Århus" on that
// backend and find it on Postgres. One pre-folded column is what makes the
// two answer the same.
func searchText(parts ...string) string {
	return strings.ToLower(strings.Join(parts, " \n"))
}

// locationSearchParts flattens a location into the strings worth searching.
// Coordinates are not among them: nobody types a latitude into a search box,
// and proximity is a separate filter.
func locationSearchParts(l store.Location) []string {
	return []string{l.Name, l.Address, l.Locality, l.Region, l.PostalCode, l.Country}
}
