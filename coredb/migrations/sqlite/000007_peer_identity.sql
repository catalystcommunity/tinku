-- +goose Up
-- The SQLite dialect of ../postgres/000007_peer_identity.sql. See that
-- file for why: a peer's canonical linkkeys identity replaces the
-- placeholder `public_key` column.
ALTER TABLE federation_peers ADD COLUMN subject_user_id text NOT NULL DEFAULT '';
ALTER TABLE federation_peers ADD COLUMN subject_domain  text NOT NULL DEFAULT '';
ALTER TABLE federation_peers ADD COLUMN application_id  text NOT NULL DEFAULT '';
ALTER TABLE federation_peers ADD COLUMN instance_id     text NOT NULL DEFAULT '';

ALTER TABLE federation_peers DROP COLUMN public_key;

-- +goose Down
ALTER TABLE federation_peers ADD COLUMN public_key text NOT NULL DEFAULT '';

ALTER TABLE federation_peers DROP COLUMN subject_user_id;
ALTER TABLE federation_peers DROP COLUMN subject_domain;
ALTER TABLE federation_peers DROP COLUMN application_id;
ALTER TABLE federation_peers DROP COLUMN instance_id;
