# Operating Tinku

This document lists the settings, the error codes, and the procedures for a
person who runs tinku.

## Settings

Every setting has an environment variable. A setting that an operator
changes for each run also has a `serve` flag. The flag has priority over
the environment variable.

### Core

`TINKU_ENV` defaults to **prod**. An instance you forget to configure is
therefore the safe one. Development login and the unsigned federation
scheme both need `TINKU_ENV=dev` (or `nonprod`) as well as their own switch,
so no single missing variable can leave a guard off.

| Environment variable | Flag | Default | Meaning |
| --- | --- | --- | --- |
| `TINKU_ENV` | `--env` | `prod` | The deployment: `dev`, `nonprod`, or `prod`. |
| `TINKU_API_PORT` | `--api-port` | `8080` | The port for CSIL-RPC and the auth callback. |
| `TINKU_OPS_PORT` | `--ops-port` | `9090` | The port for `/metrics`, `/healthz`, and `/readyz`. |
| `TINKU_DB_URI` | `--db-uri` | The Compose PostgreSQL | The database. Its scheme selects the backend. |
| `TINKU_MIGRATE_ON_BOOT` | `--skip-migrate` | `true` | Applies the migrations at start. |
| `TINKU_CORS_ORIGINS` | `--cors-origins` | `*` | The allowed origins. |
| `TINKU_ORIGIN_DOMAIN` | `--origin-domain-override` | *(derived)* | Forces this node's name. Only for a local run with no linkkeys. |

### This node's name

The origin domain is the domain half of every organization and gathering
address this node makes, and the name this node gives to a peer. You do not
set it. It comes from the linkkeys identity:

1. `TINKU_ORIGIN_DOMAIN`, if you set it.
2. `TINKU_LINKKEYS_DOMAIN`, else `TINKU_LINKKEYS_IDP_DOMAIN`.
3. `localhost`.

This instance is itself a linkkeys account. It must be, because a linkkeys
login needs both parties to know each other's domain and account. The domain
that owns this instance's account is therefore already the true answer to
"which node is this", and a second setting could only disagree with it.

The website hostname is not the answer. An instance at `tinkudomain.com`
whose linkkeys account is on `mylinkkeys.com` is
`something@mylinkkeys.com` to every peer, because that is the name a peer
can verify.

Configure linkkeys before the first organization or gathering is made. The
origin domain is written into each row and does not change afterwards. A
node that starts as `localhost` keeps `localhost` in every address it made.

### Session

| Environment variable | Flag | Default | Meaning |
| --- | --- | --- | --- |
| `TINKU_SESSION_TTL` | none | `720h` | How long a new session stays valid. |
| `TINKU_SESSION_COOKIE_SECURE` | `--session-cookie-insecure` | `true` | Sets the `Secure` attribute on the cookie. |
| `TINKU_SESSION_NONCE_SECRET` | none | A random value | Signs the login nonce. |

### Linkkeys

| Environment variable | Flag | Meaning |
| --- | --- | --- |
| `TINKU_LINKKEYS_IDP_DOMAIN` | `--linkkeys-idp-domain` | The identity domain that tinku trusts. |
| `TINKU_LINKKEYS_IDP_URL` | `--linkkeys-idp-url` | The base URL of the authorize page. |
| `TINKU_LINKKEYS_PKI_URL` | `--linkkeys-pki-url` | The base URL of the relying-party sidecar. |
| `TINKU_APP_CALLBACK_URL` | `--app-callback-url` | The absolute URL of `GET /auth/callback`. |
| `TINKU_LINKKEYS_DOMAIN` | `--linkkeys-domain` | The relying-party identity. It is the audience on an assertion. |
| `TINKU_LINKKEYS_PKI_API_KEY` | `--linkkeys-pki-api-key` | The relying-party API key. |
| `TINKU_LINKKEYS_PKI_ALLOW_INVALID_CERTS` | none | Skips TLS verification. Use only in a dev cluster. |
| `TINKU_POST_LOGIN_REDIRECT_URL` | `--post-login-redirect-url` | Where the callback sends the browser. |

Login works only when the first four are set. Without them,
`auth.begin-login` refuses and the reads continue to work.

### Development login

| Environment variable | Flag | Default | Meaning |
| --- | --- | --- | --- |
| `TINKU_DEV_AUTH_ENABLED` | `--dev-auth` | `false` | Makes `devauth.dev-login` available. |

