package csilservices

import (
	"fmt"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

// Materialization bounds. An open-ended rule describes infinitely many
// occurrences; a database holds finitely many. These are where the line is
// drawn.
const (
	// defaultHorizon is how far ahead a series is materialized when nobody
	// asks for more. A year covers every rule in the domain — a quarterly
	// rule still yields four occurrences — without filling the table for a
	// series nobody is reading.
	defaultHorizon = 365 * 24 * time.Hour

	// maxHorizon caps what ExpandEventSeries will honour, so one request
	// cannot ask for a century of a weekly series.
	maxHorizon = 3 * 365 * 24 * time.Hour

	// maxOccurrencesPerExpansion is the second, independent bound. The
	// horizon bounds TIME; this bounds ROWS, because a weekly rule and a
	// yearly rule reach the same horizon with very different row counts.
	maxOccurrencesPerExpansion = 500
)

// occurrenceTimes returns the instants a rule produces, in order, from
// `from` up to and including `through`.
//
// The rule is evaluated in the series' own timezone and only then converted
// to UTC. That order is the whole reason start_time and timezone are stored
// instead of a UTC offset: "the second Thursday at 19:00" has to stay 19:00
// local on both sides of a daylight-saving boundary, and an offset fixed at
// scheduling time cannot do that.
//
// Two rules produce no occurrence for a period rather than an approximate
// one: a fifth-weekday rule in a period with only four, and a day-of-month
// rule past the end of a short month. "The fifth Thursday" means the fifth
// one, and silently returning the fourth would be a different rule.
func occurrenceTimes(s *store.EventSeries, from, through time.Time) ([]time.Time, error) {
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return nil, fmt.Errorf("loading timezone %q: %w", s.Timezone, err)
	}
	hour, minute, err := parseClock(s.StartTime)
	if err != nil {
		return nil, err
	}

	// The rule's window: never before the series begins, never after it ends.
	windowStart := maxTime(from, s.StartsOn)
	windowEnd := through
	if s.EndsOn != nil && s.EndsOn.Before(windowEnd) {
		windowEnd = *s.EndsOn
	}
	if windowEnd.Before(windowStart) {
		return nil, nil
	}

	interval := s.Recurrence.Interval
	if interval < 1 {
		interval = 1
	}

	// Anchor every period count at the series' own start date, so which
	// periods a rule with interval > 1 selects does not shift when the
	// horizon is later extended from a different `from`.
	anchor := s.StartsOn.In(loc)
	var times []time.Time

	switch s.Recurrence.Freq {
	case store.FreqWeekly:
		weekday, err := ruleWeekday(s.Recurrence)
		if err != nil {
			return nil, err
		}
		// Walk from the first occurrence of that weekday on or after the
		// anchor, then stride by whole weeks.
		cursor := nextWeekday(dateIn(anchor, loc), weekday)
		for period := int64(0); ; period++ {
			at := atClock(cursor, hour, minute, loc)
			if at.After(windowEnd) {
				break
			}
			if period%interval == 0 && !at.Before(windowStart) {
				times = append(times, at.UTC())
				if len(times) >= maxOccurrencesPerExpansion {
					break
				}
			}
			cursor = cursor.AddDate(0, 0, 7)
		}

	case store.FreqMonthly, store.FreqQuarterly, store.FreqYearly:
		monthsPerPeriod := map[store.RecurrenceFreq]int{
			store.FreqMonthly: 1, store.FreqQuarterly: 3, store.FreqYearly: 12,
		}[s.Recurrence.Freq]
		// Start at the first day of the anchor's period and step whole
		// periods, so a rule never drifts by the length of a month.
		cursor := periodStart(anchor, monthsPerPeriod, loc)
		for period := int64(0); ; period++ {
			periodBegin := cursor
			periodEnd := periodBegin.AddDate(0, monthsPerPeriod, 0)
			if atClock(periodBegin, hour, minute, loc).After(windowEnd) {
				break
			}
			if period%interval == 0 {
				day, ok := dayInPeriod(periodBegin, periodEnd, s.Recurrence, loc)
				if ok {
					at := atClock(day, hour, minute, loc)
					if !at.Before(windowStart) && !at.After(windowEnd) {
						times = append(times, at.UTC())
						if len(times) >= maxOccurrencesPerExpansion {
							break
						}
					}
				}
			}
			cursor = periodEnd
		}

	default:
		return nil, fmt.Errorf("unknown recurrence frequency %q", s.Recurrence.Freq)
	}

	return times, nil
}

