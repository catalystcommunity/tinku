-- +goose Up
-- The gathering domain: organizations, the gatherings they own, the
-- events and recurring series under those, and who holds which role where.
--
-- Four decisions in this schema are worth reading before the tables:
--
--  1. AN OCCURRENCE IS AN EVENT. A series holds a rule; the occurrences it
--     produces are ordinary `events` rows with `series_id` set. Attendance,
--     the start-time lock, search by time and search by place therefore
--     need exactly one implementation, not two. `series_id` is ON DELETE
--     SET NULL so an occurrence that has already happened survives the
--     deletion of its rule — it is history, and history is not the rule's
--     to take back.
--
--  2. OWNERSHIP IS POLYMORPHIC AND REFERENTIALLY REAL. A gathering is owned
--     by individuals and by organizations at once. Rather than one
--     (kind, id) pair with no foreign key, `gathering_owners` carries two
--     nullable columns that each have a real FK, and a CHECK that exactly
--     one is set. The database can then clean up after a deleted user or
--     organization by itself.
--
--  3. SEARCH TEXT IS DENORMALIZED AND PRE-LOWERCASED. Every searchable row
--     carries `search_text`, which the api writes as the lowercase
--     concatenation of that row's searchable fields. Neither `lower()` nor
--     a full-text index is used, because SQLite's `lower()` folds ASCII
--     only and its FTS is a separate extension: doing the folding in Go
--     (which is Unicode-correct) is the only way the two dialects give the
--     same answer. See ../sqlite/000002_gatherings.sql.
--
--  4. COORDINATES ARE PLAIN NUMBERS. Proximity search prefilters on a
--     latitude/longitude box in SQL and refines it to a circle in Go.
--     PostGIS would be better here and has no SQLite counterpart, so it is
--     not a dependency this schema takes.

-- The global admin role rides on the users table rather than in its own,
-- because it is one bit and every session load already reads this row.
ALTER TABLE users ADD COLUMN is_admin         boolean     NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN admin_granted_at timestamptz;

CREATE INDEX users_is_admin_idx ON users (is_admin) WHERE is_admin;

-- An organization is a set of people that can hold ownership. It is
-- addressed as
-- `slug@origin_domain`, the same shape as a person's `handle@domain`, so
-- that a row arriving from a peer later can say where it came from.
CREATE TABLE organizations (
    id            text PRIMARY KEY,
    slug          text        NOT NULL,
    origin_domain text        NOT NULL,
    name          text        NOT NULL,
    blurb         text        NOT NULL DEFAULT '',
    description   text        NOT NULL DEFAULT '',
    search_text   text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (origin_domain, slug)
);
CREATE INDEX organizations_created_at_idx ON organizations (created_at DESC);

CREATE TABLE organization_members (
    organization_id  text        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id   text        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role      text        NOT NULL CHECK (role IN ('owner', 'member')),
    joined_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, user_id)
);
CREATE INDEX organization_members_user_idx ON organization_members (user_id);

-- A gathering is what people join and what events hang off. A meetup.com
-- "group" is this, not tinku's organization.
CREATE TABLE gatherings (
    id            text PRIMARY KEY,
    slug          text        NOT NULL,
    origin_domain text        NOT NULL,
    name          text        NOT NULL,
    blurb         text        NOT NULL DEFAULT '',
    description   text        NOT NULL DEFAULT '',
    search_text   text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (origin_domain, slug)
);
CREATE INDEX gatherings_created_at_idx ON gatherings (created_at DESC);

-- Exactly one of user_id and organization_id is set. The two partial unique
-- indexes below are what a composite primary key would have been if a
-- primary key could contain a null.
CREATE TABLE gathering_owners (
    id           text PRIMARY KEY,
    gathering_id text        NOT NULL REFERENCES gatherings (id) ON DELETE CASCADE,
    user_id      text        REFERENCES users (id)  ON DELETE CASCADE,
    organization_id     text        REFERENCES organizations (id) ON DELETE CASCADE,
    added_at     timestamptz NOT NULL DEFAULT now(),
    CHECK ((user_id IS NOT NULL) <> (organization_id IS NOT NULL))
);
CREATE UNIQUE INDEX gathering_owners_user_idx
    ON gathering_owners (gathering_id, user_id)  WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX gathering_owners_organization_idx
    ON gathering_owners (gathering_id, organization_id) WHERE organization_id IS NOT NULL;
CREATE INDEX gathering_owners_by_user_idx  ON gathering_owners (user_id)  WHERE user_id IS NOT NULL;
CREATE INDEX gathering_owners_by_organization_idx ON gathering_owners (organization_id) WHERE organization_id IS NOT NULL;

CREATE TABLE gathering_members (
    gathering_id text        NOT NULL REFERENCES gatherings (id) ON DELETE CASCADE,
    user_id      text        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    joined_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (gathering_id, user_id)
);
CREATE INDEX gathering_members_user_idx ON gathering_members (user_id);