`devauth.dev-login` makes a session without an identity assertion. Two
conditions must be true before the API registers the service:

1. `TINKU_DEV_AUTH_ENABLED` is true.
2. `TINKU_ENV` is `dev`, `nonprod`, or `test`.

If a condition is false, the API does not register the service. The
operation then gives transport status 2, the same answer as an operation
that does not exist.

## Error codes

`ServiceError.code` holds an application error code. Read this value in a
client. Do not read `ServiceError.message`.

| Code | Name | Meaning |
| --- | --- | --- |
| 1 | Invalid | The input failed validation. `field` names the input. |
| 2 | Unauthenticated | The caller has no session. |
| 3 | Forbidden | The caller has a session but no permission. |
| 4 | Not found | The resource does not exist, or the caller cannot know that it exists. `resource_type` names the kind. |
| 5 | Unavailable | A dependency is not configured, or it does not answer. |

Add a new code to the end of the list. Never change a code that exists. A
client maps the codes that it knows and treats the others as a general
failure.

## Administrators

An administrator is a global role. Administrators are the only people who
can delete an organization, and the only people who can delete an event that has
started. Nothing else needs the role.

The first administrator cannot be made through the API, because the
operation that grants the role needs the role. Make the first one from the
command line:

```sh
tinku admin grant ada@example.test
tinku admin list
tinku admin revoke ada@example.test
```

The address must belong to a person who signed in one time before. A user
row comes from a successful login.

The last administrator cannot be revoked. A deployment with no
administrator cannot delete an organization, and cannot make an
administrator again.

## Recurring events

A rule can make an unlimited number of events. The database holds a limited
number. Three bounds control this, in
`api/internal/csilservices/recurrence.go`:

| Bound | Value | Meaning |
| --- | --- | --- |
| `defaultHorizon` | 365 days | How far ahead a series materializes when nobody asks for more. |
| `maxHorizon` | 3 years | The most that `expand-event-series` will do. |
| `maxOccurrencesPerExpansion` | 500 | The row limit. A weekly rule and a yearly rule reach the same date with very different row counts. |

A read of a series moves its horizon forward. An open-ended series does not
run out of events one year after somebody made it.

Materialization is safe to repeat. A partial unique index on
`(series_id, starts_at)` makes a second call add only what is new.

To see how far a series goes, read `materialized_through` on the
`event_series` row.

## Federation

Federation is off. Switch it on only when you want this instance to
exchange events with another one.

| Environment variable | Flag | Default | Meaning |
| --- | --- | --- | --- |
| `TINKU_FEDERATION_ENABLED` | `--federation` | `false` | Switches the whole federation surface on. |
| `TINKU_FEDERATION_HANDLE` | `--federation-handle` | *(none)* | The local part of this instance's own account. |
| `TINKU_PUBLIC_BASE_URL` | `--public-base-url` | `https://` + origin domain | Where a reader reaches this instance. |
| `TINKU_FEDERATION_FAILURE_WINDOW` | *(none)* | `24h` | How long delivery to one peer may keep failing before that peer stops. |
| `TINKU_FEDERATION_POLL_INTERVAL` | *(none)* | `30s` | How often the sender drains the queue. |
| `TINKU_FEDERATION_RETRY_BASE` | *(none)* | `30s` | Wait before the first retry after a failure. Each further attempt doubles it. |
| `TINKU_FEDERATION_RETRY_MAX` | *(none)* | `1h` | The longest that wait becomes. |
| `TINKU_FEDERATION_RATE_DELAY` | *(none)* | `70s` | Wait after a peer refuses an event because of its rate limit. |

The retry settings are **this instance's policy**, not an agreement with a
peer. A peer can be a different implementation of this API, and nothing in
the protocol says how it wants to be retried. Set them to suit the peers you
actually talk to.

Both `TINKU_FEDERATION_ENABLED` and `TINKU_FEDERATION_HANDLE` are needed.
An instance with no account of its own cannot sign, so the service stays
unregistered and the wire answers "unknown service or op".

This instance is `TINKU_FEDERATION_HANDLE@origin-domain`. That address is
what a peer stores and shows for this instance. Under the real signing
scheme (see the next section), the address is a display name. The thing a
signature is actually checked against is the peer's canonical linkkeys
identity, not the address.