// dayInPeriod finds the day a monthly, quarterly or yearly rule selects
// inside one period. ok is false when the period does not contain that day —
// a fifth Thursday in a period with four, or the 31st of a short month.
func dayInPeriod(begin, end time.Time, r store.Recurrence, loc *time.Location) (time.Time, bool) {
	if r.DayOfMonth != nil {
		day := time.Date(begin.Year(), begin.Month(), int(*r.DayOfMonth), 0, 0, 0, 0, loc)
		// Go normalizes an out-of-range day forward into the next month
		// (31 April becomes 1 May), so the month having changed is exactly
		// the "this period is too short" case.
		if day.Month() != begin.Month() {
			return time.Time{}, false
		}
		return day, true
	}

	weekday, err := ruleWeekday(r)
	if err != nil {
		return time.Time{}, false
	}
	ordinal := int64(1)
	if r.Ordinal != nil {
		ordinal = *r.Ordinal
	}

	if ordinal == -1 {
		// The last such weekday: walk back from the day before the period
		// ends.
		for day := end.AddDate(0, 0, -1); !day.Before(begin); day = day.AddDate(0, 0, -1) {
			if day.Weekday() == weekday {
				return day, true
			}
		}
		return time.Time{}, false
	}

	seen := int64(0)
	for day := nextWeekday(begin, weekday); day.Before(end); day = day.AddDate(0, 0, 7) {
		seen++
		if seen == ordinal {
			return day, true
		}
	}
	return time.Time{}, false
}

// ruleWeekday reads the rule's weekday, rejecting a rule that has neither a
// weekday nor a day of the month — validateRecurrence refuses one of those
// long before storage, so reaching here means an invariant broke.
func ruleWeekday(r store.Recurrence) (time.Weekday, error) {
	if r.Weekday == nil {
		return 0, fmt.Errorf("recurrence rule names no weekday")
	}
	weekday, ok := store.TimeWeekday(*r.Weekday)
	if !ok {
		return 0, fmt.Errorf("unknown weekday %q", *r.Weekday)
	}
	return weekday, nil
}

// periodStart is the first day of the period containing t. Quarters are
// calendar quarters beginning in January, so "first Saturday of the quarter"
// means January, April, July and October regardless of when the series was
// created.
func periodStart(t time.Time, monthsPerPeriod int, loc *time.Location) time.Time {
	month := int(t.Month()) - 1
	month -= month % monthsPerPeriod
	return time.Date(t.Year(), time.Month(month+1), 1, 0, 0, 0, 0, loc)
}

// nextWeekday is the first day on or after t that falls on weekday.
func nextWeekday(t time.Time, weekday time.Weekday) time.Time {
	delta := (int(weekday) - int(t.Weekday()) + 7) % 7
	return t.AddDate(0, 0, delta)
}

// dateIn strips the clock from t, in loc.
func dateIn(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// atClock puts the local wall-clock time onto a date. time.Date resolves a
// clock time that daylight saving skipped, so a 02:30 rule in a zone that
// jumps 02:00 to 03:00 still yields one instant rather than none.
func atClock(day time.Time, hour, minute int, loc *time.Location) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
}

// parseClock reads the "HH:MM" the schema's regex already constrained.
func parseClock(s string) (hour, minute int, err error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, fmt.Errorf("parsing start time %q: %w", s, err)
	}
	return t.Hour(), t.Minute(), nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
