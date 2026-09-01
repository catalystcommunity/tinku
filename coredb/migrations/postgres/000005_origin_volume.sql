-- +goose Up
-- How much each originating organization sends.
--
-- The rate limit is enforced per PEER, because a peer is what a signature
-- identifies and what can be suspended. But a peer carries events from many
-- organizations, so "this peer is being throttled" does not say which
-- organization is responsible. This table answers that: an operator can see
-- which origin is filling the directory, and act on it rather than on the
-- whole peer.
--
-- Counted on ACCEPT, so it measures what actually landed rather than what
-- was attempted. An event refused for being over the allowance, or for
-- being malformed, is not counted here — the peer's rate_limited_total
-- already records the refusals.
CREATE TABLE federation_origin_stats (
    peer_id           text        NOT NULL REFERENCES federation_peers (id) ON DELETE CASCADE,
    -- The name as the peer sent it. This is display text from another
    -- instance, not a key into anything here, which is why it is the
    -- primary key's second half rather than a foreign key.
    organization_name text        NOT NULL,
    accepted_total    integer     NOT NULL DEFAULT 0,
    -- A fixed minute window, the same shape the peer's own limiter uses, so
    -- the two numbers on one screen mean the same thing.
    window_start      timestamptz,
    window_count      integer     NOT NULL DEFAULT 0,
    -- Owned by RecordOriginAccepted alone. NULL until something is actually
    -- stored: a row created by taking a rate allowance has received nothing
    -- yet, and stamping a time there tells an operator the opposite.
    last_received_at  timestamptz,
    -- This origin's own allowance. NULL uses the instance-wide origin
    -- limit. The limit exists at BOTH levels because a peer's allowance is
    -- shared: without a per-origin limit, one organization inside a peer
    -- can spend the whole budget and the peer's other organizations are
    -- refused for something they did not do.
    rate_limit_per_minute integer,
    -- Events refused for exceeding THIS origin's allowance, as distinct
    -- from the peer's.
    rate_limited_total integer NOT NULL DEFAULT 0,
    -- See federation_peers.rate_last_allowed: the consume is one statement,
    -- and its RETURNING sees the new row, so the granted amount is written
    -- down rather than recomputed.
    rate_last_allowed integer NOT NULL DEFAULT 0,
    PRIMARY KEY (peer_id, organization_name)
);
CREATE INDEX federation_origin_stats_volume_idx ON federation_origin_stats (accepted_total DESC);

-- +goose Down
DROP TABLE federation_origin_stats;
