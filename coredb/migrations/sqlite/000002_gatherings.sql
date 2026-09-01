-- +goose Up
-- The SQLite dialect of ../postgres/000002_gatherings.sql. Same tables, same
-- columns, same constraints, same indexes. Read that file for what the
-- schema means and why; this header only records what the dialect forces.
--
--   * `timestamptz` -> `text`. SQLite has no date type. The api writes and
--     reads RFC3339 UTC strings in one fixed-width layout
--     (api/internal/store/sqlite), so text comparison and text ordering mean
--     what `WHERE starts_at >= ?` and `ORDER BY starts_at` assume.
--   * `boolean` -> `integer` holding 0 or 1. database/sql converts either
--     direction, so the Go side is `bool` against both dialects.
--   * `double precision` -> `real`.
--   * `now()` -> `strftime(...)`, same RFC3339 UTC shape.
--   * Partial unique indexes and CHECK constraints are the same in both;
--     SQLite has had `CREATE UNIQUE INDEX ... WHERE` since 3.8.
--   * SQLite cannot DROP a column added by an earlier statement in some
--     builds, so the Down here rebuilds `users` instead of dropping the two
--     admin columns. It is the same end state.
--
-- Keep the two files in step: a change to one is a change to both.

ALTER TABLE users ADD COLUMN is_admin         integer NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN admin_granted_at text;

CREATE INDEX users_is_admin_idx ON users (is_admin) WHERE is_admin = 1;

CREATE TABLE organizations (
    id            text PRIMARY KEY,
    slug          text NOT NULL,
    origin_domain text NOT NULL,
    name          text NOT NULL,
    blurb         text NOT NULL DEFAULT '',
    description   text NOT NULL DEFAULT '',
    search_text   text NOT NULL DEFAULT '',
    created_at    text NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at    text NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    UNIQUE (origin_domain, slug)
);
CREATE INDEX organizations_created_at_idx ON organizations (created_at DESC);

CREATE TABLE organization_members (
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id   text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role      text NOT NULL CHECK (role IN ('owner', 'member')),
    joined_at text NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    PRIMARY KEY (organization_id, user_id)
);
CREATE INDEX organization_members_user_idx ON organization_members (user_id);

CREATE TABLE gatherings (
    id            text PRIMARY KEY,
    slug          text NOT NULL,
    origin_domain text NOT NULL,
    name          text NOT NULL,
    blurb         text NOT NULL DEFAULT '',
    description   text NOT NULL DEFAULT '',
    search_text   text NOT NULL DEFAULT '',
    created_at    text NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at    text NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    UNIQUE (origin_domain, slug)
);
CREATE INDEX gatherings_created_at_idx ON gatherings (created_at DESC);

CREATE TABLE gathering_owners (
    id           text PRIMARY KEY,
    gathering_id text NOT NULL REFERENCES gatherings (id) ON DELETE CASCADE,
    user_id      text REFERENCES users (id)  ON DELETE CASCADE,
    organization_id     text REFERENCES organizations (id) ON DELETE CASCADE,
    added_at     text NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    CHECK ((user_id IS NOT NULL) <> (organization_id IS NOT NULL))
);
CREATE UNIQUE INDEX gathering_owners_user_idx
    ON gathering_owners (gathering_id, user_id)  WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX gathering_owners_organization_idx
    ON gathering_owners (gathering_id, organization_id) WHERE organization_id IS NOT NULL;
CREATE INDEX gathering_owners_by_user_idx  ON gathering_owners (user_id)  WHERE user_id IS NOT NULL;
CREATE INDEX gathering_owners_by_organization_idx ON gathering_owners (organization_id) WHERE organization_id IS NOT NULL;

CREATE TABLE gathering_members (
    gathering_id text NOT NULL REFERENCES gatherings (id) ON DELETE CASCADE,
    user_id      text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    joined_at    text NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    PRIMARY KEY (gathering_id, user_id)
);
CREATE INDEX gathering_members_user_idx ON gathering_members (user_id);

CREATE TABLE event_series (
    id                      text PRIMARY KEY,
    gathering_id            text    NOT NULL REFERENCES gatherings (id) ON DELETE CASCADE,
    title                   text    NOT NULL,
    description             text    NOT NULL DEFAULT '',
    search_text             text    NOT NULL DEFAULT '',
    is_online               integer NOT NULL DEFAULT 0,
    is_in_person            integer NOT NULL DEFAULT 0,
    online_url              text    NOT NULL DEFAULT '',
    loc_name                text    NOT NULL DEFAULT '',
    loc_address             text    NOT NULL DEFAULT '',
    loc_locality            text    NOT NULL DEFAULT '',
    loc_region              text    NOT NULL DEFAULT '',
    loc_postal_code         text    NOT NULL DEFAULT '',
    loc_country             text    NOT NULL DEFAULT '',
    loc_latitude            real,
    loc_longitude           real,
    recurrence_freq         text    NOT NULL
                              CHECK (recurrence_freq IN ('weekly', 'monthly', 'quarterly', 'yearly')),
    recurrence_interval     integer NOT NULL DEFAULT 1 CHECK (recurrence_interval >= 1),
    recurrence_weekday      text    CHECK (recurrence_weekday IN
                              ('monday','tuesday','wednesday','thursday','friday','saturday','sunday')),
    recurrence_ordinal      integer CHECK (recurrence_ordinal BETWEEN 1 AND 5 OR recurrence_ordinal = -1),
    recurrence_day_of_month integer CHECK (recurrence_day_of_month BETWEEN 1 AND 31),
    starts_on               text    NOT NULL,
    ends_on                 text,
    start_time              text    NOT NULL,
    duration_minutes        integer NOT NULL CHECK (duration_minutes > 0),
    timezone                text    NOT NULL,
    materialized_through    text    NOT NULL,
    created_at              text    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at              text    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    CHECK (is_online = 1 OR is_in_person = 1)
);
CREATE INDEX event_series_gathering_idx ON event_series (gathering_id);

