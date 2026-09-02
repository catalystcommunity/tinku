# Tinku

Tinku is a federated directory of gatherings. A person signs in with
linkkeys, starts an **organization** or a **gathering**, and schedules
**events**. An event happens one time, or a rule makes it happen again and
again.

Tinku also shows the local development flow these repositories use for a Go
API with a SolidJS client:

- A **CSIL** schema is the contract. All API code comes from it.
- The API speaks **CSIL-RPC** (CBOR envelopes over `POST /csil/v1/rpc`).
- The API is a **CLI**. The `serve` command runs it as a service.
- **Goose** applies the migrations. The service is not ready until the
  schema is current.
- The store has **two backends**: PostgreSQL and SQLite.
- **Linkkeys** gives the identity. Tinku gives the session.
- **Prometheus** metrics are on a separate port.
- The client is **translatable** and **accessible**. No component holds a
  string that a person reads.

## The domain

Six ideas, and one rule that cuts across all of them.

| Idea | What it is |
| --- | --- |
| **Organization** | A set of people that can hold ownership. Address: `slug@domain`. |
| **Gathering** | What people join. Every event is under one. |
| **Event** | One dated thing. It is online, in person, or both. |
| **Event series** | A rule that makes events. |
| **Role** | Owner, organizer, presenter, member, administrator. |
| **Search** | One query over all four kinds, by text, by place, and by time. |

**An occurrence of a series is an event.** A series holds the rule. The
occurrences are ordinary rows in the same table, with a `series_id`.
Attendance, the start-time lock, and search work on one shape. No code
must ask if the event in hand came from a rule.

A gathering has many owners. An owner is a person or an organization. A
person owns a gathering directly, or because they own an organization that
owns it. This is why organizations exist: ownership must live longer than
any one person.

A rule looks like this:

| In words | The rule |
| --- | --- |
| every second Thursday | monthly, ordinal 2, Thursday |
| every fourth Wednesday | monthly, ordinal 4, Wednesday |
| first Saturday of the quarter | quarterly, ordinal 1, Saturday |
| every other Thursday | weekly, interval 2, Thursday |

One gathering holds many rules. The occurrences of all of them show in one
listing.

A series keeps its local clock time. It stores an IANA timezone and a
`19:00`, not a UTC offset. The second Thursday at 19:00 stays 19:00 when
daylight saving moves.

### The start-time lock

When `starts_at` goes by, the event freezes:

- No field can change.
- No person can start or stop attending.
- The description is not sent, to any caller.

Only an administrator can then delete it. This is the one rule that reaches
every layer: `perms.go` decides it, `toEvent` withholds the description, and
the client says so in words.

### Who can do what

| Role | Scope | Powers |
| --- | --- | --- |
| Administrator | Global | Delete an organization. Delete an event that started. |
| Owner | Organization | Edit the organization and its roster. |
| Owner | Gathering | Edit it, its owners, and every event under it. |
| Organizer | Event or series | Edit that event or series. |
| Presenter | Event or series | The same as a member. The role shows who presents. |
| Member | Gathering | Mark attendance on its events. |

An organizer of a series organizes each occurrence of it.

A person joins a gathering, then answers its events one at a time. There is
no way to attend a series: nobody can promise to attend every future
instance of a rule that does not end.

The hello-world greetings domain stays. It is the smallest end-to-end path
through every layer, and the tests use it as a canary.

## Quickstart

You need Go 1.26 or later and Node 22 or later (CI uses 26). You do not
need Docker, PostgreSQL, or a linkkeys server for this walkthrough.

**1. Start the API.** It applies the migrations to a SQLite file,
`local.db`, and then listens.

```sh
./tools.sh serve-local
```

- API: <http://localhost:5080>
- Ops: <http://localhost:9090/metrics>, `/healthz`, `/readyz`

The client and the API are two ports on purpose, and the browser calls each
of them: the page comes from 8080 and its API calls go to 5080. Forwarding
this to another machine therefore means forwarding BOTH — see "The client is
a different origin" in `docs/OPERATING.md`.

The log says `DEV AUTH ENABLED`. This command gives the API `--env=dev` and
`--dev-auth`, which is what makes step 3 possible. No other command does.

