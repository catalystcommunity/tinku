package csilservices

import (
	"testing"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

// The three rules the domain names by example, plus the cases that decide
// whether the engine is honest about periods a rule cannot fill.

func denver(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatalf("loading America/Denver: %v", err)
	}
	return loc
}

// seriesFor builds a series whose window is the whole of 2026, so a test
// only has to state the rule.
func seriesFor(r store.Recurrence, startTime string) *store.EventSeries {
	return &store.EventSeries{
		Recurrence:      r,
		StartsOn:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		StartTime:       startTime,
		DurationMinutes: 90,
		Timezone:        "America/Denver",
	}
}

func weekdayPtr(w store.Weekday) *store.Weekday { return &w }
func int64Ptr(n int64) *int64                   { return &n }

// localDates renders occurrences as "2006-01-02 15:04" in the series'
// timezone, which is the form the rule was written in and so the form a
// failure is readable in.
func localDates(t *testing.T, times []time.Time) []string {
	t.Helper()
	loc := denver(t)
	out := make([]string, 0, len(times))
	for _, at := range times {
		out = append(out, at.In(loc).Format("2006-01-02 15:04"))
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestEverySecondThursday(t *testing.T) {
	series := seriesFor(store.Recurrence{
		Freq:     store.FreqMonthly,
		Interval: 1,
		Weekday:  weekdayPtr("thursday"),
		Ordinal:  int64Ptr(2),
	}, "19:00")

	times, err := occurrenceTimes(series,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generating occurrences: %v", err)
	}

	want := []string{
		"2026-01-08 19:00", "2026-02-12 19:00", "2026-03-12 19:00", "2026-04-09 19:00",
		"2026-05-14 19:00", "2026-06-11 19:00", "2026-07-09 19:00", "2026-08-13 19:00",
		"2026-09-10 19:00", "2026-10-08 19:00", "2026-11-12 19:00", "2026-12-10 19:00",
	}
	if got := localDates(t, times); !equalStrings(got, want) {
		t.Errorf("every second Thursday:\n got %v\nwant %v", got, want)
	}
}

func TestEveryFourthWednesday(t *testing.T) {
	series := seriesFor(store.Recurrence{
		Freq:     store.FreqMonthly,
		Interval: 1,
		Weekday:  weekdayPtr("wednesday"),
		Ordinal:  int64Ptr(4),
	}, "18:30")

	times, err := occurrenceTimes(series,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 23, 59, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generating occurrences: %v", err)
	}

	want := []string{
		"2026-01-28 18:30", "2026-02-25 18:30", "2026-03-25 18:30",
		"2026-04-22 18:30", "2026-05-27 18:30", "2026-06-24 18:30",
	}
	if got := localDates(t, times); !equalStrings(got, want) {
		t.Errorf("every fourth Wednesday:\n got %v\nwant %v", got, want)
	}
}

// The quarter is the calendar quarter, not three months counted from
// whenever the series happened to be created — otherwise two series written
// the same way would mean different things.
func TestFirstSaturdayOfTheQuarter(t *testing.T) {
	series := seriesFor(store.Recurrence{
		Freq:     store.FreqQuarterly,
		Interval: 1,
		Weekday:  weekdayPtr("saturday"),
		Ordinal:  int64Ptr(1),
	}, "10:00")

	times, err := occurrenceTimes(series,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generating occurrences: %v", err)
	}

	want := []string{
		"2026-01-03 10:00", "2026-04-04 10:00", "2026-07-04 10:00", "2026-10-03 10:00",
	}
	if got := localDates(t, times); !equalStrings(got, want) {
		t.Errorf("first Saturday of the quarter:\n got %v\nwant %v", got, want)
	}
}

// "Every other Thursday" is the weekly form. It is a DIFFERENT rule from
// "every second Thursday", and both have to be expressible or one of them
// gets written as the other.
func TestEveryOtherThursdayIsWeeklyWithAnInterval(t *testing.T) {
	series := seriesFor(store.Recurrence{
		Freq:     store.FreqWeekly,
		Interval: 2,
		Weekday:  weekdayPtr("thursday"),
	}, "19:00")

	times, err := occurrenceTimes(series,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 28, 23, 59, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generating occurrences: %v", err)
	}

	want := []string{
		"2026-01-01 19:00", "2026-01-15 19:00", "2026-01-29 19:00",
		"2026-02-12 19:00", "2026-02-26 19:00",
	}
	if got := localDates(t, times); !equalStrings(got, want) {
		t.Errorf("every other Thursday:\n got %v\nwant %v", got, want)
	}
}

// A fifth Thursday means the fifth one. A month with only four produces
// nothing, rather than quietly producing the fourth — which would be a
// different rule than the one somebody wrote.
func TestFifthWeekdaySkipsShortMonths(t *testing.T) {
	series := seriesFor(store.Recurrence{
		Freq:     store.FreqMonthly,
		Interval: 1,
		Weekday:  weekdayPtr("thursday"),
		Ordinal:  int64Ptr(5),
	}, "19:00")

	times, err := occurrenceTimes(series,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 31, 23, 59, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generating occurrences: %v", err)
	}

	// January and April have five Thursdays in 2026; February and March
	// have four, and produce nothing at all.
	want := []string{"2026-01-29 19:00", "2026-04-30 19:00"}
	if got := localDates(t, times); !equalStrings(got, want) {
		t.Errorf("fifth Thursday:\n got %v\nwant %v", got, want)
	}
}

func TestLastWeekdayOfTheMonth(t *testing.T) {
	series := seriesFor(store.Recurrence{
		Freq:     store.FreqMonthly,
		Interval: 1,
		Weekday:  weekdayPtr("friday"),
		Ordinal:  int64Ptr(-1),
	}, "17:00")

	times, err := occurrenceTimes(series,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 31, 23, 59, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generating occurrences: %v", err)
	}

	want := []string{"2026-01-30 17:00", "2026-02-27 17:00", "2026-03-27 17:00"}
	if got := localDates(t, times); !equalStrings(got, want) {
		t.Errorf("last Friday:\n got %v\nwant %v", got, want)
	}
}

func TestDayOfMonthSkipsShortMonths(t *testing.T) {
	series := seriesFor(store.Recurrence{
		Freq:       store.FreqMonthly,
		Interval:   1,
		DayOfMonth: int64Ptr(31),
	}, "12:00")

	times, err := occurrenceTimes(series,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 30, 23, 59, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generating occurrences: %v", err)
	}

	// February has no 31st and April has no 31st. Neither is rounded down.
	want := []string{"2026-01-31 12:00", "2026-03-31 12:00"}
	if got := localDates(t, times); !equalStrings(got, want) {
		t.Errorf("31st of the month:\n got %v\nwant %v", got, want)
	}
}

// The reason start_time and timezone are stored instead of a UTC offset:
// 19:00 local has to stay 19:00 local when the offset changes underneath it.
func TestLocalClockSurvivesDaylightSaving(t *testing.T) {
	series := seriesFor(store.Recurrence{
		Freq:     store.FreqWeekly,
		Interval: 1,
		Weekday:  weekdayPtr("thursday"),
	}, "19:00")

	times, err := occurrenceTimes(series,
		time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 20, 23, 59, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generating occurrences: %v", err)
	}
	if len(times) == 0 {
		t.Fatal("expected occurrences either side of the March transition")
	}

	loc := denver(t)
	sawBothOffsets := map[int]bool{}
	for _, at := range times {
		local := at.In(loc)
		if local.Hour() != 19 || local.Minute() != 0 {
			t.Errorf("occurrence %s is not 19:00 local", local.Format(time.RFC3339))
		}
		_, offset := local.Zone()
		sawBothOffsets[offset] = true
	}
	// Denver moves from -07:00 to -06:00 on 8 March 2026, so this window
	// must contain both — otherwise the test proves nothing about the
	// transition it was written for.
	if len(sawBothOffsets) != 2 {
		t.Errorf("expected occurrences on both sides of the offset change, saw offsets %v", sawBothOffsets)
	}
}

// An open-ended rule describes infinitely many occurrences. The row bound
// is what makes it finite on disk regardless of the time bound.
func TestExpansionIsBoundedByRowCount(t *testing.T) {
	series := seriesFor(store.Recurrence{
		Freq:     store.FreqWeekly,
		Interval: 1,
		Weekday:  weekdayPtr("monday"),
	}, "09:00")

	times, err := occurrenceTimes(series,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generating occurrences: %v", err)
	}
	if len(times) != maxOccurrencesPerExpansion {
		t.Errorf("expected the row bound of %d to apply, got %d", maxOccurrencesPerExpansion, len(times))
	}
}

// ends_on closes the window even when the horizon asked for is later.
func TestEndsOnClosesTheSeries(t *testing.T) {
	series := seriesFor(store.Recurrence{
		Freq:     store.FreqMonthly,
		Interval: 1,
		Weekday:  weekdayPtr("thursday"),
		Ordinal:  int64Ptr(2),
	}, "19:00")
	endsOn := time.Date(2026, 3, 31, 23, 59, 0, 0, time.UTC)
	series.EndsOn = &endsOn

	times, err := occurrenceTimes(series,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("generating occurrences: %v", err)
	}

	want := []string{"2026-01-08 19:00", "2026-02-12 19:00", "2026-03-12 19:00"}
	if got := localDates(t, times); !equalStrings(got, want) {
		t.Errorf("series ending in March:\n got %v\nwant %v", got, want)
	}
}