CREATE TABLE events (
    id              text PRIMARY KEY,
    gathering_id    text    NOT NULL REFERENCES gatherings (id) ON DELETE CASCADE,
    series_id       text    REFERENCES event_series (id) ON DELETE SET NULL,
    title           text    NOT NULL,
    description     text    NOT NULL DEFAULT '',
    search_text     text    NOT NULL DEFAULT '',
    is_online       integer NOT NULL DEFAULT 0,
    is_in_person    integer NOT NULL DEFAULT 0,
    online_url      text    NOT NULL DEFAULT '',
    loc_name        text    NOT NULL DEFAULT '',
    loc_address     text    NOT NULL DEFAULT '',
    loc_locality    text    NOT NULL DEFAULT '',
    loc_region      text    NOT NULL DEFAULT '',
    loc_postal_code text    NOT NULL DEFAULT '',
    loc_country     text    NOT NULL DEFAULT '',
    loc_latitude    real,
    loc_longitude   real,
    starts_at       text    NOT NULL,
    ends_at         text    NOT NULL,
    timezone        text    NOT NULL,
    created_at      text    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at      text    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    CHECK (is_online = 1 OR is_in_person = 1),
    CHECK (ends_at >= starts_at)
);
CREATE INDEX events_starts_at_idx ON events (starts_at);
CREATE INDEX events_gathering_idx ON events (gathering_id, starts_at);
CREATE INDEX events_geo_idx       ON events (loc_latitude, loc_longitude);
CREATE INDEX events_locality_idx  ON events (loc_locality);
CREATE UNIQUE INDEX events_series_occurrence_idx
    ON events (series_id, starts_at) WHERE series_id IS NOT NULL;

CREATE TABLE event_attendance (
    event_id  text NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    user_id   text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    marked_at text NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    PRIMARY KEY (event_id, user_id)
);
CREATE INDEX event_attendance_user_idx ON event_attendance (user_id);

CREATE TABLE event_roles (
    id          text PRIMARY KEY,
    event_id    text REFERENCES events (id) ON DELETE CASCADE,
    series_id   text REFERENCES event_series (id) ON DELETE CASCADE,
    user_id     text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role        text NOT NULL CHECK (role IN ('organizer', 'presenter')),
    assigned_at text NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    CHECK ((event_id IS NOT NULL) <> (series_id IS NOT NULL))
);
CREATE UNIQUE INDEX event_roles_event_idx
    ON event_roles (event_id, user_id, role)  WHERE event_id IS NOT NULL;
CREATE UNIQUE INDEX event_roles_series_idx
    ON event_roles (series_id, user_id, role) WHERE series_id IS NOT NULL;
CREATE INDEX event_roles_user_idx ON event_roles (user_id);

-- +goose Down
DROP TABLE event_roles;
DROP TABLE event_attendance;
DROP TABLE events;
DROP TABLE event_series;
DROP TABLE gathering_members;
DROP TABLE gathering_owners;
DROP TABLE gatherings;
DROP TABLE organization_members;
DROP TABLE organizations;
DROP INDEX users_is_admin_idx;

-- Rebuild `users` without the two admin columns. Older SQLite builds have
-- no ALTER TABLE DROP COLUMN at all, and the ones that do refuse when an
-- index references the column; recreating the table works everywhere.
CREATE TABLE users_without_admin (
    id               text PRIMARY KEY,
    linkkeys_domain  text NOT NULL,
    linkkeys_user_id text NOT NULL,
    handle           text NOT NULL,
    display_name     text NOT NULL DEFAULT '',
    kind             text NOT NULL DEFAULT 'human'
                       CHECK (kind IN ('human', 'system')),
    created_at       text NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    UNIQUE (linkkeys_domain, linkkeys_user_id)
);
INSERT INTO users_without_admin (id, linkkeys_domain, linkkeys_user_id, handle, display_name, kind, created_at)
    SELECT id, linkkeys_domain, linkkeys_user_id, handle, display_name, kind, created_at FROM users;
DROP TABLE users;
ALTER TABLE users_without_admin RENAME TO users;
CREATE UNIQUE INDEX users_handle_idx ON users (handle);
