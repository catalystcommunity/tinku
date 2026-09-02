package store

import (
	"crypto/rand"
	"time"

	"github.com/oklog/ulid/v2"
)

// UserKind mirrors the CSIL UserKind and the `kind` CHECK constraint on the
// users table.
type UserKind string

const (
	// UserKindHuman is a linkkeys-authenticated person.
	UserKindHuman UserKind = "human"
	// UserKindSystem is a non-interactive actor.
	UserKindSystem UserKind = "system"
)

// User is a row of the users table: the local record of an identity a
// linkkeys assertion resolved to.
type User struct {
	ID             string
	LinkkeysDomain string
	LinkkeysUserID string
	Handle         string
	DisplayName    string
	Kind           UserKind
	CreatedAt      time.Time
	// IsAdmin is the global admin role: the one thing that decides who may
	// delete an organization, or an event that has already started. It rides on the
	// users row rather than in its own table because it is one bit and
	// every session load already reads this row.
	IsAdmin bool
	// AdminGrantedAt is when the role was last granted, nil when the user
	// has never held it.
	AdminGrantedAt *time.Time
}

// Session is a row of the sessions table. RawToken is never stored — only
// its SHA-256 is, in TokenHash — so it lives on this struct just long enough
// for the caller that minted it to put it in a Set-Cookie header.
type Session struct {
	ID        string
	UserID    string
	TokenHash string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Greeting is a row of the greetings table, joined to its author's handle
// for display. AuthorID and AuthorHandle are nil for a greeting whose author
// row was deleted, which the schema allows (ON DELETE SET NULL) so a
// greeting outlives its author.
type Greeting struct {
	ID           string
	AuthorID     *string
	AuthorHandle *string
	Message      string
	CreatedAt    time.Time
}

// UpsertUserParams identifies a user by their linkkeys identity — the pair
// the users table is unique on — and carries the display fields to refresh
// from the assertion on every login.
type UpsertUserParams struct {
	LinkkeysDomain string
	LinkkeysUserID string
	Handle         string
	DisplayName    string
	Kind           UserKind
}

// NewID mints a ULID: a lexicographically sortable, time-prefixed
// identifier. Ids are minted here rather than by the database because the
// same schema runs on Postgres and on SQLite, and only one of those has a
// usable generator — see the header of coredb/migrations/postgres/000001_hello.sql.
func NewID() string {
	return ulid.MustNew(ulid.Now(), rand.Reader).String()
}

// ---------------------------------------------------------------------------
// The gathering domain
// ---------------------------------------------------------------------------

// OrganizationRole is a member's standing in an organization. It mirrors the
// `role` CHECK on organization_members.
type OrganizationRole string

const (
	// OrganizationRoleOwner may edit the organization, manage its roster, and act as the
	// organization wherever an organization can own something.
	OrganizationRoleOwner OrganizationRole = "owner"
	// OrganizationRoleMember is on the roster and carries no authority.
	OrganizationRoleMember OrganizationRole = "member"
)

// OwnerKind says whether an ownership row points at a user or at an organization.
type OwnerKind string

const (
	// OwnerKindUser is an individual owner.
	OwnerKindUser OwnerKind = "user"
	// OwnerKindOrganization is an organization that owns on behalf of its own owners.
	OwnerKindOrganization OwnerKind = "organization"
)

// EventRole is a role held on one event or one series. Ownership is not in
// here: owners belong to the gathering and reach every event under it.
type EventRole string

const (
	// EventRoleOrganizer may edit the event or series it is held on.
	EventRoleOrganizer EventRole = "organizer"
	// EventRolePresenter is billed as presenting. It carries a member's
	// permissions and nothing more.
	EventRolePresenter EventRole = "presenter"
)

// RecurrenceFreq is the period a rule repeats over.
type RecurrenceFreq string

// The four periods a series can repeat over.
const (
	FreqWeekly    RecurrenceFreq = "weekly"
	FreqMonthly   RecurrenceFreq = "monthly"
	FreqQuarterly RecurrenceFreq = "quarterly"
	FreqYearly    RecurrenceFreq = "yearly"
)

// Weekday is a day of the week as the schema spells it: lowercase English,
// a wire and storage value rather than display text.
type Weekday string

// weekdaysFromSunday lists the seven in the order Go's time.Weekday numbers
// them, so WeekdayOf can index it directly.
var weekdaysFromSunday = [7]Weekday{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

// WeekdayOf names the weekday t falls on.
func WeekdayOf(t time.Time) Weekday { return weekdaysFromSunday[int(t.Weekday())] }

// TimeWeekday converts back to Go's numbering. ok is false for a string
// that is not one of the seven.
func TimeWeekday(w Weekday) (time.Weekday, bool) {
	for i, candidate := range weekdaysFromSunday {
		if candidate == w {
			return time.Weekday(i), true
		}
	}
	return 0, false
}

// Organization is a row of the organizations table, with its two roster
// counts joined in.
type Organization struct {
	ID           string
	Slug         string
	OriginDomain string
	Name         string
	Blurb        string
	Description  string
	// PublishEvents is this organization's own answer, which may be unset.
	PublishEvents PublishSetting
	MemberCount   int64
	OwnerCount    int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// OrganizationMember is one roster row joined to the person it names.
type OrganizationMember struct {
	OrganizationID string
	UserID         string
	Handle         string
	LinkkeysDomain string
	DisplayName    string
	Role           OrganizationRole
	JoinedAt       time.Time
}

// OwnerRef is one owner of a gathering: an individual or an organization. ID is a
// user id or an organization id according to Kind; the display fields are joined in
// on read and ignored on write.
type OwnerRef struct {
	Kind         OwnerKind
	ID           string
	DisplayName  string
	Handle       string
	OriginDomain string
}

// Gathering is a row of the gatherings table with its owners and its
// derived counts.
type Gathering struct {
	ID           string
	Slug         string
	OriginDomain string
	Name         string
	Blurb        string
	Description  string
	Owners       []OwnerRef
	// PublishEvents is this gathering's own answer, which may be unset.
	PublishEvents PublishSetting
	MemberCount   int64
	EventCount    int64
	NextEventAt   *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// GatheringMember is one roster row of a gathering.
type GatheringMember struct {
	GatheringID    string
	UserID         string
	Handle         string
	LinkkeysDomain string
	DisplayName    string
	JoinedAt       time.Time
}

// Location is the loc_* column group, shared by events and series. Every
// field is optional; the service is what insists an in-person event have
// something here.
type Location struct {
	Name       string
	Address    string
	Locality   string
	Region     string
	PostalCode string
	Country    string
	Latitude   *float64
	Longitude  *float64
}

// IsZero reports whether nothing about this location was given.
func (l Location) IsZero() bool {
	return l.Name == "" && l.Address == "" && l.Locality == "" && l.Region == "" &&
		l.PostalCode == "" && l.Country == "" && l.Latitude == nil && l.Longitude == nil
}

// Recurrence is the rule half of a series: the recurrence_* column group.
type Recurrence struct {
	Freq       RecurrenceFreq
	Interval   int64
	Weekday    *Weekday
	Ordinal    *int64
	DayOfMonth *int64
}

// Event is a row of the events table. An occurrence of a series is one of
// these with SeriesID set — there is no separate occurrence type, because
// on the wire and in the database there is no separate occurrence.
//
// AttendeeCount is joined in on every read. ViewerAttending is NOT: it
// depends on who is asking, so the caller fills it from AttendanceFor.
type Event struct {
	ID              string
	GatheringID     string
	SeriesID        *string
	Title           string
	Description     string
	IsOnline        bool
	IsInPerson      bool
	OnlineURL       string
	Location        Location
	StartsAt        time.Time
	EndsAt          time.Time
	Timezone        string
	AttendeeCount   int64
	ViewerAttending bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// EventSeries is a row of the event_series table: the rule, the template
// fields, and how far the occurrence horizon has been materialized.
type EventSeries struct {
	ID                  string
	GatheringID         string
	Title               string
	Description         string
	IsOnline            bool
	IsInPerson          bool
	OnlineURL           string
	Location            Location
	Recurrence          Recurrence
	StartsOn            time.Time
	EndsOn              *time.Time
	StartTime           string
	DurationMinutes     int64
	Timezone            string
	MaterializedThrough time.Time
	OccurrenceCount     int64
	NextOccurrenceAt    *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// RoleScope names what a role assignment hangs off. Exactly one field is
// set, which is the same invariant the event_roles CHECK enforces.
type RoleScope struct {
	EventID  string
	SeriesID string
}

// Valid reports whether exactly one of the two ids is set.
func (s RoleScope) Valid() bool {
	return (s.EventID == "") != (s.SeriesID == "")
}

// EventRoleAssignment is one event_roles row joined to the person it names.
type EventRoleAssignment struct {
	EventID        *string
	SeriesID       *string
	UserID         string
	Handle         string
	LinkkeysDomain string
	DisplayName    string
	Role           EventRole
	AssignedAt     time.Time
}

// Attendee is one event_attendance row joined to the person it names.
type Attendee struct {
	UserID         string
	Handle         string
	LinkkeysDomain string
	DisplayName    string
	MarkedAt       time.Time
}

// GatheringAccess is what one person's standing in one gathering resolves
// to, answered in a single round trip because every permission check needs
// all of it at once.
//
// IsOwner is true for a direct individual owner AND for an owner of any
// organization that owns the gathering. That indirection is the whole reason
// organization ownership exists, so it is resolved here rather than left to
// callers.
type GatheringAccess struct {
	IsMember bool
	IsOwner  bool
}

// Page is a limit and an offset, already clamped by the caller.
type Page struct {
	Limit  int64
	Offset int64
}

// GeoBox is a latitude/longitude rectangle. Proximity search prefilters on
// one of these in SQL and then refines it to a circle in Go, which is what
// keeps the two backends identical — see csil/types/search.csil.
type GeoBox struct {
	MinLatitude  float64
	MaxLatitude  float64
	MinLongitude float64
	MaxLongitude float64
}

// PlaceFilter is the place half of a query. Text fields match on
// case-insensitive equality against the stored lowercase value, so the
// caller lowercases before it gets here.
type PlaceFilter struct {
	Locality string
	Region   string
	Country  string
	Box      *GeoBox
}

// IsEmpty reports whether this filter constrains nothing.
func (p PlaceFilter) IsEmpty() bool {
	return p.Locality == "" && p.Region == "" && p.Country == "" && p.Box == nil
}

// OrganizationInput is a complete set of an organization's own fields. Updates are
// read-modify-write rather than partial, so that search_text — which is
// derived from all three text fields at once — is never written from a
// half-known row.
type OrganizationInput struct {
	Slug          string
	OriginDomain  string
	Name          string
	Blurb         string
	Description   string
	SearchText    string
	PublishEvents PublishSetting
}

// GatheringInput is a complete set of a gathering's own fields.
type GatheringInput struct {
	Slug          string
	OriginDomain  string
	Name          string
	Blurb         string
	Description   string
	SearchText    string
	PublishEvents PublishSetting
}

// EventInput is a complete set of an event's own fields. UpsertOccurrences
// passes a slice of these, which is why materializing a series needs no
// second shape.
type EventInput struct {
	GatheringID string
	SeriesID    *string
	Title       string
	Description string
	SearchText  string
	IsOnline    bool
	IsInPerson  bool
	OnlineURL   string
	Location    Location
	StartsAt    time.Time
	EndsAt      time.Time
	Timezone    string
}

// EventSeriesInput is a complete set of a series' own fields.
type EventSeriesInput struct {
	GatheringID         string
	Title               string
	Description         string
	SearchText          string
	IsOnline            bool
	IsInPerson          bool
	OnlineURL           string
	Location            Location
	Recurrence          Recurrence
	StartsOn            time.Time
	EndsOn              *time.Time
	StartTime           string
	DurationMinutes     int64
	Timezone            string
	MaterializedThrough time.Time
}

// OrganizationFilter selects organizations. Query is already lowercased and
// matched as a substring of search_text.
type OrganizationFilter struct {
	Query string
	// MemberID restricts to organizations this person belongs to. Empty means no
	// restriction.
	MemberID string
	Page     Page
}

// GatheringFilter selects gatherings.
type GatheringFilter struct {
	Query string
	// MemberID restricts to gatherings this person belongs to or owns,
	// following organization ownership as well as direct ownership.
	MemberID string
	// OwnedByOrganization restricts to gatherings this organization owns.
	OwnedByOrganization string
	Place               PlaceFilter
	Page                Page
}

// EventFilter selects events. It is the one filter both the listing ops and
// search use: a search for events IS a listing with more predicates set, and
// giving them separate queries would only guarantee they answer differently.
type EventFilter struct {
	Query       string
	GatheringID string
	// SeriesID restricts to the occurrences of one series. An occurrence is
	// an ordinary event, so this is a predicate on `events` like any other —
	// it is not a second way of reading a series.
	SeriesID string
	// AttendeeID restricts to events this person has marked attendance on.
	AttendeeID string
	// MemberID restricts to events under gatherings this person belongs to
	// or owns. Wider than AttendeeID: what is on for my groups, rather than
	// what I have said I am coming to.
	MemberID string
	// OwnedByOrganization restricts to events under the gatherings one
	// organization owns.
	OwnedByOrganization string
	Place               PlaceFilter
	// StartsAfter and StartsBefore bound starts_at. A zero time means
	// unbounded on that side.
	StartsAfter  time.Time
	StartsBefore time.Time
	OnlineOnly   bool
	InPersonOnly bool
	// IncludeStarted admits events whose starts_at has passed. Off by
	// default: the common question is about what is still to come.
	IncludeStarted bool
	// Now is the instant "started" is measured against. Passed in rather
	// than read from the clock inside the store so a test can pin it.
	Now  time.Time
	Page Page
}

// EventSeriesFilter selects series.
type EventSeriesFilter struct {
	Query       string
	GatheringID string
	Place       PlaceFilter
	Page        Page
}

// ---------------------------------------------------------------------------
// Federation
// ---------------------------------------------------------------------------

// PeerStatus is a peer's standing in ONE direction. A peer row carries two
// of these, because accepting what somebody sends and publishing to them
// are two decisions.
type PeerStatus string

const (
	// PeerStatusNone is the resting state: no agreement either way.
	PeerStatusNone PeerStatus = "none"
	// PeerStatusPending is a request nobody has answered.
	PeerStatusPending PeerStatus = "pending"
	// PeerStatusApproved is the only status that moves data.
	PeerStatusApproved PeerStatus = "approved"
	// PeerStatusBlocked is a decision. It differs from none in surviving a
	// further request.
	PeerStatusBlocked PeerStatus = "blocked"
)

// ValidPeerStatus reports whether s is one of the four.
func ValidPeerStatus(s PeerStatus) bool {
	switch s {
	case PeerStatusNone, PeerStatusPending, PeerStatusApproved, PeerStatusBlocked:
		return true
	}
	return false
}

// PeerIdentity is a peer's canonical linkkeys identity: one account, one
// application, one application instance. It is what a delivery is actually
// checked against — never `Address` (`handle@domain`), which can move to a
// different account or be reused. All four fields are set together or not
// at all; see Empty.
type PeerIdentity struct {
	SubjectUserID string
	SubjectDomain string
	ApplicationID string
	InstanceID    string
}

// Empty reports whether no field of this identity is set — the state of a
// peer this instance has never captured a canonical identity for.
func (p PeerIdentity) Empty() bool {
	return p.SubjectUserID == "" && p.SubjectDomain == "" && p.ApplicationID == "" && p.InstanceID == ""
}

// Peer is another instance, as this one sees it.
type Peer struct {
	ID      string
	Address string
	Handle  string
	Domain  string
	// Identity is this peer's canonical linkkeys identity, resolved and
	// stored the first time a signed request from this address verifies,
	// or set by an administrator at approval time. Empty until then — see
	// PeerIdentity.
	Identity          PeerIdentity
	BaseURL           string
	InboundStatus     PeerStatus
	OutboundStatus    PeerStatus
	Note              string
	SuspendedAt       *time.Time
	FirstFailureAt    *time.Time
	LastFailureAt     *time.Time
	LastFailureReason string
	LastSuccessAt     *time.Time
	PendingDeliveries int64
	// RateLimitPerMinute is this peer's own allowance. Nil means the
	// instance-wide limit applies, so raising that raises it for every peer
	// that has not been given its own.
	RateLimitPerMinute *int64
	// RateLimitedTotal is how many events this peer has ever had refused
	// for exceeding its allowance. An administrator needs to see that a
	// peer is being throttled, not merely that it has gone quiet.
	RateLimitedTotal int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Suspended reports whether delivery to this peer has stopped. A suspended
// peer is skipped by the sender until an administrator restarts it.
func (p Peer) Suspended() bool { return p.SuspendedAt != nil }

// PeerInput is what creating or amending a peer needs. Identity is left
// zero for a peer whose canonical identity is not yet known — CreatePeer
// only stores it when the caller already has it (a verified peering
// request); an administrator can set or replace it later with
// SetPeerStatus.
type PeerInput struct {
	Address  string
	Handle   string
	Domain   string
	BaseURL  string
	Identity PeerIdentity
	Note     string
}

// OutboxItem is one queued delivery. Payload is the already-signed
// envelope: it is built at enqueue time so that a retry sends the same
// bytes the first attempt did, which is what makes a signature verifiable
// after a delay.
type OutboxItem struct {
	ID            string
	PeerID        string
	PeerAddress   string
	PeerBaseURL   string
	EventID       string
	Payload       []byte
	Attempts      int64
	NextAttemptAt time.Time
	LastError     string
	CreatedAt     time.Time
}

// RemoteEvent is one event a peer sent: a summary and a link. It is
// deliberately not an Event — see the migration header.
type RemoteEvent struct {
	ID               string
	PeerID           string
	PeerAddress      string
	RemoteID         string
	OriginDomain     string
	CanonicalURL     string
	Title            string
	IsOnline         bool
	IsInPerson       bool
	Location         Location
	StartsAt         time.Time
	EndsAt           time.Time
	Timezone         string
	GatheringName    string
	OrganizationName string
	ReceivedAt       time.Time
}

// RemoteEventInput is one row of a delivered batch, ready to store.
type RemoteEventInput struct {
	RemoteID         string
	OriginDomain     string
	CanonicalURL     string
	Title            string
	SearchText       string
	IsOnline         bool
	IsInPerson       bool
	Location         Location
	StartsAt         time.Time
	EndsAt           time.Time
	Timezone         string
	GatheringName    string
	OrganizationName string
}

// RemoteEventFilter selects what peers have sent. It mirrors EventFilter,
// minus everything that only means something for a local event.
type RemoteEventFilter struct {
	Query        string
	Place        PlaceFilter
	StartsAfter  time.Time
	StartsBefore time.Time
	OnlineOnly   bool
	InPersonOnly bool
	// IncludeStarted admits events whose starts_at has passed.
	IncludeStarted bool
	Now            time.Time
	Page           Page
}

// ---------------------------------------------------------------------------
// Instance settings, publishing, and rate limiting
// ---------------------------------------------------------------------------

// PublishSetting is one level's answer to "do these events go to peer
// directories". PublishUnset is a THIRD state, not a synonym for out: it
// means this level said nothing and the level above decides. A bool cannot
// carry that, which is why this is a string.
type PublishSetting string

const (
	// PublishUnset defers to the level above.
	PublishUnset PublishSetting = ""
	// PublishIn publishes.
	PublishIn PublishSetting = "in"
	// PublishOut does not.
	PublishOut PublishSetting = "out"
)

// ValidPublishSetting reports whether s is one this store will write.
func ValidPublishSetting(s PublishSetting) bool {
	return s == PublishUnset || s == PublishIn || s == PublishOut
}

// InstanceSettings are the values an administrator changes while the
// service runs. They live in the database rather than the environment so a
// change needs no restart and every replica sees the same value.
type InstanceSettings struct {
	// PublishDefault is what a gathering publishes when nothing below it
	// has said otherwise.
	PublishDefault PublishSetting
	// OrganizationOverrideAllowed and GatheringOverrideAllowed say which
	// levels may disagree with the default.
	OrganizationOverrideAllowed bool
	GatheringOverrideAllowed    bool
	// RetentionDays is how long a directory keeps what a peer sent, after
	// the event ends. Zero keeps everything.
	RetentionDays int64
	// PeerRateLimitPerMinute is how many events one peer may have accepted
	// per minute. Zero means no limit.
	PeerRateLimitPerMinute int64
	// OriginRateLimitPerMinute is the same question for one ORGANIZATION
	// inside a peer.
	//
	// Both levels exist because a peer's allowance is shared. Without this
	// one, a single organization inside a peer can spend the whole budget,
	// and that peer's other organizations are refused for something they
	// did not do.
	OriginRateLimitPerMinute int64
}

// DefaultInstanceSettings is what an instance that has set nothing behaves
// as. Publishing defaults to IN: an instance that has switched federation
// on and approved a directory has already made the interesting choice, and
// a default of "out" would make that approval do nothing.
func DefaultInstanceSettings() InstanceSettings {
	return InstanceSettings{
		PublishDefault:              PublishIn,
		OrganizationOverrideAllowed: true,
		GatheringOverrideAllowed:    true,
		RetentionDays:               365,
		PeerRateLimitPerMinute:      60,
		// Below the peer limit on purpose. An origin limit at or above the
		// peer limit can never bind, because the peer's own check refuses
		// the batch first.
		OriginRateLimitPerMinute: 20,
	}
}

// RateVerdict is what a rate-limit check answered: how many of the events
// asked for are allowed, and how many were refused.
type RateVerdict struct {
	Allowed int64
	Refused int64
}

// OriginVolume is how much one originating organization has sent, with its
// peer's throttle state alongside.
//
// The limit is enforced per peer; this is what says which organization
// inside a peer is responsible for the volume. An operator reading it can
// act on the origin rather than on the whole peer.
type OriginVolume struct {
	PeerID           string
	PeerAddress      string
	OrganizationName string
	// Held is what the directory currently has from this organization.
	Held int64
	// AcceptedTotal is everything ever accepted from it.
	AcceptedTotal int64
	// AcceptedThisMinute is measured in the same fixed window the peer's
	// limiter uses, so the two numbers on one screen mean the same thing.
	AcceptedThisMinute int64
	LastReceivedAt     *time.Time

	PeerRateLimitPerMinute int64
	PeerRateLimitedTotal   int64
	PeerSuspended          bool

	// RateLimitPerMinute is this origin's own allowance. Nil uses the
	// instance-wide origin limit.
	RateLimitPerMinute *int64
	// EffectiveRateLimitPerMinute is what is actually in force.
	EffectiveRateLimitPerMinute int64
	// RateLimitedTotal counts refusals against THIS origin, as distinct
	// from the peer's.
	RateLimitedTotal int64
}

// GatheringOfferStatus is where a two-sided move stands.
type GatheringOfferStatus string

const (
	OfferPending   GatheringOfferStatus = "pending"
	OfferAccepted  GatheringOfferStatus = "accepted"
	OfferDeclined  GatheringOfferStatus = "declined"
	OfferWithdrawn GatheringOfferStatus = "withdrawn"
)

// GatheringOffer is one gathering offered to one organization.
//
// The names and the offerer's handle are carried alongside the ids because
// every screen that shows an offer shows all three, and a directory that
// spans domains has to show which domain each of them is from.
type GatheringOffer struct {
	ID               string
	GatheringID      string
	GatheringName    string
	OrganizationID   string
	OrganizationName string
	OfferedByID      string
	OfferedByHandle  string
	OfferedByName    string
	OfferedByDomain  string
	Note             string
	Status           GatheringOfferStatus
	CreatedAt        time.Time
	ResolvedAt       *time.Time
}

// WebhookOwnerKind is the level a webhook hangs off.
type WebhookOwnerKind string

const (
	WebhookOwnerOrganization WebhookOwnerKind = "organization"
	WebhookOwnerGathering    WebhookOwnerKind = "gathering"
)

// WebhookScope is how much of what happens underneath is reported.
type WebhookScope string

const (
	// WebhookScopeAll reports everything under the level, events included.
	WebhookScopeAll WebhookScope = "all"
	// WebhookScopeStructure reports the level and its gatherings, and no
	// events at all.
	WebhookScopeStructure WebhookScope = "structure_only"
)

// Webhook is one outbound notification endpoint.
//
// Secret is the HMAC key and never leaves the server except in the reply to
// the call that created it.
type Webhook struct {
	ID        string
	OwnerKind WebhookOwnerKind
	OwnerID   string
	URL       string
	Secret    string
	Scope     WebhookScope
	Note      string
	Active    bool
	// IncludeDetails sends the record rather than a pointer to it. The
	// owner's own choice, made with a warning in front of them.
	IncludeDetails bool
	FailureCount   int64
	LastStatus     *int64
	LastAttemptAt  *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// WebhookInput is what an owner sets. The server owns everything else.
type WebhookInput struct {
	URL            string
	Scope          WebhookScope
	Note           string
	Active         bool
	IncludeDetails bool
}

// WebhookDelivery is one queued POST.
type WebhookDelivery struct {
	ID        string
	WebhookID string
	Payload   []byte
	Attempts  int64
	URL       string
	Secret    string
}

// WebhookAudience splits the webhooks a change matched by what they are
// allowed to be told. The dispatcher builds one body for each half rather
// than one for each webhook.
type WebhookAudience struct {
	Pointer  []Webhook
	Detailed []Webhook
}
