-- +goose Up
-- A series' first and last days are calendar days, not instants. The
-- PostgreSQL migration explains why.
--
-- The columns stay `text`; the value becomes "YYYY-MM-DD", which sorts in
-- calendar order. SQLite has no timezone database. The old starts_on was
-- local midnight and the old ends_on local 23:59, so moving each 12 hours
-- toward the middle of its local day gives the right day for every zone
-- from UTC-11 to UTC+12. Outside that range the day can be one off. SQLite
-- is for local runs and tests only.

UPDATE event_series SET starts_on = date(starts_on, '+12 hours');
UPDATE event_series SET ends_on = date(ends_on, '-12 hours') WHERE ends_on IS NOT NULL;

-- +goose Down
UPDATE event_series SET starts_on = starts_on || 'T00:00:00Z';
UPDATE event_series SET ends_on = ends_on || 'T23:59:00Z' WHERE ends_on IS NOT NULL;
