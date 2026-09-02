-- +goose Up
-- The SQLite half of 000009. Same logical schema as
-- ../postgres/000009_offers_and_webhooks.sql, with the two differences this
-- dialect forces: `timestamptz` is RFC3339 UTC text (sqlite.timeLayout), and
-- `boolean` is an integer holding 0 or 1. Read the Postgres file for why
-- each table exists.

CREATE TABLE gathering_offers (
    id              text PRIMARY KEY,
    gathering_id    text NOT NULL REFERENCES gatherings (id)    ON DELETE CASCADE,
    organization_id text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    offered_by      text NOT NULL REFERENCES users (id)         ON DELETE CASCADE,
    note            text NOT NULL DEFAULT '',
    status          text NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending', 'accepted', 'declined', 'withdrawn')),
    created_at      text NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    resolved_at     text
);

CREATE UNIQUE INDEX gathering_offers_pending_idx
    ON gathering_offers (gathering_id, organization_id)
    WHERE status = 'pending';
CREATE INDEX gathering_offers_organization_idx ON gathering_offers (organization_id, status);
CREATE INDEX gathering_offers_gathering_idx ON gathering_offers (gathering_id, status);

CREATE TABLE webhooks (
    id              text    NOT NULL PRIMARY KEY,
    owner_kind      text    NOT NULL CHECK (owner_kind IN ('organization', 'gathering')),
    owner_id        text    NOT NULL,
    url             text    NOT NULL,
    secret          text    NOT NULL,
    scope           text    NOT NULL DEFAULT 'all'
                      CHECK (scope IN ('all', 'structure_only')),
    note            text    NOT NULL DEFAULT '',
    active          integer NOT NULL DEFAULT 1,
    failure_count   integer NOT NULL DEFAULT 0,
    last_status     integer,
    last_attempt_at text,
    created_at      text    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at      text    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE INDEX webhooks_owner_idx ON webhooks (owner_kind, owner_id);
CREATE INDEX webhooks_active_idx ON webhooks (active);

CREATE TABLE webhook_deliveries (
    id          text    NOT NULL PRIMARY KEY,
    webhook_id  text    NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
    payload     text    NOT NULL,
    attempts    integer NOT NULL DEFAULT 0,
    next_try_at text    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    last_error  text    NOT NULL DEFAULT '',
    created_at  text    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE INDEX webhook_deliveries_due_idx ON webhook_deliveries (next_try_at);

-- +goose Down
DROP TABLE webhook_deliveries;
DROP TABLE webhooks;
DROP TABLE gathering_offers;
