-- +goose Up
-- Whether a delivery carries the record or only a pointer to it.
--
-- The default is off, and off is the safe one: a delivery goes to a URL this
-- server has no say over. It is the owner's own information to send, so the
-- switch exists — the client warns and asks them to accept it, and this
-- column is where that choice lives.

ALTER TABLE webhooks ADD COLUMN include_details integer NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE webhooks DROP COLUMN include_details;
