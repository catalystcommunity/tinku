# Working in this repository

Tinku is a federated directory of gatherings, built on the local
development flow these repositories share. Read `README.md` first for the
architecture and the domain. This file holds the rules that are not obvious
from the code.

## The schema is the contract

`csil/tinku.csil` and `csil/types/*.csil` define every type and every
operation. Change the schema first, then run `./tools.sh gen`, then change
the code.

Never edit these directories. `./tools.sh gen` writes over them:

- `api/internal/csil/`
- `webapp/src/gen/`
- `clients/go/`, `clients/typescript/`

`api/internal/transport/` and `webapp/src/transport/csil/` are vendored
copies of the csilgen reference transport. Do not edit them either. Copy
them again from `catalystcommunity/csilgen/transports/` so that the
conformance vectors continue to pass.

### Wire ids

Every service and every operation carries a `@wire-id` ordinal. Give a new
declaration a new ordinal. Never change an ordinal, and never use one
again. Service ordinal 0 belongs to the transport control plane.

### Include order

Only `csil/tinku.csil` uses `include`. The files in `csil/types/` include
nothing. csilgen emits a type twice if the type is reachable through two
include edges in one compilation.

## The domain rules that reach every layer

Four rules cross the layers. Break one in a new place and the tests catch
it, but only because somebody wrote the test — so read these first.

**An occurrence of a series is an event.** A series holds the rule; its
occurrences are ordinary `events` rows with `series_id` set. Never add a
second shape for an occurrence. A query that must find "events" finds these
too, and that is the point.

**The start-time lock.** When `starts_at` goes by, the event freezes: no
edit, no attendance change, no description, for every caller. Three places
hold this and all three must agree:

| Place | What it does |
| --- | --- |
| `csilservices/perms.go` — `lockedAt` | The one definition of "started". |
| `csilservices/perms.go` — `eventViewer` | Clears the permissions and keeps only an administrator's power to delete. |
| `csilservices/convert.go` — `toEvent` | Does not send the description or the online link. |

A new operation on an event must call `lockedAt` before it writes.

**Every stored instant is UTC.** A timezone is for display and for entry.
It never reaches a timestamp column. `formatTime` (SQLite) and
`args.nextTime` (PostgreSQL) are the only two writers, and both call
`.UTC()`; a new write path must go through one of them.
`TestEveryStoredInstantIsUTC` reads the raw column text rather than a value
the store handed back, because a round trip through the store would pass
even if the bytes on disk were wrong.

One thing is not an instant and must not be flattened into one:
`event_series.start_time` with `event_series.timezone`. Together they are
the RULE — "the second Thursday at 19:00 in America/Denver" — and a rule is
entry, not a time. It cannot be held as UTC without becoming wrong: that
same rule is 01:00 UTC in September and 02:00 UTC in November. The
occurrences the rule PRODUCES are stored as UTC instants like everything
else. See `TestLocalClockSurvivesDaylightSaving`.

**Only administrators delete an organization, or an event that started.** Every
other deletion belongs to an owner. Do not add a second way around this.

**The permission model lives in `csilservices/perms.go` and nowhere else.**
Every operation asks that file. The answer travels to the client in the
`viewer` block, so the client never decides for itself. When a rule changes,
one file changes.

`store.GatheringAccessFor` answers membership and ownership in one query.
Ownership reaches a person through an organization they own. Never
re-derive this in a service.

## Adding an operation

1. Declare the type and the operation in `csil/`.
2. Run `./tools.sh gen`.
3. Add a row to `buildRoutes` in `api/internal/server/dispatch.go`. Use
   `routeFallible` when the operation declares a `/ ServiceError` arm, and
   `routeInfallible` when it does not.
4. Implement the method in `api/internal/csilservices/`. Ask `perms.go`
   for the permission. Do not write a new rule in the operation.
5. Add every string the client shows to `webapp/src/i18n/en-US.ts`.
6. Run `./tools.sh test`.

## The error contract

A service method returns one of two kinds of error:

- `*csilservices.AppError` — an expected failure. The dispatcher sends it as
  the declared `ServiceError` arm. The caller sees the message.
