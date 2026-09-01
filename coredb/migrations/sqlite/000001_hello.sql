-- +goose Up
-- The SQLite dialect of ../postgres/000001_hello.sql. Same tables, same
-- columns, same constraints; the differences are the ones SQLite forces:
--
--   * `timestamptz` -> `text`. SQLite has no date type. The api writes and
--     reads RFC3339 UTC strings (api/internal/store/sqlite), which sort
--     correctly as text, so `ORDER BY created_at DESC` still means what it
--     says.
--   * `now()` -> `strftime(...)`, producing the same
--     RFC3339 UTC shape.
--   * Foreign keys need `PRAGMA foreign_keys = ON` per connection; the
--     store sets it in its DSN rather than here, because a pragma set inside
--     a migration does not outlive the migration's connection.
--
-- Keep the two files in step: a change to one is a change to both.

CREATE TABLE users (
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
CREATE UNIQUE INDEX users_handle_idx ON users (handle);

CREATE TABLE sessions (
    id         text PRIMARY KEY,
    user_id    text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    created_at text NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    expires_at text NOT NULL
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE greetings (
    id         text PRIMARY KEY,
    author_id  text REFERENCES users (id) ON DELETE SET NULL,
    message    text NOT NULL,
    created_at text NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE INDEX greetings_created_at_idx ON greetings (created_at DESC);

-- +goose Down
DROP TABLE greetings;
DROP TABLE sessions;
DROP TABLE users;
