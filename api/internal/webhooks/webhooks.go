// Package webhooks turns a change into an outbound POST.
//
// Two halves, deliberately separate:
//
//   - The DISPATCHER runs inside the request that made the change. It finds
//     the webhooks that want to hear about it and writes one row for each.
//     It never opens a socket: a request that changed something must not
//     wait on somebody else's server, and a notification must not be lost
//     because that server was slow.
//
//   - The SENDER drains those rows on its own clock, with retries and
//     backoff, and switches off an endpoint that has failed too many times
//     in a row.
//
// This is not federation. A webhook points at a URL its owner chose, it
// brings nothing in, and it carries no peer's signature — the HMAC here
// only tells the receiver that the POST came from this instance. The two
// must not be confused, which is why they are separate packages with
// separate tables.
package webhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

// Action is what happened. A cancellation is not a deletion: a cancelled
// event still exists and still shows, and a receiver has to be able to tell
// that from a record that went away.
type Action string

const (
	ActionCreated   Action = "created"
	ActionUpdated   Action = "updated"
	ActionCancelled Action = "cancelled"
	ActionDeleted   Action = "deleted"
)

// Subject is what it happened to.
type Subject string

const (
	SubjectOrganization Subject = "organization"
	SubjectGathering    Subject = "gathering"
	SubjectEvent        Subject = "event"
	SubjectSeries       Subject = "series"
)

// Change is one thing that happened, with enough context to route it.
//
// OrganizationIDs is every organization that owns the gathering this change
// sits under — ownership is many-to-many, so a change can be reportable to
// more than one organization at once, and each of them decides for itself
// through its own webhooks.
type Change struct {
	Action  Action
	Subject Subject

	// ID is the thing that changed.
	ID   string
	Name string

	// GatheringID is the gathering this sits under, empty when the subject
	// IS an organization.
	GatheringID string

	// OrganizationIDs are the organizations that own that gathering, or the
	// organization itself when the subject is one.
	OrganizationIDs []string

	// URL is where a reader can see the thing, when there is somewhere.
	URL string

	// Details is the record itself, for the webhooks whose owner asked for
	// it. It is built by the service that made the change — the only place
	// that has the record — and it is dropped for every webhook that did
	// not ask.
	Details *Details

	At time.Time
}

// Details is what a reader of the record would see.
//
// It is sent only to a webhook whose owner switched `include_details` on,
// having been shown what it means. That is their information to send: they
// wrote it, and a directory that refused to hand them their own description
// would be pretending to a control it does not have. The default stays off,
// because the URL is one this server has no say over.
type Details struct {
	Description string     `json:"description,omitempty"`
	Blurb       string     `json:"blurb,omitempty"`
	StartsAt    *time.Time `json:"starts_at,omitempty"`
	EndsAt      *time.Time `json:"ends_at,omitempty"`
	Timezone    string     `json:"timezone,omitempty"`
	IsOnline    bool       `json:"is_online,omitempty"`
	IsInPerson  bool       `json:"is_in_person,omitempty"`
	OnlineURL   string     `json:"online_url,omitempty"`
	Location    *Location  `json:"location,omitempty"`
	// Recurrence is a rule said in the schema's own terms, not a sentence:
	// a receiver renders it, and this server does not know the language of
	// whoever reads it.
	Recurrence *Recurrence `json:"recurrence,omitempty"`
}

