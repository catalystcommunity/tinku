-- +goose Up
-- A series' first and last days are calendar days, not instants.
--
-- They were stored as the instant a day begins (starts_on) or nearly ends
-- (ends_on, 23:59) in the series' own timezone. That instant changes its
-- day when somebody edits the timezone, and an instant at midnight UTC is
-- the day before west of Greenwich. The day, read in the series' own
-- timezone, is what the rule means, so that is what the row holds now.

ALTER TABLE event_series
    ALTER COLUMN starts_on TYPE date USING (starts_on AT TIME ZONE timezone)::date,
    ALTER COLUMN ends_on   TYPE date USING (ends_on   AT TIME ZONE timezone)::date;

-- +goose Down
-- The instant each day begins in the series' timezone, and 23:59 on the
-- last day: the values the client wrote before the Up step.
ALTER TABLE event_series
    ALTER COLUMN starts_on TYPE timestamptz USING (starts_on::timestamp AT TIME ZONE timezone),
    ALTER COLUMN ends_on   TYPE timestamptz
        USING ((ends_on::timestamp + interval '23 hours 59 minutes') AT TIME ZONE timezone);
