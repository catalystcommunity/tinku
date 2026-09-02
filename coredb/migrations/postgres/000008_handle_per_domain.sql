-- +goose Up
-- A handle is unique WITHIN a domain, not across the directory.
--
-- 000001 made users.handle globally unique. That is wrong for a federated
-- directory: `ada@one.example` and `ada@two.example` are two different
-- people, and the whole point of showing a domain beside every name is that
-- two instances can hold the same name at once. The old index made the
-- second one impossible to store, and the failure arrived as a UNIQUE
-- constraint violation at login — from the far domain's point of view, an
-- unexplained internal error.
--
-- UserByHandle already asks for both parts, so nothing that reads changes.

DROP INDEX users_handle_idx;
CREATE UNIQUE INDEX users_domain_handle_idx ON users (linkkeys_domain, handle);

-- +goose Down
DROP INDEX users_domain_handle_idx;
CREATE UNIQUE INDEX users_handle_idx ON users (handle);
