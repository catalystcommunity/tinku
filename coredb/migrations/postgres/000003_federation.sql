-- +goose Up
-- Federation: peers, the delivery queue, and what peers sent.
--
-- Three decisions worth reading before the tables:
--
--  1. TWO STATUSES PER PEER, NOT ONE. `inbound_status` and
--     `outbound_status` are independent because the two directions are two
--     decisions. A single "status" column would make accepting a peer's
--     events and publishing to them the same act, which is exactly what
--     both-sides-opt-in exists to prevent.
--
--  2. THE QUEUE COALESCES. One row per (peer, event). An event edited five
--     times before its first delivery is delivered once, in its final
--     state, and a deletion replaces a pending update rather than racing
--     it. That is what the unique index is for; it is not an optimization.
--
--  3. A REMOTE EVENT IS NOT AN EVENT. `remote_events` is its own table. A
--     peer's event has no gathering here, nobody here can edit it, nobody
--     here can attend it, and the start-time lock does not apply because
--     there is no description to withhold. Putting it in `events` would
--     mean every rule that holds for a local event would have to be
--     re-checked for one that cannot obey it.

-- Another instance, as this one sees it. `address` is the identity a
-- signature is checked against; `base_url` is merely where it is reached,
-- and a peer that moves host keeps its name.
CREATE TABLE federation_peers (
    id                  text PRIMARY KEY,
    address             text        NOT NULL UNIQUE,
    handle              text        NOT NULL,
    domain              text        NOT NULL,
    base_url            text        NOT NULL DEFAULT '',
    -- How we verify this peer. Its shape is the signing scheme's business;
    -- this schema only stores it.
    public_key          text        NOT NULL DEFAULT '',
    inbound_status      text        NOT NULL DEFAULT 'none'
                          CHECK (inbound_status IN ('none', 'pending', 'approved', 'blocked')),
    outbound_status     text        NOT NULL DEFAULT 'none'
                          CHECK (outbound_status IN ('none', 'pending', 'approved', 'blocked')),
    note                text        NOT NULL DEFAULT '',
    -- Delivery health. suspended_at set means the sender skips this peer
    -- until an administrator restarts it: a peer that has been failing for
    -- a day is a thing a person should look at, not a thing to keep
    -- hammering.
    suspended_at        timestamptz,
    first_failure_at    timestamptz,
    last_failure_at     timestamptz,
    last_failure_reason text        NOT NULL DEFAULT '',
    last_success_at     timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX federation_peers_outbound_idx ON federation_peers (outbound_status);
CREATE INDEX federation_peers_inbound_idx  ON federation_peers (inbound_status);

-- The outbox. `event_id` carries no foreign key on purpose: a deletion has
-- to be delivered AFTER the event is gone, and a foreign key would take the
-- tombstone with it.
CREATE TABLE federation_outbox (
    id              text PRIMARY KEY,
    peer_id         text        NOT NULL REFERENCES federation_peers (id) ON DELETE CASCADE,
    event_id        text        NOT NULL,
    -- The already-signed envelope, built when the row was enqueued. Signing
    -- at send time would re-sign on every retry, and a retry must deliver
    -- the same bytes the first attempt did.
    payload         bytea       NOT NULL,
    attempts        integer     NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (peer_id, event_id)
);
CREATE INDEX federation_outbox_due_idx ON federation_outbox (next_attempt_at);

-- What peers sent. A summary and a link; never a description.
CREATE TABLE remote_events (
    id                text PRIMARY KEY,
    peer_id           text        NOT NULL REFERENCES federation_peers (id) ON DELETE CASCADE,
    remote_id         text        NOT NULL,
    origin_domain     text        NOT NULL,
    canonical_url     text        NOT NULL,
    title             text        NOT NULL,
    search_text       text        NOT NULL DEFAULT '',
    is_online         boolean     NOT NULL DEFAULT false,
    is_in_person      boolean     NOT NULL DEFAULT false,
    loc_name          text        NOT NULL DEFAULT '',
    loc_address       text        NOT NULL DEFAULT '',
    loc_locality      text        NOT NULL DEFAULT '',
    loc_region        text        NOT NULL DEFAULT '',
    loc_postal_code   text        NOT NULL DEFAULT '',
    loc_country       text        NOT NULL DEFAULT '',
    loc_latitude      double precision,
    loc_longitude     double precision,
    starts_at         timestamptz NOT NULL,
    ends_at           timestamptz NOT NULL,
    timezone          text        NOT NULL,
    gathering_name    text        NOT NULL DEFAULT '',
    organization_name text        NOT NULL DEFAULT '',
    received_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (peer_id, remote_id)
);
CREATE INDEX remote_events_starts_at_idx ON remote_events (starts_at);
CREATE INDEX remote_events_geo_idx       ON remote_events (loc_latitude, loc_longitude);
CREATE INDEX remote_events_locality_idx  ON remote_events (loc_locality);

-- +goose Down
DROP TABLE remote_events;
DROP TABLE federation_outbox;
DROP TABLE federation_peers;