**2. Start the web client**, in a second terminal. The command installs the
client's dependencies the first time you run it.

```sh
./tools.sh dev-web
```

Open <http://localhost:8080>.

**3. Sign in as `devadmin`, at whatever domain you like.** Select **Sign
in**, give the handle `devadmin`, and keep the domain the form suggests or
type your own — `example.test`, `two.example`, anything. In a development
environment that handle holds the administrator role at every domain, so
you can see the whole application at once, and two instances on two domains
each have an administrator without a second command.

There is no password. The client calls `devauth.dev-login`, which makes the
person the first time their handle is used and gives back a session with no
identity assertion behind it. That is why the operation exists only in a
dev or nonprod environment, and why the API refuses it anywhere else.

Any other handle works too and makes an ordinary account: `ada`, `devuser`,
your own name. Use one to see what somebody without the role sees. Two
handles at two domains are two different people, which is the rule the
whole directory rests on.

**4. Make something.**

- **Orgs → Start an organization.** The organization gets an address from
  its name: `forge-utah@localhost`. `localhost` is this instance's origin
  domain. Another instance shows its own.
- **Gatherings → Start a gathering.** Open it. It has two forms: one
  schedules a single event, and one schedules a rule.
- In **Schedule a recurring event**, keep the defaults — second Thursday,
  19:00, America/Denver — give a title and a first possible date, then
  select **Schedule**. Tinku writes the occurrences the rule makes, one
  year ahead. Each one is an ordinary event that a person attends by
  itself.
- Times show in the event's own timezone, never in yours. That is the
  point: an event is where it is.

**5. The administrator role.** Only an administrator can delete an
organization, or an event that has started. Signing in as `devadmin`
(step 3) is the short way to hold it. To give it to an account you have
already signed in as:

```sh
./tools.sh admin grant ada@example.test
./tools.sh admin list
```

The role is read from the database on every request, so it takes effect at
once. Reload the page to see the controls that it adds. `tinku admin` is
also the only way to make the FIRST administrator of a real deployment,
where development sign-in does not exist: granting the role over the API
needs the role.

The person has to sign in one time before you grant the role. The command
answers `nobody here has the address ...` when they have not. It also
refuses to revoke the last administrator, so an instance cannot lose the
role completely.

**6. Start again from nothing**, at any time:

```sh
rm -f local.db
```

### What to try after that

| Rule | How to see it |
| --- | --- |
| An event freezes when it starts. | Schedule a single event two minutes ahead. When the time goes by, the description, the edit, and **Attend** all go away — for everybody. |
| A rule can make nothing. | Ask for the fifth Thursday, or the 31st. A month without one makes no event. It is never moved to the nearest day. |
| Federation is off. | The publish control on a gathering says `Decided by this instance`. See the federation section of `docs/OPERATING.md` to switch it on. |

## Start the full stack

```sh
./tools.sh dev            # dev-up is the same command
```

This command starts PostgreSQL, the API, and the web client with Docker
Compose. It waits until the API reports ready, makes the development
accounts, and then prints the addresses. It does not hold the terminal.

- Web client: <http://localhost:8080>
- API: <http://localhost:5080>
- Ops: <http://localhost:9090>
- PostgreSQL: `postgresql://tinku:devpass123@localhost:5432/tinku_db`

Sign in as **devadmin**, which holds the administrator role, or **devuser**,
which holds nothing. The domain is `example.test`. There is no password:
development sign-in carries no credential.

```sh
./tools.sh dev-logs       # follow the logs; dev-logs api for one service
./tools.sh dev-down       # stop it. The database survives.
./tools.sh dev-down --volumes   # stop it and drop the database
```

## Repository layout

| Path | Contents |
| --- | --- |
| `csil/` | The CSIL schema. This is the contract. |
| `coredb/` | The goose migrations, for both dialects. A Go module. |
| `api/` | The Go API. A Go module. |
| `webapp/` | The SolidJS client. |
| `clients/` | Generated standalone clients for other applications. |
| `version/` | The version this repository releases. |
| `website/` | The marketing site. PySocha builds it; Caddy serves it. |
| `.reactorcide/` | CI. One Python plugin holds every job body. |

### Inside `api/`