- Any other error — an unexpected failure. The dispatcher logs it and sends
  transport status 6. The caller sees nothing.

A bare `fmt.Errorf` from a service method is always safe. It can only hide
information from a caller. It cannot disclose information.

## The two store backends

`api/internal/store` holds the interface. `store/postgres` and
`store/sqlite` each hold their own SQL. A method added to the interface is a
method that both backends must have.

`coredb/migrations/postgres/` and `coredb/migrations/sqlite/` hold the same
schema in each dialect. Change both, or change neither. These are the
differences that the dialects force:

- SQLite has no timestamp type. It stores RFC3339 UTC text in the layout
  that `sqlite.timeLayout` defines. Text comparison and text ordering then
  work.
- SQLite does not allow `INSERT` inside a common table expression. The
  SQLite backend uses a transaction where the PostgreSQL backend uses one
  statement.

Ids are ULIDs that `store.NewID` makes in Go. The database does not make
them, because only one of the two dialects can.

## Federation

Off unless switched on, and off in every test that does not turn it on. The
rules that must not be quietly broken:

**Both directions are separate decisions.** `inbound_status` and
`outbound_status` move independently. Never add code that sets one from the
other, and never collapse them into a single "trusted" flag.

**Verify before decode.** `SignedDelivery.body` is `bytes`. Check the
signature against the bytes as received, THEN decode. Decoding first, or
signing a decoded structure, makes the two sides agree on an encoding — and
any disagreement then reads as forgery.

**A verified signer speaks only for its own domain.** `DeliverEvents`
compares `EventBatch.origin_domain` with the verified peer's domain. Keep
that check next to the verification.

**A delivery carries no description.** Add a field to `FederatedEvent` only
if it is safe on a site you do not control. This is also what keeps the
start-time lock from crossing a domain boundary: there is nothing withheld
to leak.

**Signing goes through `federation.Signer` and `federation.Verifier`.** Do
not call a crypto library from delivery code. The only scheme today
authenticates nothing and refuses to build outside a dev environment; the
real one replaces it at the seam.

**"Unset" is a third state.** `PublishSetting` is unset, in or out at each
of three levels, and unset means "the level above decides". Never model it
as a boolean, and never write an empty string where the column expects
NULL — `nullablePublish` exists for that.

**The publish rule lives in `resolvePublish` and nowhere else.** The server
resolves it and sends the answer in `Gathering.publish`; the client renders
that answer. A client that re-derives a three-level rule will disagree with
the server the first time a level changes.

**A tombstone ignores the publish rule.** `EventService.publish` checks the
decision for an update and skips it for a deletion. An event that was
published and then opted out of still has to be withdrawn from the peer.

**A name is not an identity; the domain is.** Every displayable record
carries an `origin` block, and the client always shows the domain. Never add
a screen that shows a name from another domain without it: two instances can
hold the same name, and a directory shows both at once.

`is_external` is DERIVED from the verified peer, never read from the
message. Do not add an origin field to `FederatedEvent` — a sender would
then be trusted to say who it is, which the signature already answers.

**Rate limiting has two levels and an event passes both.** A peer's
allowance is shared, so a per-organization limit is what stops one
organization inside a peer starving the rest. A deletion is charged against
neither: refusing a tombstone for rate would leave an event here that its
origin removed.

**The limiter owns the minute window; the counters own the totals.**
`ConsumeOriginAllowance` writes `window_start` and `window_count`.
`RecordOriginAccepted` writes `accepted_total` and `last_received_at`. Both
writing the window is how it once got counted twice.

**Read the receipt, not the transport status.** A transport-level OK says
the message arrived, not that the peer kept it. `Sender.deliver` returns an
outcome, and only `accepted` removes the outbox row. Treating arrival as
acceptance is how a rate-limited event gets deleted and lost.

**Backpressure is not failure.** A rate refusal uses `DeferDelivery`, which
does not count an attempt, and records the peer as REACHABLE. Counting it
would grow the exponential backoff and move a working peer toward
suspension.

