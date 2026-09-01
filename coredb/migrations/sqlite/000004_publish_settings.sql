-- +goose Up
-- The SQLite dialect of ../postgres/000004_publish_settings.sql. Same
-- tables, same columns, same constraints. Read that file for what they
-- mean; `timestamptz` becomes RFC3339 UTC text and nothing else differs.
--
-- Keep the two files in step: a change to one is a change to both.
--
-- Instance settings, the three-level publish choice, and per-peer rate
-- limiting.
--
--  1. SETTINGS LIVE IN THE DATABASE, not the environment. An administrator
--     changes them while the service runs, and every replica sees the same
--     value. A key/value table rather than a column per setting: these are
--     read as a group, they are all small, and a new one must not be a
--     migration.
--
--  2. PUBLISHING IS A THREE-LEVEL CHOICE. The instance sets the default and
--     says which levels may override it; an organization and a gathering
--     may then each say `in` or `out`. NULL means "not set", which is why
--     these are nullable text and not booleans — a boolean cannot tell
--     "off" from "unstated".
--
--  3. THE RATE LIMIT IS A FIXED WINDOW ON THE PEER ROW. Two columns, reset
--     when the minute rolls over. A peer that loses control of itself
--     cannot fill this instance's directory: it gets its allowance and the
--     rest of the batch is refused.

CREATE TABLE instance_settings (
    key        text PRIMARY KEY,
    value      text        NOT NULL,
    updated_at text NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- `in` or `out`; NULL is "not set", and the level above decides.
ALTER TABLE organizations ADD COLUMN publish_events text
    CHECK (publish_events IN ('in', 'out'));
ALTER TABLE gatherings    ADD COLUMN publish_events text
    CHECK (publish_events IN ('in', 'out'));

-- NULL means "use the instance default", so raising the instance default
-- raises it for every peer that has not been given its own.
ALTER TABLE federation_peers ADD COLUMN rate_limit_per_minute integer
    CHECK (rate_limit_per_minute IS NULL OR rate_limit_per_minute >= 0);
-- The current fixed window: when it began, and how many events have been
-- accepted in it.
ALTER TABLE federation_peers ADD COLUMN rate_window_start text;
ALTER TABLE federation_peers ADD COLUMN rate_window_count integer NOT NULL DEFAULT 0;
-- How many events this peer has had refused for exceeding its allowance,
-- ever. An administrator needs to see that a peer is being throttled, not
-- merely that it is quiet.
ALTER TABLE federation_peers ADD COLUMN rate_limited_total integer NOT NULL DEFAULT 0;
-- How many the last allowance call granted.
--
-- The consume has to be ONE statement, or two callers both read an empty
-- window and both fill it — and the API has to scale horizontally, so a
-- mutex in one process is not an answer. A single UPDATE takes a row lock
-- and is safe, but its RETURNING clause sees the NEW row, so the granted
-- amount cannot be recomputed there from the old count. It is written here
-- instead, and read back in the same statement.
ALTER TABLE federation_peers ADD COLUMN rate_last_allowed integer NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE federation_peers DROP COLUMN rate_last_allowed;
ALTER TABLE federation_peers DROP COLUMN rate_limited_total;
ALTER TABLE federation_peers DROP COLUMN rate_window_count;
ALTER TABLE federation_peers DROP COLUMN rate_window_start;
ALTER TABLE federation_peers DROP COLUMN rate_limit_per_minute;
ALTER TABLE gatherings    DROP COLUMN publish_events;
ALTER TABLE organizations DROP COLUMN publish_events;
DROP TABLE instance_settings;