-- The rule, plus the template every occurrence is stamped from.
--
-- start_time and timezone are stored instead of a UTC offset because "the
-- second Thursday at 19:00" has to stay 19:00 local across a daylight-saving
-- boundary, and an offset cannot express that. materialized_through records
-- how far the horizon has been expanded, so expanding again is cheap and
-- idempotent.
CREATE TABLE event_series (
    id                      text PRIMARY KEY,
    gathering_id            text        NOT NULL REFERENCES gatherings (id) ON DELETE CASCADE,
    title                   text        NOT NULL,
    description             text        NOT NULL DEFAULT '',
    search_text             text        NOT NULL DEFAULT '',
    is_online               boolean     NOT NULL DEFAULT false,
    is_in_person            boolean     NOT NULL DEFAULT false,
    online_url              text        NOT NULL DEFAULT '',
    loc_name                text        NOT NULL DEFAULT '',
    loc_address             text        NOT NULL DEFAULT '',
    loc_locality            text        NOT NULL DEFAULT '',
    loc_region              text        NOT NULL DEFAULT '',
    loc_postal_code         text        NOT NULL DEFAULT '',
    loc_country             text        NOT NULL DEFAULT '',
    loc_latitude            double precision,
    loc_longitude           double precision,
    -- Prefixed because `interval` is a type name in Postgres and reads as
    -- one at a glance even where the parser accepts it.
    recurrence_freq         text        NOT NULL
                              CHECK (recurrence_freq IN ('weekly', 'monthly', 'quarterly', 'yearly')),
    recurrence_interval     integer     NOT NULL DEFAULT 1 CHECK (recurrence_interval >= 1),
    recurrence_weekday      text        CHECK (recurrence_weekday IN
                              ('monday','tuesday','wednesday','thursday','friday','saturday','sunday')),
    recurrence_ordinal      integer     CHECK (recurrence_ordinal BETWEEN 1 AND 5 OR recurrence_ordinal = -1),
    recurrence_day_of_month integer     CHECK (recurrence_day_of_month BETWEEN 1 AND 31),
    starts_on               timestamptz NOT NULL,
    ends_on                 timestamptz,
    start_time              text        NOT NULL,
    duration_minutes        integer     NOT NULL CHECK (duration_minutes > 0),
    timezone                text        NOT NULL,
    materialized_through    timestamptz NOT NULL,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    CHECK (is_online OR is_in_person)
);
CREATE INDEX event_series_gathering_idx ON event_series (gathering_id);

-- One dated thing, whether it stands alone or came from a rule.
CREATE TABLE events (
    id              text PRIMARY KEY,
    gathering_id    text        NOT NULL REFERENCES gatherings (id) ON DELETE CASCADE,
    series_id       text        REFERENCES event_series (id) ON DELETE SET NULL,
    title           text        NOT NULL,
    description     text        NOT NULL DEFAULT '',
    search_text     text        NOT NULL DEFAULT '',
    is_online       boolean     NOT NULL DEFAULT false,
    is_in_person    boolean     NOT NULL DEFAULT false,
    online_url      text        NOT NULL DEFAULT '',
    loc_name        text        NOT NULL DEFAULT '',
    loc_address     text        NOT NULL DEFAULT '',
    loc_locality    text        NOT NULL DEFAULT '',
    loc_region      text        NOT NULL DEFAULT '',
    loc_postal_code text        NOT NULL DEFAULT '',
    loc_country     text        NOT NULL DEFAULT '',
    loc_latitude    double precision,
    loc_longitude   double precision,
    starts_at       timestamptz NOT NULL,
    ends_at         timestamptz NOT NULL,
    timezone        text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (is_online OR is_in_person),
    CHECK (ends_at >= starts_at)
);
CREATE INDEX events_starts_at_idx    ON events (starts_at);
CREATE INDEX events_gathering_idx    ON events (gathering_id, starts_at);
CREATE INDEX events_geo_idx          ON events (loc_latitude, loc_longitude);
CREATE INDEX events_locality_idx     ON events (loc_locality);
-- One occurrence per rule per instant: what makes re-expanding a series
-- idempotent rather than duplicating everything already on disk.
CREATE UNIQUE INDEX events_series_occurrence_idx
    ON events (series_id, starts_at) WHERE series_id IS NOT NULL;

CREATE TABLE event_attendance (
    event_id  text        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    user_id   text        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    marked_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, user_id)
);
CREATE INDEX event_attendance_user_idx ON event_attendance (user_id);

-- Organizer and presenter attach to one event or to one series, never to
-- both and never to neither — the same shape as gathering_owners, for the
-- same reason. Owners are NOT here: ownership is a property of the
-- gathering and reaches every event under it.
CREATE TABLE event_roles (
    id          text PRIMARY KEY,
    event_id    text        REFERENCES events (id) ON DELETE CASCADE,
    series_id   text        REFERENCES event_series (id) ON DELETE CASCADE,
    user_id     text        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role        text        NOT NULL CHECK (role IN ('organizer', 'presenter')),
    assigned_at timestamptz NOT NULL DEFAULT now(),
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
ALTER TABLE users DROP COLUMN admin_granted_at;
ALTER TABLE users DROP COLUMN is_admin;
