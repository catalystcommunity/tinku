-- +goose Up
-- A peer's canonical linkkeys identity, replacing the placeholder
-- `public_key` column.
--
-- `address` (`handle@domain`) is a display name and a routing hint. It is
-- NOT what a delivery is checked against under the real (linkkeys
-- application-key) signing scheme: a handle can move to a different
-- account or be reused, so trusting it permanently would let approval
-- silently transfer to whoever holds the handle next. The four columns
-- added here are the peer's canonical identity — one linkkeys account, one
-- application, one application instance — resolved and stored the first
-- time a signed request from this address verifies, or set by an
-- administrator at approval time. See
-- api/internal/csilservices/federation.go's SetPeerStatus and
-- verifiedPeer, and docs/OPERATING.md's "Federation signing".
--
-- `public_key` was a placeholder for the development-only signing scheme,
-- which verified nothing and needed no key. The real scheme has several
-- keys per peer, each independently attested and each with its own
-- lifetime, so a single stored key could never have represented it; that
-- material is resolved on demand through this instance's RP, not stored on
-- the peer row at all.
ALTER TABLE federation_peers
    ADD COLUMN subject_user_id text NOT NULL DEFAULT '',
    ADD COLUMN subject_domain  text NOT NULL DEFAULT '',
    ADD COLUMN application_id  text NOT NULL DEFAULT '',
    ADD COLUMN instance_id     text NOT NULL DEFAULT '';

ALTER TABLE federation_peers DROP COLUMN public_key;

-- +goose Down
ALTER TABLE federation_peers ADD COLUMN public_key text NOT NULL DEFAULT '';

ALTER TABLE federation_peers
    DROP COLUMN subject_user_id,
    DROP COLUMN subject_domain,
    DROP COLUMN application_id,
    DROP COLUMN instance_id;