| Path | Contents |
| --- | --- |
| `main.go`, `cmd/` | The CLI: `serve`, `migrate` and `admin`. |
| `internal/csil/` | Generated. Do not edit. |
| `internal/csilservices/` | The service implementations, the permission model, and the recurrence engine. |
| `internal/server/` | The HTTP carrier, the dispatch table, the session. |
| `internal/store/` | The store interface and its two backends. |
| `internal/linkkeys/` | The relying-party client and the login nonce. |
| `internal/metrics/` | The collectors and the ops listener. |
| `internal/transport/` | Vendored. The CSIL-RPC reference transport. |

## The commands

| Command | Result |
| --- | --- |
| `./tools.sh gen` | Generates all code from `csil/tinku.csil`. |
| `./tools.sh build` | Builds the API, coredb, and the web client. |
| `./tools.sh test` | Runs all tests, on SQLite. |
| `./tools.sh test-pg` | Runs the Go tests against the Compose PostgreSQL. |
| `./tools.sh lint` | Runs `go vet` and `tsc --noEmit`. |
| `./tools.sh migrate up\|down\|status` | Applies or reports the migrations. |
| `./tools.sh serve-local` | Runs the API on SQLite. |
| `./tools.sh dev` (or `dev-up`) | Starts the Docker Compose stack, waits for it, and seeds the development accounts. |
| `./tools.sh dev-logs [service]` | Follows the stack's logs. |
| `./tools.sh dev-down [--volumes]` | Stops the stack. `--volumes` drops the database. |
| `./tools.sh dev-seed` | Makes `devadmin` and `devuser`. Idempotent. |
| `./tools.sh admin list\|grant\|revoke` | The global administrator role. |
| `./tools.sh dev-web` | Starts the Vite development server. |
| `./tools.sh build-images` | Builds the container images. |
| `./tools.sh site build` | Builds the marketing site in `website/`. |

## How a request moves

```
SolidJS component
  -> generated client        webapp/src/gen/client.async.gen.ts
  -> HTTP transport          webapp/src/lib/httpTransport.ts
  ~~ POST /csil/v1/rpc, one CBOR envelope ~~
  -> HTTP carrier            api/internal/server/server.go
  -> dispatch table          api/internal/server/dispatch.go
  -> service implementation  api/internal/csilservices/
  -> store                   api/internal/store/{postgres,sqlite}
```

The envelope holds the service name and the operation name. The HTTP path
holds no routing information.

## The three outcomes

A CSIL-RPC call has three possible results. Keep them separate.

| Result | Transport status | Variant | Cause |
| --- | --- | --- | --- |
| A typed reply | 0 | The response type | The operation succeeded. |
| An application error | 0 | `ServiceError` | The operation refused. |
| A transport failure | Not 0 | None | The operation did not run. |

The HTTP status is always 200 for a request the carrier can read. An HTTP
status other than 200 means a failure below CSIL-RPC.

An application error is a declared arm of the operation. Only an operation
with a `/ ServiceError` arm in the schema can return one. Read the error
codes in `api/internal/csilservices/errors.go`.

## The two backends

The scheme of the database URI selects the backend AND the migration tree.

```sh
./tools.sh serve-local                                  # sqlite:./local.db
tinku serve --db-uri=postgresql://user:pass@host/db    # PostgreSQL
tinku serve --db-uri=sqlite:./dev.db                   # SQLite
```

PostgreSQL is the backend for Docker Compose and for a deployment. SQLite
needs no server, so it is the backend for a first run and for the tests.
The SQLite driver is `modernc.org/sqlite`, a pure-Go translation. The build
needs no cgo and no system libraries.

The two migration trees hold the same schema in each dialect. Change one
tree only when you change the other.

## Readiness

The `serve` command starts in this order:

1. It starts the ops listener. `/healthz` gives 200. `/readyz` gives 503.
2. It applies the migrations.
3. It makes sure that no migration is pending. If one is, it waits.
4. It opens the store.
5. It opens the readiness gate. It starts the API listener.

The API port does not accept a connection until step 5. Look at `/readyz`
to find the cause of a wait:

```sh
curl -i localhost:9090/readyz
```