**The limiter is one statement.** `ConsumePeerAllowance` and
`ConsumeOriginAllowance` do the window roll, the decision and the write in a
single UPDATE, and return the granted amount through `rate_last_allowed`.
The API scales horizontally, so nothing held in one process can enforce a
limit, and a read-then-write across two statements lets N callers each see
an empty window. `TestTheLimiterHoldsUnderConcurrency` is the proof, and it
only means something on the Postgres run.

**A batch id is claimed before the batch is processed, and released if
nothing was applied.** Claiming afterwards leaves a window where two copies
of a replay both pass; never releasing it means an honest retry of a
rate-refused batch is rejected forever.

**A suspension is lifted by a person.** `ResumePeer` is the only thing that
clears `suspended_at`. Do not add a timer that un-suspends.

`api/internal/server/federation_test.go` boots TWO instances with two
databases and real HTTP. A federation change is not tested until it is
tested there — a single instance cannot exercise a feature whose whole
content is what happens between two of them.

## The web client

Three rules. The first two have tests.

**No component holds a string a person reads.** Every message is a key in
`webapp/src/i18n/en-US.ts`. Never join fragments to make a sentence: word
order changes between languages. A message with a value in it has a
`{placeholder}`. A plural has a `_one` key and an `_other` key, and
`plural()` picks with `Intl.PluralRules`.

Dates, times and numbers go through the formatters in
`webapp/src/i18n/index.tsx`. An event shows in **its own** timezone, never
in the reader's.

A recurrence rule is described on the client, from the structured rule
(`webapp/src/i18n/recurrence.ts`). The server never sends a sentence.

**Accessibility is behaviour, not style.** Use `components/Field.tsx` for a
form control: it ties the label, the hint and the error together. Use
`components/Alert.tsx` for a failure. Give each button in a list an
`aria-label` that says which row it acts on. Colour must never be the only
carrier of meaning.

**One stylesheet, and it holds every colour.** `webapp/src/index.css`
defines the palette as custom properties on bare `:root`; the dark theme
redefines the properties and nothing else. The palette is the marketing
site's (`website/site-src/content/extra_files/styles.css`) — change one and
change the other. Never write a colour in a component.

`--accent` is a fill and `--accent-text` is text. They differ in the dark
theme because a fill needs 3:1 and body text needs 4.5:1, and `#e0445f`
reaches only 4.2:1 on a panel.

The layout classes are the whole vocabulary: `.field-row` puts fields side
by side and collapses on a phone, `.check-row` does the same for
checkboxes, `.form-actions` holds the buttons that end a form,
`.danger-zone` holds a deletion, `.page-address` and `.page-meta` are the
lines under a title. A `<form>` is already a panel — do not put a
`<fieldset>` around the whole of one, which draws a box inside a box.

## Tests

`api/internal/server/server_test.go` and `domain_test.go` run the real HTTP
handler against a real SQLite database. Nothing in them is a mock. Write a
new end-to-end test the same way. The SQLite backend makes this possible
without a server, and that is most of the reason it exists.

`domain_test.go` holds `testEnv`, which carries a clock the test moves.
Use it for any rule about the passage of time. The only other way to test
the start-time lock is to wait.

`api/internal/csilservices/recurrence_test.go` holds every rule the domain
names by example, and the cases where a rule makes nothing: a fifth Thursday
in a month with four, the 31st of a short month. A rule that cannot happen
must make no event. Never round it to the nearest day.

`api/internal/transport/conformance_test.go` and
`webapp/src/transport/csil/conformance.test.ts` check the vendored codecs
against the shared vectors. A failure means that the vendored copy is old.

## Before you finish

```sh
./tools.sh lint
./tools.sh test
```

After a change to either store backend, or to a migration, also run the
same suite against the backend production uses:

```sh
./tools.sh test-pg     # needs docker; ./tools.sh dev-down stops it again
```

`store/postgres` and `store/sqlite` hold their own SQL, so a query is only
exercised on the backend the tests happen to use. Two bugs have already
reached this repository through that gap: an argument list that PostgreSQL
rejected and SQLite silently misbound into a LIKE pattern, and a set of
table renames. `./tools.sh test` alone would have passed for both.