### Federation signing keys

Tinku signs its own event batches. It never asks linkkeys to sign one.
Linkkeys only attests that a public key belongs to one linkkeys account,
one application, and one application instance, for a short and renewable
time. Read `docs/application-keys.md` in the linkkeys repository for the
full protocol. This section covers the Tinku side of it.

There are two signing schemes:

- **The development scheme.** This is the default. It signs with no key at
  all and proves nothing. It refuses to run outside `TINKU_ENV=dev` or
  `nonprod`, so it cannot be the scheme running in production by accident.
- **The real scheme.** Tinku holds its own Ed25519 keys and signs with
  them. A peer checks a batch against a linkkeys attestation of the signing
  key, fetched through this instance's own RP. Set
  `TINKU_FEDERATION_SIGNING_KEYS` to switch to it.

#### Generate a keyring

```sh
tinku federation generate-keys --address=my-handle@my-domain.example
```

This prints a JSON secret to stdout and the public keys to stderr. The
command generates three keys by default. Three keys is the number that
lets two keys revoke the third — the design of application keys says a key
may never sign its own revocation, so two keys can never revoke each
other. Do not run this command with `--count` below 2.

The JSON on stdout is a secret. It holds private key material. Put it
straight into your secret store. Do not commit it, log it, or leave it in
shell history.

#### Enroll the keys

`tinku federation generate-keys` only makes the keys. It does not enroll
them. Enrollment (`Account/enroll-application-instance`) needs the account
owner to log in to the linkkeys home domain directly and approve the
application instance. This is a one-time, human step outside this
command's reach — Tinku cannot do it for you.

After enrollment, linkkeys gives you an `instance_id`. Record it: it goes
into `TINKU_FEDERATION_INSTANCE_ID` below.

#### Configure the real scheme

| Environment variable | Meaning |
| --- | --- |
| `TINKU_FEDERATION_SIGNING_KEYS` | The JSON secret from `tinku federation generate-keys`. Empty selects the development scheme. |
| `TINKU_FEDERATION_SUBJECT_USER_ID` | This instance's linkkeys account UUID — the account that enrolled it. |
| `TINKU_FEDERATION_APPLICATION_ID` | The application id linkkeys knows this as. Defaults to `tinku`. |
| `TINKU_FEDERATION_INSTANCE_ID` | The application-instance id linkkeys assigned at enrollment. |
| `TINKU_FEDERATION_RP_TCP_ADDRESS` | This instance's own regular RP: where `Rp/resolve-application-keys` is asked for a PEER's keys. |
| `TINKU_FEDERATION_RP_FINGERPRINTS` | Comma-separated pinned TLS fingerprints of that RP. |
| `TINKU_FEDERATION_RP_API_KEY` | The RP API key. A secret — treat it the same way as `TINKU_FEDERATION_SIGNING_KEYS`. |
| `TINKU_FEDERATION_HOME_DOMAIN_TCP_ADDRESS` | This instance's OWN home domain, for renewing its own keys' attestations. Optional: without it, renew by hand. |
| `TINKU_FEDERATION_HOME_DOMAIN_FINGERPRINTS` | Comma-separated pinned TLS fingerprints of that home domain. |

`TINKU_FEDERATION_SUBJECT_USER_ID`, `TINKU_FEDERATION_APPLICATION_ID` and
`TINKU_FEDERATION_INSTANCE_ID` are all required once
`TINKU_FEDERATION_SIGNING_KEYS` is set. `serve` refuses to start
federation without them.

The RP settings are required too: without them, this instance cannot
verify anything a peer sends it. The home-domain settings are optional:
without them, this instance still signs and verifies, but somebody has to
renew its own keys' attestations by hand before they expire.

#### Renewal

Once `TINKU_FEDERATION_HOME_DOMAIN_TCP_ADDRESS` is set, this instance
renews each of its own keys' attestations on its own, roughly every 15
minutes, but only calls linkkeys when a key's current attestation has used
up more than half of its life. Asking earlier wastes a round trip: the
home domain returns the same stored bytes and does not sign again below
half-life. Asking any later risks a gap where a peer has no current proof
for that key.

An expired attestation is not a revocation. It means a peer has no current
proof for that key. A renewed attestation makes the same key work again.

#### The batch signature context

