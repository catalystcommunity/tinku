-- +goose Up
-- The whole tinku schema in one migration: the users a linkkeys assertion
-- resolves to, the sessions minted for them, and the greetings they leave.
--
-- Ids are `text` holding a ULID minted by the api (api/internal/store.NewID),
-- not a database-generated uuid. The same logical schema also runs on SQLite
-- (see ../sqlite/000001_hello.sql), and only Postgres has a usable id
-- generator, so id minting lives in the one place both dialects share: Go.

CREATE TABLE users (
    id               text PRIMARY KEY,
    linkkeys_domain  text        NOT NULL,
    linkkeys_user_id text        NOT NULL,
    handle           text        NOT NULL,
    display_name     text        NOT NULL DEFAULT '',
    kind             text        NOT NULL DEFAULT 'human'
                       CHECK (kind IN ('human', 'system')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (linkkeys_domain, linkkeys_user_id)
);
CREATE UNIQUE INDEX users_handle_idx ON users (handle);

-- Sessions are tinku's own. Linkkeys verifies identity; it does not issue
-- the session. Only the SHA-256 of the cookie value is stored, so a database
-- read cannot impersonate a live session.
CREATE TABLE sessions (
    id         text PRIMARY KEY,
    user_id    text        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash text        NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- Greetings are the hello-world domain. author_id is nullable so a greeting
-- outlives the user who left it; the listing renders those as anonymous.
CREATE TABLE greetings (
    id         text PRIMARY KEY,
    author_id  text        REFERENCES users (id) ON DELETE SET NULL,
    message    text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX greetings_created_at_idx ON greetings (created_at DESC);

-- +goose Down
DROP TABLE greetings;
DROP TABLE sessions;
DROP TABLE users;