Liveness and readiness answer different questions. Liveness asks if the
process works. It stays 200 through a slow migration, so an orchestrator
does not stop a pod that starts correctly. Readiness asks if the process
must receive traffic. It stays 503 until the schema is current and the
database answers.

## Metrics

Prometheus metrics are on the ops port, not on the API port. This keeps
them internal, and it keeps them available while the API refuses traffic.

```sh
curl localhost:9090/metrics
```

| Metric | Meaning |
| --- | --- |
| `tinku_rpc_requests_total` | Calls, by service, operation, and outcome. |
| `tinku_rpc_duration_seconds` | Handler latency. |
| `tinku_migrations_pending` | 1 while the schema is behind the binary. |
| `tinku_sessions_reaped_total` | Expired sessions that the sweep removed. |

Alert on `tinku_migrations_pending`. A value of 1 that does not change
means that a deployment stopped before it served traffic.

## Login

The full flow uses a linkkeys relying party:

1. The client calls `auth.begin-login` with a domain.
2. The API makes a nonce. The relying party signs a request that holds it.
3. The API gives the identity-provider URL to the client.
4. The identity provider sends the browser to `GET /auth/callback`.
5. The API decrypts the token and verifies the assertion, the audience, and
   the nonce.
6. The API makes a user record and a session. It sets the session cookie.

Step 4 is not a CSIL operation. An identity provider can only send a
browser to a plain GET URL.

The session belongs to tinku. Linkkeys verifies the identity only. The
database holds the SHA-256 of the cookie value, not the value.

Set these to make the flow work:

```sh
TINKU_LINKKEYS_IDP_DOMAIN=idp.example.com
TINKU_LINKKEYS_IDP_URL=https://idp.example.com
TINKU_LINKKEYS_PKI_URL=http://linkkeys-rp:8080
TINKU_APP_CALLBACK_URL=https://tinku.example.com/auth/callback
TINKU_SESSION_NONCE_SECRET=<a fixed random value>
```

Without them, `auth.begin-login` refuses and the reads still work. Use
`--dev-auth` for local work.

`TINKU_SESSION_NONCE_SECRET` must be the same on every replica. The API
makes a random value at start if you do not set one. Logins then fail after
a restart or across replicas.

## Federation

**Off by default.** An instance can exchange events with another instance,
the way one mail server exchanges mail with another.

An instance is itself an account: `handle@domain`, on the same linkkeys
domain it authenticates its people against. It signs what it sends, and a
peer verifies the signature before reading anything.

```
instance A                                   instance B (a directory)
  event created
  -> outbox row per approved peer            <- signed batch over CSIL-RPC
  -> sender, with retry and backoff          -> signature verified as bytes
                                             -> origin checked against signer
                                             -> stored in remote_events
                                             -> searchable, links back to A
```

Four rules shape it:

| Rule | Why |
| --- | --- |
| Both sides opt in. | Two statuses per peer, moving independently. No instance is listed where it did not choose; no directory fills with events it did not admit. |
| A summary travels, not a description. | The origin stays the only place the whole event lives, and the start-time lock never crosses a domain. |
| A deletion is a message. | A directory cannot tell deletion from silence. |
| Every delivery carries a batch id and a timestamp. | A signature never expires, so it cannot stop a replay on its own. |
| The receipt decides, not the transport status. | A message that arrived is not a message that was kept. |
| A remote event is not an Event. | It has no gathering here, nobody can edit or attend it. Its own table, its own type. |

The signature is detached and made over **opaque bytes**. A delivery carries
its body as `bytes`; the receiver checks the signature against exactly what
arrived, and only then decodes it. Signing a decoded structure would need
both sides to re-encode identically, and any disagreement would look like
forgery.

### The signing scheme is not finished

Signing sits behind a `Signer` and a `Verifier`. One scheme exists today and
**it authenticates nothing**: it exists so the queue, the retries, the
peering flow and the receiving side could be built and tested end to end.
It refuses to start outside a development environment, the same way dev
login does.

The real scheme signs as this instance's linkkeys account. It is not written
because the linkkeys operation that signs an arbitrary payload for another
account to verify has not been confirmed, and inventing one would be
inventing a security guarantee.

## The web client