Every event batch Tinku signs carries a fixed signature context that Tinku
itself defines: `tinku-federation-batch-v1`. This is not a linkkeys tag. A
signature made under this context can never be presented as an
authentication request, and a signature made for any other purpose can
never be presented as a batch. This is what stops a captured federation
message from being replayed as something else.

#### A peer's canonical identity, not its handle

A `Peer` row records more than an address. It also records the peer's
canonical linkkeys identity: `subject_user_id`, `subject_domain`,
`application_id`, `instance_id`. This is what a delivery is actually
checked against — never the address alone, because a handle can move to a
different linkkeys account, or be reused.

Approving a peer's inbound direction requires this identity.
`federation/set-peer-status` refuses to approve inbound for a peer with no
identity recorded, unless the same request supplies one. Give all four
identity fields together, or give none: a partial identity is refused.

A peer's identity is captured once, the first time a signed request from
it verifies, or set by an administrator by hand. It is never replaced by
a later signed request from the same address. If that address later
belongs to a different account, the new holder's batches are refused: they
do not hold the key material the OLD, approved identity is attested
under.

### Both sides must agree

Each peer row holds two statuses, and they move independently:

| Status | Meaning |
| --- | --- |
| `inbound_status` | Do we accept what this peer sends us. |
| `outbound_status` | Do we deliver our events to this peer. |

Both begin at `none`. Approving one does not approve the other. An
administrator on each side must approve their own direction, so no instance
is listed somewhere it did not choose, and no directory fills with events
from somebody it did not admit.

### Which events are published

Three levels answer. **The most specific level that is both set and allowed
wins:**

```
gathering          if the instance allows a gathering to change it
organization       if the instance allows an organization to change it
instance default   otherwise
```

Each level can also say nothing. "Not set" is a real third answer, not the
same as "no": it means that level defers to the level above.

A gathering can have several owning organizations. Among them, an explicit
**no** beats an explicit **yes**. Publishing somebody's events is the act
that cannot be taken back, so the most restrictive owner decides.

An instance can withdraw the right to change it at either level. A level
that may not change it is skipped, and the API refuses a caller that tries.
The refusal is deliberate: a setting that is accepted and then ignored is
worse than one that is refused.

A **deletion always travels**, whatever the setting says. An event that was
published and is then deleted has to be removed from the peer, or it stays
on their site forever.

### Instance settings

These live in the database. An administrator changes them while the service
runs, and every replica sees the same value. Read them at **Federation** in
the web client, or with `admin/get-instance-settings`.

| Setting | Default | Meaning |
| --- | --- | --- |
| `publish_default` | `in` | What a gathering publishes when nothing below says otherwise. |
| `organization_override_allowed` | `true` | May an organization change it. |
| `gathering_override_allowed` | `true` | May a gathering change it. |
| `retention_days` | `365` | How long a directory keeps what a peer sent, counted from when the event ends. Zero keeps everything. |
| `peer_rate_limit_per_minute` | `60` | How many events one peer may have accepted each minute. Zero means no limit. |
| `origin_rate_limit_per_minute` | `20` | The same, for one organization inside a peer. Zero means no limit. |

The publish default is `in` because an instance that has switched federation
on and approved a directory has already made the interesting choice. Set it
to `out` if you want each organization or gathering to ask.

### Rate limiting a peer

A peer that loses control of itself must not fill this instance's
directory. Each peer has an allowance in **events a minute**. A batch that
goes over has the rest of its events refused, and the receipt tells the
sender how many, so it can slow down instead of retrying.

The count is a fixed window: it resets when the minute changes. A peer can
therefore send a double allowance across a window boundary. This is
accepted, because a limiter that needs background work can itself fall
behind.

Give one peer its own allowance at **Federation**, or with
`federation/set-peer-rate-limit`. An empty value restores the instance-wide
limit. A limit of zero for one peer means no limit for that peer, which is
how you admit a trusted bulk publisher without raising the ceiling for
everybody.

`rate_limited_total` on the peer counts every event ever refused this way.
Watch it: a peer that has gone quiet and a peer that is being throttled look
the same otherwise.

### Two limits, not one

A peer's allowance is shared by every organization inside it. With only a
peer limit, one organization can use the whole allowance, and that peer's
other organizations are then refused for something they did not do.

So there are two limits, and an event must pass both:

