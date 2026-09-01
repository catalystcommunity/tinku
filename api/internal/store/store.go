// Package store is tinku's persistence seam. It declares the Store
// interface every service method talks to, plus the row types that cross it,
// and nothing else — the two implementations live in store/postgres and
// store/sqlite.
//
// Two implementations rather than one dialect-parameterized query builder is
// a deliberate choice: the dialects differ in more than placeholder style
// (SQLite has no timestamp type, so it reads and writes RFC3339 text), and a
// single implementation papering over that ends up with per-dialect branches
// scattered through every query. Each backend owning its own SQL keeps those
// differences where a reader looking for them will find them.
//
// Open (open.go) picks the implementation from the database URI, so callers
// name a backend by URI and never import either package directly.
package store

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned by every lookup whose row does not exist. It is
// the only sentinel this package defines: callers map it to whatever their
// layer means by "missing" (csilservices turns it into a ServiceError with
// code CodeNotFound).
var ErrNotFound = errors.New("store: not found")

// Store is the full persistence surface tinku needs. It is small enough to
// implement twice on purpose — every method added here is a method both
// backends must grow.
type Store interface {
	// Ping verifies the connection is usable. The readiness probe calls it,
	// so a pod that has lost its database stops reporting ready.
	Ping(ctx context.Context) error

	// Close releases the connection pool.
	Close() error

	// UpsertUser creates or refreshes the user identified by
	// (LinkkeysDomain, LinkkeysUserID) and returns the stored row. Called on
	// every successful login, so the display fields track the assertion.
	UpsertUser(ctx context.Context, params UpsertUserParams) (*User, error)

	// UserByID returns one user, or ErrNotFound.
	UserByID(ctx context.Context, id string) (*User, error)

	// CreateSession stores a session. The caller mints the id, the raw token
	// and the hash; this only writes them.
	CreateSession(ctx context.Context, session *Session) error

	// SessionByTokenHash returns the unexpired session with this token hash
	// and the user it belongs to, or ErrNotFound. An expired session reads
	// as ErrNotFound: an expired cookie and no cookie mean the same thing to
	// every caller.
	SessionByTokenHash(ctx context.Context, tokenHash string) (*Session, *User, error)

	// DeleteSession removes one session by id. Deleting a session that is
	// already gone is not an error — logout is idempotent.
	DeleteSession(ctx context.Context, id string) error

	// DeleteExpiredSessions removes every session past its expiry and
	// returns how many rows went. Called on a timer by `tinku serve`.
	DeleteExpiredSessions(ctx context.Context) (int64, error)

	// CreateGreeting stores a greeting and returns it as it will be read
	// back, with the author handle joined in.
	CreateGreeting(ctx context.Context, authorID string, message string) (*Greeting, error)

	// ListGreetings returns every greeting, newest first.
	ListGreetings(ctx context.Context) ([]Greeting, error)

	// GreetingByID returns one greeting, or ErrNotFound.
	GreetingByID(ctx context.Context, id string) (*Greeting, error)

	// ---- People and the global admin role ----------------------------

	// UserByHandle resolves one federated address (`handle@domain`) to a
	// user, or ErrNotFound. A roster editor needs this: people know each
	// other by address, not by ULID.
	UserByHandle(ctx context.Context, handle, domain string) (*User, error)

	// SearchUsers returns users whose handle starts with prefix, so a member
	// picker can offer completions. Prefix, not substring: a prefix can use
	// the handle index, and completing from the middle of a handle is not
	// how anybody looks a person up.
	SearchUsers(ctx context.Context, prefix string, page Page) ([]User, error)

	// SetAdmin grants or revokes the global admin role. Idempotent.
	SetAdmin(ctx context.Context, userID string, granted bool) error

	// ListAdmins returns everybody holding the role, oldest grant first.
	ListAdmins(ctx context.Context) ([]User, error)

	// ---- Organizations -------------------------------------------------------

	// CreateOrganization stores an organization and makes firstOwnerID its first owner, in
	// one transaction. An organization with no owner could never be edited or given
	// one, so the two writes are not separable.
	CreateOrganization(ctx context.Context, in OrganizationInput, firstOwnerID string) (*Organization, error)

	// UpdateOrganization replaces an organization's own fields. The caller supplies every
	// field, having read the row first — see OrganizationInput.
	UpdateOrganization(ctx context.Context, id string, in OrganizationInput) (*Organization, error)

	// OrganizationByID returns one organization with its roster counts, or ErrNotFound.
	OrganizationByID(ctx context.Context, id string) (*Organization, error)

	// OrganizationBySlug returns the organization at one federated address, or
	// ErrNotFound. A slug is derived from a name, and two people naming a
	// organization the same thing is normal — so minting one needs a real lookup,
	// not a substring search that happens to miss.
	OrganizationBySlug(ctx context.Context, originDomain, slug string) (*Organization, error)

	// ListOrganizations returns the page f asks for and the total before paging.
	ListOrganizations(ctx context.Context, f OrganizationFilter) ([]Organization, int64, error)

	// DeleteOrganization removes an organization and everything referencing it. Deleting
	// one that is already gone is not an error.
	DeleteOrganization(ctx context.Context, id string) error

	// SetOrganizationMember adds somebody to a roster or changes their standing.
	// Idempotent.
	SetOrganizationMember(ctx context.Context, organizationID, userID string, role OrganizationRole) error

	// RemoveOrganizationMember takes somebody off a roster. Idempotent.
	RemoveOrganizationMember(ctx context.Context, organizationID, userID string) error

	// ListOrganizationMembers returns a page of the roster, owners first, and the
	// total before paging.
	ListOrganizationMembers(ctx context.Context, organizationID string, page Page) ([]OrganizationMember, int64, error)

	// OrganizationRoleFor returns one person's standing in one organization. ok is false
	// when they are not on the roster at all.
	OrganizationRoleFor(ctx context.Context, organizationID, userID string) (role OrganizationRole, ok bool, err error)

	// ---- Gatherings ---------------------------------------------------

	// CreateGathering stores a gathering and its first owner in one
	// transaction, for the same reason CreateOrganization does.
	CreateGathering(ctx context.Context, in GatheringInput, firstOwner OwnerRef) (*Gathering, error)

	// UpdateGathering replaces a gathering's own fields.
	UpdateGathering(ctx context.Context, id string, in GatheringInput) (*Gathering, error)

	// GatheringByID returns one gathering with its owners and counts, or
	// ErrNotFound.
	GatheringByID(ctx context.Context, id string) (*Gathering, error)

	// GatheringBySlug returns the gathering at one federated address, or
	// ErrNotFound.
	GatheringBySlug(ctx context.Context, originDomain, slug string) (*Gathering, error)

	// ListGatherings returns the page f asks for and the total before paging.
	ListGatherings(ctx context.Context, f GatheringFilter) ([]Gathering, int64, error)

	// DeleteGathering removes a gathering and every event under it.
	DeleteGathering(ctx context.Context, id string) error

	// AddGatheringOwner adds an individual or an organization as an owner.
	// Idempotent.
	AddGatheringOwner(ctx context.Context, gatheringID string, owner OwnerRef) error

	// RemoveGatheringOwner drops one owner. Idempotent.
	RemoveGatheringOwner(ctx context.Context, gatheringID string, owner OwnerRef) error

	// CountGatheringOwners reports how many ownership rows a gathering has,
	// so the service can refuse to remove the last one.
	CountGatheringOwners(ctx context.Context, gatheringID string) (int64, error)

	// JoinGathering puts somebody on the roster. Idempotent.
	JoinGathering(ctx context.Context, gatheringID, userID string) error

	// LeaveGathering takes somebody off the roster. Idempotent.
	LeaveGathering(ctx context.Context, gatheringID, userID string) error

	// ListGatheringMembers returns a page of the roster and the total.
	ListGatheringMembers(ctx context.Context, gatheringID string, page Page) ([]GatheringMember, int64, error)

	// GatheringAccessFor resolves one person's whole standing in one
	// gathering — membership and ownership, the latter through owned organizations
	// as well as directly — in a single round trip, because every
	// permission check needs all of it at once.
	GatheringAccessFor(ctx context.Context, gatheringID, userID string) (GatheringAccess, error)

	// ---- Events -------------------------------------------------------

	// CreateEvent stores one event.
	CreateEvent(ctx context.Context, in EventInput) (*Event, error)

	// UpdateEvent replaces an event's own fields.
	UpdateEvent(ctx context.Context, id string, in EventInput) (*Event, error)

	// EventByID returns one event with its attendee count, or ErrNotFound.
	// ViewerAttending is left false; fill it from AttendanceFor.
	EventByID(ctx context.Context, id string) (*Event, error)

	// ListEvents returns the page f asks for and the total before paging.
	ListEvents(ctx context.Context, f EventFilter) ([]Event, int64, error)

	// DeleteEvent removes one event. Idempotent.
	DeleteEvent(ctx context.Context, id string) error

	// AttendEvent marks somebody attending. Idempotent.
	AttendEvent(ctx context.Context, eventID, userID string) error

	// UnattendEvent withdraws their attendance. Idempotent.
	UnattendEvent(ctx context.Context, eventID, userID string) error

	// ListAttendees returns a page of who is attending and the total.
	ListAttendees(ctx context.Context, eventID string, page Page) ([]Attendee, int64, error)

	// AttendanceFor reports which of eventIDs this person is attending. One
	// query fills ViewerAttending for a whole listing, which is why it takes
	// a slice rather than being asked once per event.
	AttendanceFor(ctx context.Context, userID string, eventIDs []string) (map[string]bool, error)

	// ---- Event series -------------------------------------------------

	// CreateEventSeries stores one series. Materializing its occurrences is
	// a separate step (UpsertOccurrences), because the rule that produces
	// them lives in the service layer, not in SQL.
	CreateEventSeries(ctx context.Context, in EventSeriesInput) (*EventSeries, error)

	// UpdateEventSeries replaces a series' own fields.
	UpdateEventSeries(ctx context.Context, id string, in EventSeriesInput) (*EventSeries, error)

	// EventSeriesByID returns one series with its occurrence count and next
	// occurrence, or ErrNotFound.
	EventSeriesByID(ctx context.Context, id string) (*EventSeries, error)

	// ListEventSeries returns the page f asks for and the total.
	ListEventSeries(ctx context.Context, f EventSeriesFilter) ([]EventSeries, int64, error)

	// DeleteEventSeries removes a series. Its occurrences survive with a
	// null series_id — see the ON DELETE SET NULL in the migration and the
	// note about history in csil/types/events.csil.
	DeleteEventSeries(ctx context.Context, id string) error

	// DeleteOccurrencesFrom removes a series' occurrences starting at or
	// after notBefore. Rewriting a series after an edit is this plus
	// UpsertOccurrences, and passing the caller's "now" is what keeps
	// occurrences that have already started out of it.
	DeleteOccurrencesFrom(ctx context.Context, seriesID string, notBefore time.Time) error

	// UpsertOccurrences materializes occurrences, skipping any instant the
	// series already has a row for, and returns how many were new. The
	// partial unique index on (series_id, starts_at) is what makes this
	// idempotent rather than duplicating on every call.
	UpsertOccurrences(ctx context.Context, seriesID string, occurrences []EventInput) (int64, error)

	// SetMaterializedThrough records how far the horizon has been expanded.
	SetMaterializedThrough(ctx context.Context, seriesID string, through time.Time) error

	// ---- Roles on events and series -----------------------------------

	// SetEventRole gives somebody a role on one event or one series.
	// Idempotent.
	SetEventRole(ctx context.Context, scope RoleScope, userID string, role EventRole) error

	// RemoveEventRole takes a role away. Idempotent.
	RemoveEventRole(ctx context.Context, scope RoleScope, userID string, role EventRole) error

	// ListEventRoles returns everybody holding a role in scope.
	ListEventRoles(ctx context.Context, scope RoleScope) ([]EventRoleAssignment, error)

	// EventRolesFor reports what roles one person holds on one event,
	// counting roles held on its parent series: an organizer of a series
	// organizes every occurrence of it, and saying so once here is what
	// stops every caller from having to remember that.
	EventRolesFor(ctx context.Context, eventID, userID string) (isOrganizer, isPresenter bool, err error)

	// SeriesRolesFor is the same question asked about a series directly.
	SeriesRolesFor(ctx context.Context, seriesID, userID string) (isOrganizer, isPresenter bool, err error)

	// ---- Federation: peers -------------------------------------------

	// CreatePeer records another instance. Both statuses start at none:
	// knowing about a peer is not agreeing with it.
	CreatePeer(ctx context.Context, in PeerInput) (*Peer, error)

	// PeerByID returns one peer, or ErrNotFound.
	PeerByID(ctx context.Context, id string) (*Peer, error)

	// PeerByAddress returns the peer at one address, or ErrNotFound. The
	// address, not the URL, is the identity: a peer that moves host keeps
	// its name, and a signature is checked against the name.
	PeerByAddress(ctx context.Context, address string) (*Peer, error)

	// ListPeers returns a page of peers and the total before paging.
	ListPeers(ctx context.Context, page Page) ([]Peer, int64, error)

	// UpdatePeer replaces a peer's settable fields.
	UpdatePeer(ctx context.Context, id string, in PeerInput) (*Peer, error)

	// SetPeerStatus changes one or both directions. A nil status leaves
	// that direction alone, so approving inbound cannot silently approve
	// outbound. A nil identity leaves the peer's stored identity as it is;
	// a non-nil one replaces it — see PeerIdentity and
	// FederationService.SetPeerStatus for the rule that inbound approval
	// requires an identity to already be set or supplied here.
	SetPeerStatus(ctx context.Context, id string, inbound, outbound *PeerStatus, identity *PeerIdentity) (*Peer, error)

	// DeletePeer forgets a peer, its queue and everything it sent.
	DeletePeer(ctx context.Context, id string) error

	// ---- Federation: delivery health ---------------------------------

	// RecordDeliverySuccess clears the failure run and stamps the success.
	// The run is over, so first_failure_at goes with it.
	RecordDeliverySuccess(ctx context.Context, peerID string, at time.Time) error

	// RecordDeliveryFailure stamps a failure, starting a run if none is
	// open. It suspends the peer when the run began at or before
	// suspendIfStartedBefore — which is how "stop after this long" becomes
	// one comparison rather than a counter that has to be reasoned about.
	RecordDeliveryFailure(ctx context.Context, peerID, reason string, at, suspendIfStartedBefore time.Time) error

	// ResumePeer lifts a suspension, clears the failure run, and makes every
	// waiting delivery due now. This is the button an administrator presses;
	// nothing else clears a suspension.
	ResumePeer(ctx context.Context, peerID string, now time.Time) error

	// ---- Federation: the outbox --------------------------------------

	// EnqueueDelivery queues one event for one peer, REPLACING whatever was
	// already queued for that pair. An event edited five times before its
	// first delivery is delivered once, in its final state, and a deletion
	// replaces a pending update rather than racing it.
	EnqueueDelivery(ctx context.Context, peerID, eventID string, payload []byte, dueAt time.Time) error

	// DueDeliveries returns work that is ready: due now, for a peer whose
	// outbound status is approved and which is not suspended. The peer's
	// address and base URL ride along, so the sender needs no second read.
	DueDeliveries(ctx context.Context, now time.Time, limit int) ([]OutboxItem, error)

	// MarkDelivered removes a delivered item.
	MarkDelivered(ctx context.Context, id string) error

	// MarkDeliveryFailed records the error and when to try again. It counts
	// an attempt, which is what grows the exponential backoff.
	MarkDeliveryFailed(ctx context.Context, id, reason string, nextAttemptAt time.Time) error

	// DeferDelivery reschedules without counting an attempt.
	//
	// It is for backpressure, not for failure: a peer that refused an event
	// for rate ANSWERED, so the delivery is not broken and the exponential
	// backoff — which exists to stop hammering something unreachable —
	// must not grow. Counting it would also march the peer toward
	// suspension for being busy.
	DeferDelivery(ctx context.Context, id, reason string, nextAttemptAt time.Time) error

	// OutboundPeers returns every peer this instance publishes to: approved
	// outbound and not suspended.
	OutboundPeers(ctx context.Context) ([]Peer, error)

	// ---- Federation: what peers sent ---------------------------------

	// UpsertRemoteEvent stores or refreshes one delivered event.
	UpsertRemoteEvent(ctx context.Context, peerID string, in RemoteEventInput) error

	// DeleteRemoteEvent forgets one. ok is false when nothing was held,
	// which is how a tombstone for something never seen is counted as
	// rejected rather than accepted.
	DeleteRemoteEvent(ctx context.Context, peerID, remoteID string) (ok bool, err error)

	// ListRemoteEvents returns the page f asks for and the total.
	ListRemoteEvents(ctx context.Context, f RemoteEventFilter) ([]RemoteEvent, int64, error)

	// DeleteRemoteEventsEndedBefore drops what a peer sent that has already
	// finished, so a directory does not grow without bound. Returns how
	// many went.
	DeleteRemoteEventsEndedBefore(ctx context.Context, cutoff time.Time) (int64, error)

	// ---- Instance settings -------------------------------------------

	// InstanceSettings reads the settings, filling anything unset from
	// DefaultInstanceSettings. It never returns "not configured": an
	// instance that has set nothing still has behaviour, and every caller
	// would otherwise have to know the same defaults.
	InstanceSettings(ctx context.Context) (InstanceSettings, error)

	// PutInstanceSettings writes the whole set. The caller reads, changes
	// what it means to change, and writes back — the same read-modify-write
	// the other update paths use.
	PutInstanceSettings(ctx context.Context, in InstanceSettings) error

	// ---- Rate limiting ------------------------------------------------

	// ConsumePeerAllowance takes up to `wanted` from this peer's budget for
	// the minute containing `now`, and reports what it got.
	//
	// It is a fixed window on the peer row rather than a token bucket: two
	// columns, reset when the minute rolls over, and no background work. A
	// peer can therefore burst across a window boundary, which is a price
	// worth paying for a limiter that cannot itself fall behind.
	//
	// A limit of zero means no limit, and everything asked for is allowed.
	ConsumePeerAllowance(ctx context.Context, peerID string, wanted, limit int64, now time.Time) (RateVerdict, error)

	// SetPeerRateLimit gives one peer its own allowance. A nil limit
	// restores the instance-wide one.
	SetPeerRateLimit(ctx context.Context, peerID string, limit *int64) (*Peer, error)

	// RecordOriginAccepted counts one accepted event against the
	// organization that originated it. Counted on ACCEPT, so it measures
	// what landed rather than what was attempted — the peer's
	// RateLimitedTotal already counts the refusals.
	RecordOriginAccepted(ctx context.Context, peerID, organizationName string, now time.Time) error

	// ListOriginVolume returns origins busiest first, optionally narrowed
	// to one peer. defaultLimit fills EffectiveRateLimitPerMinute for an
	// origin with no override of its own.
	ListOriginVolume(ctx context.Context, peerID string, defaultLimit int64, now time.Time, page Page) ([]OriginVolume, int64, error)

	// ConsumeOriginAllowance takes up to `wanted` from ONE organization's
	// budget inside one peer, resolving that origin's own limit over
	// defaultLimit. Same fixed window as the peer's.
	ConsumeOriginAllowance(ctx context.Context, peerID, organizationName string, wanted, defaultLimit int64, now time.Time) (RateVerdict, error)

	// RememberBatch records that a peer's batch has been applied, and
	// reports whether it was already known. A second arrival is a replay.
	//
	// The insert is the check: two concurrent copies of the same replayed
	// envelope must not both be "first", and only a unique constraint can
	// promise that.
	RememberBatch(ctx context.Context, peerID, batchID string, now time.Time) (firstTime bool, err error)

	// ForgetBatch un-remembers one batch, so an honest sender may send it
	// again. It is for a batch that arrived but was NOT applied.
	ForgetBatch(ctx context.Context, peerID, batchID string) error

	// ForgetBatchesSeenBefore drops remembered ids that are older than any
	// batch the freshness window would now accept. Without it the table
	// grows forever.
	ForgetBatchesSeenBefore(ctx context.Context, cutoff time.Time) (int64, error)

	// SetOriginRateLimit gives one origin its own allowance. A nil limit
	// restores the instance-wide one.
	SetOriginRateLimit(ctx context.Context, peerID, organizationName string, limit *int64, defaultLimit int64, now time.Time) (*OriginVolume, error)
}
