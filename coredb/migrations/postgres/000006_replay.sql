-- +goose Up
-- What a peer has already sent.
--
-- A signature does not expire, so it cannot stop a replay on its own: a
-- captured envelope resent a week later verifies perfectly, and applying it
-- would revert whatever the peer sent since — or re-delete an event they
-- republished.
--
-- Two things together fix that, and neither works alone:
--
--   * A batch_id the sender never repeats. Remembered here, so the second
--     arrival is refused.
--   * A freshness window on the batch's own sent_at. Without it this table
--     would have to remember every batch forever; with it, anything older
--     than the window is refused on its timestamp and its id can be
--     forgotten.
CREATE TABLE federation_seen_batches (
    peer_id  text        NOT NULL REFERENCES federation_peers (id) ON DELETE CASCADE,
    batch_id text        NOT NULL,
    seen_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (peer_id, batch_id)
);
CREATE INDEX federation_seen_batches_seen_at_idx ON federation_seen_batches (seen_at);

-- +goose Down
DROP TABLE federation_seen_batches;