1. The **peer** limit, taken for the whole batch.
2. The **organization** limit, taken for each organization in the batch.

Set the organization limit below the peer limit. At or above it, the peer's
own check refuses the batch first and the organization limit never applies.
The defaults are 60 and 20, so three organizations can each work at full
rate before the peer limit binds.

A **deletion is never refused for rate**. A refused deletion would leave an
event on this directory that its origin has removed, which is worse than
accepting one more row.

Throttle one organization at **Federation**, under **Where deliveries come
from**, or with `federation/set-origin-rate-limit`. An empty value restores
the instance-wide limit.

### Which organization is sending the volume

The limit applies to a **peer**, because a peer is what a signature
identifies and what you can suspend. But a peer carries events from many
organizations, so a throttled peer does not tell you which of them caused
it.

**Federation**, then **Where deliveries come from**, lists each originating
organization, busiest first: what is held from it, what it has ever sent,
what it has sent this minute against the peer's limit, and whether that peer
is throttled or stopped. Over the API this is
`federation/list-origin-volume`; narrow it to one peer with `peer_id`.

The counts are taken when an event is **accepted**, so they measure what
landed. Refusals are counted on the peer, not here.

An organization name is display text the peer sent. It is not a record on
this instance, so two peers can use the same name and each is listed
separately, under its own peer.

## Names are not identities

A name does not identify anything across domains. Two instances can each
have an organization called "Loud Chess Club", and a directory can hold
both. The **domain** is the identity.

Every record the API returns therefore carries an `origin` block:

| Field | Meaning |
| --- | --- |
| `domain` | The domain that owns this record. |
| `is_external` | True when that domain is not this instance's own. |
| `peer_address` | The peer it arrived from, when it is external. |

The web client always shows the domain, and marks an external record with a
badge and with words. A reader therefore never has to think to ask whether
a familiar name is the one they know.

`is_external` is derived, never sent. A federated event does not say where
it is from: the delivery came in, and the verified signature says which
domain it came from. A field inside the message could say anything the
sender liked.

### What travels

A summary and a link. Title, times, timezone, online or in person,
location, and the names of the gathering and organization. Never a
description, and never an attendee. A reader who wants more follows the
link to the instance that owns the event.

A deletion travels as a message of its own. A directory cannot tell an event
that was deleted from one that stopped being mentioned, so silence would
leave a deleted event on another site.

### A refused event is not a lost event

A peer answers a delivery with a receipt. The receipt is what decides what
happens next, not the fact that the message arrived:

| Receipt | What the sender does |
| --- | --- |
| Accepted | Removes the delivery from the queue. |
| Refused for rate | **Keeps it** and tries again after `TINKU_FEDERATION_RATE_DELAY`. |
| Refused for any other reason | Removes it and logs an error. Waiting cannot make a malformed event acceptable. |

A rate refusal does not count an attempt and does not start a failure run.
The peer answered, so it is healthy — only busy. Counting it would grow the
backoff and march a working peer toward suspension.

### When a peer stops answering

Delivery retries with a growing wait. When the current run of failures has
lasted longer than `TINKU_FEDERATION_FAILURE_WINDOW`, the peer is
**suspended** and the sender stops choosing it.

Nothing lifts a suspension on its own. Look at the reason on the peer, fix
the cause, then restart the peer:

- In the web client: **Federation**, then **Restart delivery**.
- Over the API: `federation/resume-peer`.

A restart clears the failure run and makes every waiting delivery due at
once.

### Procedure: connect two instances

1. On instance B, read the address: `federation/federation-identity`. Under
   the real signing scheme, this also returns B's canonical identity
   (`subject_user_id`, `subject_domain`, `application_id`, `instance_id`).
2. On instance A, add B as a peer with B's address and base URL. A's
   outbound status becomes `pending`.
3. On instance A, read `federation/federation-identity` the same way, for
   B's administrator to record.
4. On instance B, approve A's inbound status
   (`federation/set-peer-status`), supplying A's identity in the same
   request. Approving inbound with no identity, and none already recorded,
   is refused — see "Federation signing keys" above.
5. On instance A, set B's outbound status to `approved`.

Instance A now delivers its events to B, and B lists them.

Under the development scheme, steps 3 and 4's identity fields do not
matter: the development Verifier ignores them. Approving inbound still
needs SOME identity recorded, even a placeholder one, because the rule
above does not know which scheme is running.

