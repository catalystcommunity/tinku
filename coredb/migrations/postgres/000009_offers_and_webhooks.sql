-- +goose Up
-- Two additions that are both about ownership changing hands or being
-- reported on: an offer of a gathering to an organization, and the webhooks
-- an owner sets to hear about what happens under them.

-- An offer is a two-sided move. It exists as a row because neither side can
-- complete it alone: the gathering's owner makes it, and an owner of the
-- organization answers it. Without a row there is nothing for the second
-- side to find.
CREATE TABLE gathering_offers (
    id              text PRIMARY KEY,
    gathering_id    text        NOT NULL REFERENCES gatherings (id)    ON DELETE CASCADE,
    organization_id text        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    offered_by      text        NOT NULL REFERENCES users (id)         ON DELETE CASCADE,
    note            text        NOT NULL DEFAULT '',
    status          text        NOT NULL DEFAULT 'pending'
                      CHECK (status IN ('pending', 'accepted', 'declined', 'withdrawn')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    resolved_at     timestamptz
);

-- One pending offer of a gathering to an organization at a time. A second
-- offer while one is open would give the receiving side two things to
-- answer and one outcome.
CREATE UNIQUE INDEX gathering_offers_pending_idx
    ON gathering_offers (gathering_id, organization_id)
    WHERE status = 'pending';
CREATE INDEX gathering_offers_organization_idx ON gathering_offers (organization_id, status);
CREATE INDEX gathering_offers_gathering_idx ON gathering_offers (gathering_id, status);

-- A webhook hangs off exactly one level: an organization or a gathering.
-- The pair (owner_kind, owner_id) is that level. It is not two nullable
-- foreign keys, because the two are never both set and a receiver of the
-- delivery cares which kind it is.
CREATE TABLE webhooks (
    id              text PRIMARY KEY,
    owner_kind      text        NOT NULL CHECK (owner_kind IN ('organization', 'gathering')),
    owner_id        text        NOT NULL,
    url             text        NOT NULL,
    -- The HMAC-SHA256 key deliveries are signed with. Minted here, returned
    -- once, never read back over the API.
    secret          text        NOT NULL,
    scope           text        NOT NULL DEFAULT 'all'
                      CHECK (scope IN ('all', 'structure_only')),
    note            text        NOT NULL DEFAULT '',
    active          boolean     NOT NULL DEFAULT true,
    failure_count   integer     NOT NULL DEFAULT 0,
    last_status     integer,
    last_attempt_at timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX webhooks_owner_idx ON webhooks (owner_kind, owner_id);
CREATE INDEX webhooks_active_idx ON webhooks (active);

-- A delivery is a row before it is a request. A webhook that fires inside
-- the transaction that changed something would make the change wait on
-- somebody else's server, and would lose the notification entirely if that
-- server were slow. The sender drains this table instead.
CREATE TABLE webhook_deliveries (
    id           text PRIMARY KEY,
    webhook_id   text        NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
    payload      text        NOT NULL,
    attempts     integer     NOT NULL DEFAULT 0,
    next_try_at  timestamptz NOT NULL DEFAULT now(),
    last_error   text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX webhook_deliveries_due_idx ON webhook_deliveries (next_try_at);

-- +goose Down
DROP TABLE webhook_deliveries;
DROP TABLE webhooks;
DROP TABLE gathering_offers;