// Location is the postal half of an in-person event.
type Location struct {
	Name       string `json:"name,omitempty"`
	Address    string `json:"address,omitempty"`
	Locality   string `json:"locality,omitempty"`
	Region     string `json:"region,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
	Country    string `json:"country,omitempty"`
}

// Recurrence is the structured rule behind a series.
type Recurrence struct {
	Freq       string `json:"freq"`
	Interval   int64  `json:"interval"`
	Ordinal    *int64 `json:"ordinal,omitempty"`
	Weekday    string `json:"weekday,omitempty"`
	DayOfMonth *int64 `json:"day_of_month,omitempty"`
	// StartTime is the local clock reading the rule names, with Timezone.
	// The two are the RULE and cannot be flattened into an instant: the same
	// rule is a different UTC time in summer and in winter.
	StartTime string `json:"start_time,omitempty"`
	Timezone  string `json:"timezone,omitempty"`
}

// payload is the JSON body a receiver gets.
//
// By default it says WHAT changed and where to look, and nothing more: the
// URL belongs to somebody this server has no say over. A webhook whose owner
// switched `include_details` on gets the record as well — see Details.
type payload struct {
	Action    Action    `json:"action"`
	Subject   Subject   `json:"subject"`
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Gathering string    `json:"gathering_id,omitempty"`
	URL       string    `json:"url,omitempty"`
	At        time.Time `json:"at"`
	// Instance is this node's own domain, so a receiver listening to more
	// than one tinku can tell them apart.
	Instance string `json:"instance"`
	// Details is present only for a webhook whose owner asked for it.
	Details *Details `json:"details,omitempty"`
}

// Dispatcher writes deliveries for the webhooks that want a change.
type Dispatcher struct {
	Store        store.Store
	OriginDomain string
	// PublicBaseURL is where a reader reaches this instance, used to build
	// the link in a delivery. Empty leaves the link out rather than
	// inventing a URL that does not resolve.
	PublicBaseURL string
}

// Dispatch queues one change to every webhook that should hear about it.
//
// A failure here is logged and swallowed. The change has already happened;
// refusing the request that made it because a notification could not be
// queued would be reporting the wrong thing to the wrong person.
func (d *Dispatcher) Dispatch(ctx context.Context, change Change) {
	if d == nil || d.Store == nil {
		return
	}
	if err := d.dispatch(ctx, change); err != nil {
		log.WithError(err).WithFields(log.Fields{
			"subject": change.Subject,
			"action":  change.Action,
			"id":      change.ID,
		}).Warn("could not queue webhook deliveries")
	}
}

func (d *Dispatcher) dispatch(ctx context.Context, change Change) error {
	if change.At.IsZero() {
		change.At = time.Now()
	}

	hooks, err := d.matching(ctx, change)
	if err != nil {
		return err
	}
	if len(hooks) == 0 {
		return nil
	}

	base := payload{
		Action:    change.Action,
		Subject:   change.Subject,
		ID:        change.ID,
		Name:      change.Name,
		Gathering: change.GatheringID,
		URL:       change.URL,
		At:        change.At.UTC(),
		Instance:  d.OriginDomain,
	}

	// Two bodies at most, whatever the number of webhooks: one carrying the
	// record for the owners who asked for it, one carrying a pointer for
	// everybody else. Marshalling per webhook would be the same two answers
	// computed five times.
	var pointerBody, detailedBody []byte
	for _, hook := range hooks {
		body := pointerBody
		if hook.IncludeDetails && change.Details != nil {
			if detailedBody == nil {
				withDetails := base
				withDetails.Details = change.Details
				if detailedBody, err = json.Marshal(withDetails); err != nil {
					return err
				}
			}
			body = detailedBody
		} else {
			if pointerBody == nil {
				if pointerBody, err = json.Marshal(base); err != nil {
					return err
				}
			}
			body = pointerBody
		}
		if err := d.Store.QueueWebhookDelivery(ctx, hook.ID, body, change.At); err != nil {
			return err
		}
	}
	return nil
}

// matching is the routing rule, in one place.
//
// A webhook on a gathering hears about that gathering and what is under it.
// A webhook on an organization hears about the organization, the gatherings
// it owns, and — unless it is scoped to structure only — their events.
func (d *Dispatcher) matching(ctx context.Context, change Change) ([]store.Webhook, error) {
	out := []store.Webhook{}

	if change.GatheringID != "" {
		hooks, err := d.Store.ListWebhooks(ctx, store.WebhookOwnerGathering, change.GatheringID)
		if err != nil {
			return nil, err
		}
		for _, hook := range hooks {
			if wants(hook, change) {
				out = append(out, hook)
			}
		}
	}

	for _, organizationID := range change.OrganizationIDs {
		hooks, err := d.Store.ListWebhooks(ctx, store.WebhookOwnerOrganization, organizationID)
		if err != nil {
			return nil, err
		}
		for _, hook := range hooks {
			if wants(hook, change) {
				out = append(out, hook)
			}
		}
	}
	return out, nil
}

// wants applies the scope. `structure_only` is the setting for an
// integration that tracks what EXISTS rather than what is scheduled, so it
// drops events and series and keeps everything else.
func wants(hook store.Webhook, change Change) bool {
	if !hook.Active {
		return false
	}
	if hook.Scope == store.WebhookScopeStructure {
		switch change.Subject {
		case SubjectEvent, SubjectSeries:
			return false
		}
	}
	return true
}

// Sign is the HMAC-SHA256 of the body, hex, as sent in X-Tinku-Signature.
//
// A receiver that does not check it cannot tell a delivery from anybody
// else's POST to the same URL, which is why the secret is minted for every
// webhook whether or not the owner intends to use it.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