## Replay

A signature does not expire. A delivery captured today and sent again next
week still verifies, so the signature alone cannot stop a replay. Applying
one would undo whatever the peer sent in between, and a replayed deletion
would remove an event the peer had published again.

Two rules stop it, and neither works alone:

- Each delivery carries a **batch id** the sender never repeats. The
  receiver remembers it and refuses the second arrival.
- Each delivery carries the time the sender made it. A delivery more than
  **one hour** from the receiver's clock, in either direction, is refused.
  This bounds how long ids must be remembered.

A batch that arrived but was refused for rate is **forgotten again**, so an
honest sender can send it once the peer's window has passed.

Remembered ids are swept on the same hourly timer as the retention sweep.

If a peer's deliveries are all refused as stale, compare the two clocks
first. An hour is generous for normal skew.

## Times

Every timestamp column holds UTC, on both backends. A `timezone` column
holds an IANA name. It is used to display an instant, and to interpret what
somebody typed. It is never an offset put into a stored time.

The one column that is not an instant is `event_series.start_time`. It holds
a local clock time such as `19:00`, and it is read together with
`event_series.timezone`. The pair is the recurrence rule, not a time. The
rule makes UTC instants in the `events` table, and those obey the same rule
as all other times. This is what keeps a 19:00 meeting at 19:00 local when
daylight saving moves.

To show a stored instant in a person's own zone, convert it as it goes out.
Never convert it as it comes in.

## A difference between the two backends

The two backends answer the same for every query but one. A search that
filters by place folds the stored value with SQL `lower()`. SQLite folds
ASCII only; PostgreSQL folds all of Unicode. A place name with a non-ASCII
capital letter therefore matches with case sensitivity on SQLite and
without it on PostgreSQL.

Free-text search is not affected. That column is folded in Go, which is
correct for all of Unicode, and both backends compare the same bytes.

SQLite is for local runs and for tests. Use PostgreSQL where the difference
matters.

## Transport statuses

A transport status is not an application error. It reports that the
operation did not run.

| Status | Name | Cause |
| --- | --- | --- |
| 0 | Ok | The operation ran. Read the variant. |
| 1 | Malformed envelope | The API could not read the request. |
| 2 | Unknown service or op | This API does not have the operation. |
| 6 | Internal | The operation failed for a reason the API does not disclose. |

## Procedures

### Find why a pod is not ready

```sh
curl -i http://<pod>:9090/readyz
```

The body gives the cause. These are the causes:

- `applying database migrations` — the migration is in progress.
- `database migrations are pending` — the schema is behind the binary. Run
  the migration step.
- `cannot reach the database: ...` — the database does not answer.
- `dependency check failed: ...` — the database stopped answering after the
  service became ready.

### Apply the migrations as a separate step

Set `TINKU_MIGRATE_ON_BOOT=false` on the service. Run the migrations from
a job:

```sh
tinku migrate --db-uri="$TINKU_DB_URI" up
```

The service waits at step 3 of its start sequence until the job finishes.
It does not fail. Two replicas that both apply migrations are also safe: on
PostgreSQL the second replica waits for an advisory lock, then finds
nothing to do.

### Roll the migrations back

```sh
tinku migrate --db-uri="$TINKU_DB_URI" down
```

This command removes every migration. It destroys the data. Make a backup
first.

### Find the cause of a failed login

Look for these lines in the API log:

| Log message | Cause |
| --- | --- |
| `token would not decrypt` | The token is not for this relying party. |
| `assertion would not verify` | The signature is not correct for the IDP domain. |
| `assertion audience mismatch` | The assertion is for a different service. |
| `nonce rejected` | The login is more than 10 minutes old, or it did not start here. |

A `nonce rejected` message that happens after a restart, or on some
requests only, means that `TINKU_SESSION_NONCE_SECRET` is not set. Each
process then makes its own secret.

### Read the metrics

```sh
curl http://<pod>:9090/metrics | grep tinku_
```

The `outcome` label on `tinku_rpc_requests_total` separates the three
results:

- `ok` — a typed reply.
- `service_error` — a declared application error.
- Any other value — the transport status name.

A high `service_error` rate on `create-greeting` means that clients call it
without a session. A high `unknown_service_or_op` rate means that a client
is newer than this API.
