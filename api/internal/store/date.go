package store

import (
	"database/sql/driver"
	"fmt"
	"time"
)

// Date is a calendar day with no time and no zone: the CSIL `date` type,
// "2026-02-11" on the wire. A series' starts_on and ends_on are dates
// because they are part of the rule, read in the series' own timezone.
//
// It is not an instant. An instant at midnight UTC is the day before west
// of Greenwich, and a stored instant changes its day when the series'
// timezone changes. Ask for an instant with Midnight when one is needed.
//
// Date is a value for both store backends: PostgreSQL holds it in a `date`
// column, SQLite as "YYYY-MM-DD" text, which sorts in calendar order.
type Date struct {
	year  int
	month time.Month
	day   int
}

const dateLayout = "2006-01-02"

// ParseDate reads "YYYY-MM-DD". The day must exist: 2026-02-30 is an error.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("not a calendar date %q: want YYYY-MM-DD", s)
	}
	return DateOf(t), nil
}

// DateOf is the calendar day of t, in t's own location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{year: y, month: m, day: d}
}

// NewDate makes a date. An out-of-range day or month is normalized the way
// time.Date normalizes it.
func NewDate(year int, month time.Month, day int) Date {
	return DateOf(time.Date(year, month, day, 0, 0, 0, 0, time.UTC))
}

// IsZero reports whether d is the zero Date, which is not a real day.
func (d Date) IsZero() bool { return d == Date{} }

// String is "YYYY-MM-DD".
func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.year, d.month, d.day)
}

// Midnight is the instant that day begins in loc.
func (d Date) Midnight(loc *time.Location) time.Time {
	return time.Date(d.year, d.month, d.day, 0, 0, 0, 0, loc)
}

// AddDays moves d by n days.
func (d Date) AddDays(n int) Date {
	return NewDate(d.year, d.month, d.day+n)
}

// Before reports whether d is an earlier day than o.
func (d Date) Before(o Date) bool {
	if d.year != o.year {
		return d.year < o.year
	}
	if d.month != o.month {
		return d.month < o.month
	}
	return d.day < o.day
}

// Value writes "YYYY-MM-DD". PostgreSQL casts the text to its `date` type.
func (d Date) Value() (driver.Value, error) {
	if d.IsZero() {
		return nil, fmt.Errorf("storing the zero Date")
	}
	return d.String(), nil
}

// Scan reads a PostgreSQL `date` (a time.Time at midnight UTC) or SQLite
// text.
func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case time.Time:
		*d = DateOf(v)
		return nil
	case string:
		return d.scanText(v)
	case []byte:
		return d.scanText(string(v))
	default:
		return fmt.Errorf("scanning a Date from %T", src)
	}
}

func (d *Date) scanText(s string) error {
	// Accept a full timestamp text too, so a driver that renders a `date`
	// as "2026-02-11T00:00:00Z" still reads the right day.
	if len(s) > len(dateLayout) {
		s = s[:len(dateLayout)]
	}
	parsed, err := ParseDate(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