The client is a SolidJS single-page app. Two rules shape it.

**Every string a person reads is in a catalog.** No component holds English.
`webapp/src/i18n/en-US.ts` holds every message; a component names a key.
To add a language, add a catalog and one entry to `catalogs` in
`webapp/src/i18n/index.tsx`. No component changes.

The catalog obeys four rules, and `i18n.test.ts` holds two of them:

| Rule | Why |
| --- | --- |
| No string is built by joining fragments. | Word order changes between languages. A message with a value in it is one message with a `{placeholder}`. |
| A plural has a `_one` and an `_other` key. | `Intl.PluralRules` picks the form. Some languages need more than two, which is then a catalog change and not a code change. |
| Dates, times and numbers go through `Intl`. | A catalog must not hold a date format. |
| An event shows in **its own** timezone. | 19:00 in Denver is 19:00 in Denver, whoever reads it. |

The recurrence rule is described on the client, from the structured rule.
The server never sends a sentence. This is what lets each language put the
ordinal, the weekday and the period in its own order.

**The client is accessible.** These are behaviours, not decoration:

| Behaviour | Where |
| --- | --- |
| A skip link, first in the tab order. | `App.tsx` |
| Focus moves to the main landmark on each route change. | `App.tsx` |
| `aria-current="page"` on the current navigation link. | `App.tsx` |
| A label, a hint and an error are tied to each control with `aria-describedby`. | `components/Field.tsx` |
| An error is announced with `role="alert"`. A status is announced politely. | `components/Alert.tsx` |
| A visible focus ring that no rule removes. | `index.css` |
| Colour is never the only carrier of meaning. | everywhere |

The server sends a `viewer` block with each resource. It says what the
caller can do. The client renders from it and never decides for itself. A
client that decides moves away from the server the first time a rule
changes.

## Change the schema

The schema is the contract. Change it first.

1. Edit `csil/tinku.csil` or a file in `csil/types/`.
2. Give each new service and operation a new `@wire-id`. Never change or
   use again an ordinal that exists.
3. Run `./tools.sh gen`.
4. Add a row to the dispatch table in `api/internal/server/dispatch.go`.
   The compiler finds a row that does not agree with the schema.
5. Implement the operation in `api/internal/csilservices/`.
6. Run `./tools.sh test`.

Never edit generated code. `./tools.sh gen` writes over it.

## Releases

A merge makes a release. Nothing is released by hand, and nothing is
released by editing a version number.

1. A pull request merges into `main`. `tinku-release-tag` runs
   [semver-tags](https://github.com/catalystcommunity/semver-tags), which
   reads the Conventional Commit subjects since the last tag and pushes the
   next one — `v0.3.0` for a `feat:`, `v0.2.1` for a `fix:`. A merge that
   carries no releasable change pushes no tag, and that is an answer rather
   than a failure.
2. The job then writes that number into `version/VERSION.txt` and pushes it
   to `main`, so the repository states what it last released. This is a
   RECORD, not the trigger.

   The organisation's CI account is allowed past branch protection, so a
   refused push means something is actually wrong — the token, the grant, or
   the rule — and the job fails rather than continuing quietly. A `main`
   that a concurrent merge advanced is a race, not a fault: the commit is
   re-based onto the new tip and pushed again, up to three times.
3. The TAG starts `tinku-release`, which publishes at one version:

   | Job | What it does |
   | --- | --- |
   | `release-images` | Builds and pushes `tinku-api` and `tinku-webapp` to `containers.catalystsquad.com/public/catalystcommunity/`. |
   | `website-deploy` | Builds the marketing site image and rolls the Helm release in the `tinku-website` namespace. |
   | `release-github` | Cuts the GitHub release. It waits for the other two: a release that exists for artifacts that failed to build is a lie somebody has to undo. |

Every job reads its version from the TAG, never from `version/VERSION.txt`.
The tag sits on the merge commit and the version file is written after it,
so a job that trusted the file would publish the previous number.

The api and the web client are published as images and are not deployed
anywhere yet — that waits on a domain for the application itself. The
marketing site is deployed, at `tinkucommunities.com`.

## Documentation

- `docs/OPERATING.md` — the settings, the error codes, and the procedures.
