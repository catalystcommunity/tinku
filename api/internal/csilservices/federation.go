package csilservices

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/federation"
	"github.com/catalystcommunity/tinku/api/internal/store"
)

// FederationService implements csil.FederationService.
//
// Its ops are authenticated two different ways, which is the thing to hold
// on to when reading this file:
//
//   - The administrative ops need an administrator's session on THIS
//     instance. They are how a person here decides who to trust.
//   - deliver-events and request-peering are called by another INSTANCE.
//     They carry no session at all; a signature over opaque bytes is the
//     authentication, and it is checked before anything is written.
//
// The service is registered only when federation is switched on. When it is
// off the wire answers "unknown service or op", which is the honest answer
// for an instance that does not federate.
type FederationService struct {
	Store store.Store
	// OriginDomain is this instance's own name, which is what decides
	// whether a record's domain is foreign.
	OriginDomain string
	// Signer is this instance's identity. Its address is what a peer knows
	// us as.
	Signer federation.Signer
	// Verifier checks a delivery (DeliverEvents) — every check under
	// federation.BatchSignatureTag. PeeringVerifier checks a peering
	// request (RequestPeering) — every check under
	// federation.PeeringSignatureTag. They are separate fields, not one
	// Verifier reused for both, because the two operations must never
	// accept a signature made for the other — see sign.go's doc comment on
	// why signature contexts are not interchangeable. The dev scheme's
	// Verifier ignores context and can be used for both.
	Verifier        federation.Verifier
	PeeringVerifier federation.Verifier
	// SubjectUserID, SubjectDomain, ApplicationID and InstanceID are this
	// instance's OWN canonical linkkeys identity — what FederationIdentity
	// reports, so a peer administrator adding this instance can record the
	// identity to approve rather than only the address.
	SubjectUserID string
	SubjectDomain string
	ApplicationID string
	InstanceID    string
	Now           func() time.Time
}

var _ csil.FederationService = (*FederationService)(nil)

// FreshnessWindow is how far a delivery's own timestamp may be from this
// instance's clock, in either direction.
//
// Both directions matter: too old is a replay, and too far ahead is a
// sender whose clock is wrong — accepting one would mean remembering its
// batch id until that future arrived. An hour is generous for clock skew
// between servers and short enough that the remembered ids stay a small
// table.
const FreshnessWindow = time.Hour

func (s *FederationService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// admin is the gate on every op a person calls here.
func (s *FederationService) admin(ctx context.Context, action string) (caller, error) {
	c, err := authenticated(ctx, action)
	if err != nil {
		return c, err
	}
	if !c.IsAdmin {
		return c, Forbidden("only an administrator can " + action)
	}
	return c, nil
}

// FederationIdentity is what a peer needs in order to address this
// instance. Anonymous callers are allowed: an address is a name, not a
// secret, and somebody has to be able to read it before they can ask for
// peering.
func (s *FederationService) FederationIdentity(ctx context.Context, _ csil.Empty) (csil.FederationIdentity, error) {
	return csil.FederationIdentity{
		Address:       s.Signer.Address(),
		Enabled:       true,
		Algorithm:     s.Signer.Algorithm(),
		SubjectUserId: s.SubjectUserID,
		SubjectDomain: s.SubjectDomain,
		ApplicationId: s.ApplicationID,
		InstanceId:    s.InstanceID,
	}, nil
}

func (s *FederationService) ListPeers(ctx context.Context, req csil.ListPeersRequest) (csil.PeerList, error) {
	if _, err := s.admin(ctx, "list peers"); err != nil {
		return csil.PeerList{}, err
	}
	peers, total, err := s.Store.ListPeers(ctx, pageOf(req.Page))
	if err != nil {
		return csil.PeerList{}, err
	}
	out := make([]csil.Peer, 0, len(peers))
	for i := range peers {
		out = append(out, toPeer(&peers[i]))
	}
	return csil.PeerList{Peers: out, Total: uint64(total)}, nil
}

// AddPeer registers a directory to publish to and asks it to accept us.
//
// Our outbound status goes to `pending`, not `approved`: wanting to publish
// somewhere is not the same as being allowed to, and the peer's answer is
// what settles it. The request itself is sent by the sender, not here — a
// peer that is unreachable right now must not stop an administrator from
// recording it.
func (s *FederationService) AddPeer(ctx context.Context, req csil.AddPeerRequest) (csil.Peer, error) {
	if _, err := s.admin(ctx, "add a peer"); err != nil {
		return csil.Peer{}, err
	}
	handle, domain, ok := federation.SplitAddress(req.Address)
	if !ok {
		return csil.Peer{}, Invalid("address", "a peer address looks like handle@domain")
	}
	address := handle + "@" + domain
	if address == strings.ToLower(s.Signer.Address()) {
		return csil.Peer{}, Invalid("address", "this instance cannot be its own peer")
	}
	if strings.TrimSpace(req.BaseUrl) == "" {
		return csil.Peer{}, Invalid("base_url", "a peer needs a base URL to be reached at")
	}

	if existing, err := s.Store.PeerByAddress(ctx, address); err == nil {
		return csil.Peer{}, Invalid("address", "that peer is already recorded as "+string(existing.InboundStatus)+
			" inbound and "+string(existing.OutboundStatus)+" outbound")
	} else if !errors.Is(err, store.ErrNotFound) {
		return csil.Peer{}, err
	}

	peer, err := s.Store.CreatePeer(ctx, store.PeerInput{
		Address: address,
		Handle:  handle,
		Domain:  domain,
		BaseURL: strings.TrimSpace(req.BaseUrl),
		Note:    strings.TrimSpace(req.Note),
	})
	if err != nil {
		return csil.Peer{}, err
	}
	pending := store.PeerStatusPending
	updated, err := s.Store.SetPeerStatus(ctx, peer.ID, nil, &pending, nil)
	if err != nil {
		return csil.Peer{}, err
	}
	return toPeer(updated), nil
}

// SetPeerStatus answers a peer's request, or changes our mind about one.
// The two directions move independently: approving what a peer sends us has
// never implied publishing to them.
//
// Approving inbound REQUIRES the peer to have a canonical identity — either
// already stored (captured earlier by RequestPeering, or set here in an
// earlier call), or supplied in this same request. This is the rule that
// makes "resolve and store the peer's canonical identity during approval"
// a fact about every approved peer, not a convention an administrator has
// to remember: DeliverEvents verifies a batch against exactly this stored
// identity, never against `address` — see verifiedPeer.
func (s *FederationService) SetPeerStatus(ctx context.Context, req csil.SetPeerStatusRequest) (csil.Peer, error) {
	if _, err := s.admin(ctx, "change a peer's status"); err != nil {
		return csil.Peer{}, err
	}
	var inbound, outbound *store.PeerStatus
	if req.InboundStatus != nil {
		v := store.PeerStatus(*req.InboundStatus)
		if !store.ValidPeerStatus(v) {
			return csil.Peer{}, Invalid("inbound_status", "a status is none, pending, approved or blocked")
		}
		inbound = &v
	}
	if req.OutboundStatus != nil {
		v := store.PeerStatus(*req.OutboundStatus)
		if !store.ValidPeerStatus(v) {
			return csil.Peer{}, Invalid("outbound_status", "a status is none, pending, approved or blocked")
		}
		outbound = &v
	}

	identity, err := identityFromRequest(req)
	if err != nil {
		return csil.Peer{}, err
	}
	if inbound == nil && outbound == nil && identity == nil {
		return csil.Peer{}, Invalid("inbound_status", "name at least one direction, or an identity, to change")
	}

	if inbound != nil && *inbound == store.PeerStatusApproved {
		resulting := identity
		if resulting == nil {
			current, err := s.Store.PeerByID(ctx, req.PeerId)
			if errors.Is(err, store.ErrNotFound) {
				return csil.Peer{}, NotFound("peer", "no peer with that id")
			}
			if err != nil {
				return csil.Peer{}, err
			}
			resulting = &current.Identity
		}
		if resulting.Empty() {
			return csil.Peer{}, Invalid("subject_user_id",
				"a peer needs its canonical identity resolved before it can be approved inbound")
		}
	}

	peer, err := s.Store.SetPeerStatus(ctx, req.PeerId, inbound, outbound, identity)
	if errors.Is(err, store.ErrNotFound) {
		return csil.Peer{}, NotFound("peer", "no peer with that id")
	}
	if err != nil {
		return csil.Peer{}, err
	}
	return toPeer(peer), nil
}

// identityFromRequest reads the four identity fields as all-or-nothing:
// nil when none are set (leave the peer's stored identity alone), a
// pointer to the four values when every one is set, and Invalid when only
// some are — a partial identity is never a valid thing to store.
func identityFromRequest(req csil.SetPeerStatusRequest) (*store.PeerIdentity, error) {
	fields := []*string{req.SubjectUserId, req.SubjectDomain, req.ApplicationId, req.InstanceId}
	present := 0
	for _, f := range fields {
		if f != nil {
			present++
		}
	}
	if present == 0 {
		return nil, nil
	}
	if present != len(fields) {
		return nil, Invalid("subject_user_id", "give all four identity fields together, or none")
	}
	identity := store.PeerIdentity{
		SubjectUserID: strings.TrimSpace(*req.SubjectUserId),
		SubjectDomain: strings.TrimSpace(*req.SubjectDomain),
		ApplicationID: strings.TrimSpace(*req.ApplicationId),
		InstanceID:    strings.TrimSpace(*req.InstanceId),
	}
	if identity.SubjectUserID == "" || identity.SubjectDomain == "" ||
		identity.ApplicationID == "" || identity.InstanceID == "" {
		return nil, Invalid("subject_user_id", "an identity field cannot be empty when given")
	}
	return &identity, nil
}

// ResumePeer is the button. A suspension is a decision that a person has to
// undo, because the thing that caused it is a thing a person has to look
// at — an endpoint that moved, a certificate that expired, a peer that
// stopped existing.
func (s *FederationService) ResumePeer(ctx context.Context, req csil.ResumePeerRequest) (csil.Peer, error) {
	if _, err := s.admin(ctx, "restart a peer"); err != nil {
		return csil.Peer{}, err
	}
	if _, err := s.Store.PeerByID(ctx, req.PeerId); errors.Is(err, store.ErrNotFound) {
		return csil.Peer{}, NotFound("peer", "no peer with that id")
	} else if err != nil {
		return csil.Peer{}, err
	}
	if err := s.Store.ResumePeer(ctx, req.PeerId, s.now()); err != nil {
		return csil.Peer{}, err
	}
	peer, err := s.Store.PeerByID(ctx, req.PeerId)
	if err != nil {
		return csil.Peer{}, err
	}
	return toPeer(peer), nil
}

func (s *FederationService) RemovePeer(ctx context.Context, req csil.RemovePeerRequest) (csil.Empty, error) {
	if _, err := s.admin(ctx, "remove a peer"); err != nil {
		return csil.Empty{}, err
	}
	if _, err := s.Store.PeerByID(ctx, req.PeerId); errors.Is(err, store.ErrNotFound) {
		return csil.Empty{}, NotFound("peer", "no peer with that id")
	} else if err != nil {
		return csil.Empty{}, err
	}
	if err := s.Store.DeletePeer(ctx, req.PeerId); err != nil {
		return csil.Empty{}, err
	}
	return csil.Empty{}, nil
}

// SetPeerRateLimit gives one peer its own allowance, or restores the
// instance-wide one. A limit of zero means no limit for that peer, which is
// how a directory admits a trusted bulk publisher without raising the
// ceiling for everybody.
func (s *FederationService) SetPeerRateLimit(ctx context.Context, req csil.SetPeerRateLimitRequest) (csil.Peer, error) {
	if _, err := s.admin(ctx, "change a peer's rate limit"); err != nil {
		return csil.Peer{}, err
	}
	var limit *int64
	if req.RateLimitPerMinute != nil {
		value := int64(*req.RateLimitPerMinute)
		limit = &value
	}
	peer, err := s.Store.SetPeerRateLimit(ctx, req.PeerId, limit)
	if errors.Is(err, store.ErrNotFound) {
		return csil.Peer{}, NotFound("peer", "no peer with that id")
	}
	if err != nil {
		return csil.Peer{}, err
	}
	return toPeer(peer), nil
}

// ---- Called by another instance -------------------------------------------

// DeliverEvents accepts a signed batch.
//
// The order of the checks is the security of this op, so it is worth
// stating: the signature is verified against the bytes as received BEFORE
// they are decoded, the claimed origin is then checked against the verified
// signer, and only an approved inbound peer gets anything written. A caller
// who fails any of these learns only that they were refused.
func (s *FederationService) DeliverEvents(ctx context.Context, req csil.SignedDelivery) (csil.DeliveryReceipt, error) {
	peer, err := s.verifiedPeer(ctx, req.SenderAddress, req.Algorithm, req.KeyId, req.Body, []byte(req.Signature))
	if err != nil {
		return csil.DeliveryReceipt{}, err
	}
	if peer.InboundStatus != store.PeerStatusApproved {
		// Same answer whether they are pending, blocked or unknown: a peer
		// that is not admitted learns nothing about why.
		return csil.DeliveryReceipt{}, Forbidden("this instance does not accept deliveries from you")
	}

	batch, err := csil.DecodeEventBatch(req.Body)
	if err != nil {
		return csil.DeliveryReceipt{}, Invalid("body", "the signed body is not an event batch")
	}
	// A verified signer cannot speak for a domain that is not theirs.
	if !strings.EqualFold(strings.TrimSpace(batch.OriginDomain), peer.Domain) {
		return csil.DeliveryReceipt{}, Forbidden("the batch claims an origin the signature does not support")
	}

	// Replay protection, in two halves that only work together.
	//
	// A signature never expires, so a captured envelope resent later
	// verifies perfectly. Applying it would revert whatever the peer has
	// sent since, and a replayed tombstone would delete an event they
	// republished.
	//
	// The freshness window refuses anything too old or too far ahead. The
	// remembered batch id refuses a second arrival inside that window. The
	// window is also what bounds how long ids must be kept.
	age := s.now().Sub(batch.SentAt)
	if age > FreshnessWindow || age < -FreshnessWindow {
		return csil.DeliveryReceipt{}, Invalid("sent_at", "this delivery is outside the freshness window")
	}
	if strings.TrimSpace(batch.BatchId) == "" {
		return csil.DeliveryReceipt{}, Invalid("batch_id", "a delivery needs a batch id")
	}
	firstTime, err := s.Store.RememberBatch(ctx, peer.ID, batch.BatchId, s.now())
	if err != nil {
		return csil.DeliveryReceipt{}, err
	}
	if !firstTime {
		// Answered as a refusal rather than as success. Reporting "accepted"
		// would tell an honest sender its retry landed when nothing changed.
		return csil.DeliveryReceipt{}, Invalid("batch_id", "this delivery has already been applied")
	}
	// The id is claimed BEFORE the batch is processed, because claiming it
	// afterwards would leave a window in which two copies of a replay both
	// passed the check. If the batch then turns out not to have been
	// applied, the claim is released below — an honest sender must be able
	// to retry what was refused for rate.
	applied := false
	defer func() {
		if applied {
			return
		}
		if err := s.Store.ForgetBatch(ctx, peer.ID, batch.BatchId); err != nil {
			log.WithError(err).WithField("peer", peer.Address).
				Warn("federation: could not release an unapplied batch id")
		}
	}()

	// The allowance is taken BEFORE anything is written, for the whole
	// batch at once. A peer that has lost control of itself gets its
	// minute's worth and the rest is refused — which is the difference
	// between a noisy peer and a peer that fills this instance's directory.
	settings, err := s.Store.InstanceSettings(ctx)
	if err != nil {
		return csil.DeliveryReceipt{}, err
	}
	limit := settings.PeerRateLimitPerMinute
	if peer.RateLimitPerMinute != nil {
		limit = *peer.RateLimitPerMinute
	}
	// A tombstone is charged against NEITHER limit, so the allowance is
	// taken over the UPDATES in the batch alone. Charging a deletion would
	// leave an event on this directory that its origin has removed — and it
	// would also let a run of deletions starve that organization's genuine
	// updates for the rest of the minute.
	var chargeable int64
	for i := range batch.Events {
		if !batch.Events[i].Deleted {
			chargeable++
		}
	}
	verdict, err := s.Store.ConsumePeerAllowance(ctx, peer.ID, chargeable, limit, s.now())
	if err != nil {
		return csil.DeliveryReceipt{}, err
	}

	var accepted, rejected uint64
	var problems []string
	// The allowance is SPENT as the batch is walked in order rather than
	// used to truncate it. Truncating drops the tombstones that fall past
	// the cut along with the updates, which is exactly what a deletion
	// being uncharged is supposed to prevent.
	peerBudget := verdict.Allowed
	rateLimited := verdict.Refused
	if verdict.Refused > 0 {
		problems = append(problems, fmt.Sprintf(
			"%d refused: over the peer limit of %d events a minute", verdict.Refused, limit))
	}

	// The SECOND limit, per originating organization.
	//
	// The peer's allowance is shared. Without this, one organization inside
	// a peer can spend the whole budget and that peer's other organizations
	// are refused for something they did not do. The budget is taken per
	// organization up front, then spent as the batch is walked in order.
	//
	// It is taken over exactly the updates the peer allowance admits — no
	// more, or an organization's minute is spent on events this instance
	// already refused — and over no deletions at all.
	budget := map[string]int64{}
	admissible := peerBudget
	for i := range batch.Events {
		if batch.Events[i].Deleted {
			continue
		}
		if admissible <= 0 {
			break
		}
		admissible--
		budget[batch.Events[i].OrganizationName]++
	}
	for organization, wanted := range budget {
		originVerdict, err := s.Store.ConsumeOriginAllowance(
			ctx, peer.ID, organization, wanted, settings.OriginRateLimitPerMinute, s.now())
		if err != nil {
			return csil.DeliveryReceipt{}, err
		}
		budget[organization] = originVerdict.Allowed
		if originVerdict.Refused > 0 {
			rateLimited += originVerdict.Refused
			problems = append(problems, fmt.Sprintf(
				"%d refused from %q: over the per-organization limit of %d events a minute",
				originVerdict.Refused, organization, settings.OriginRateLimitPerMinute))
		}
	}

	for i := range batch.Events {
		event := batch.Events[i]
		// A tombstone is charged against neither budget and is never
		// skipped for rate: refusing a deletion would leave an event on
		// this directory that its origin has removed, which is worse than
		// accepting one more row.
		if !event.Deleted {
			if peerBudget <= 0 || budget[event.OrganizationName] <= 0 {
				continue
			}
			peerBudget--
			budget[event.OrganizationName]--
		}
		if event.Deleted {
			ok, err := s.Store.DeleteRemoteEvent(ctx, peer.ID, event.RemoteId)
			if err != nil {
				return csil.DeliveryReceipt{}, err
			}
			// A tombstone for something never held is counted as rejected
			// rather than treated as an error: it is a normal consequence
			// of a directory that joined late, not a fault.
			if ok {
				accepted++
			} else {
				rejected++
			}
			continue
		}
		if problem := validateFederatedEvent(event); problem != "" {
			rejected++
			problems = append(problems, event.RemoteId+": "+problem)
			continue
		}
		if err := s.Store.UpsertRemoteEvent(ctx, peer.ID, remoteEventInput(event, peer.Domain)); err != nil {
			return csil.DeliveryReceipt{}, err
		}
		// Counted on accept, so this measures what landed. A failure here
		// is logged rather than returned: the event IS stored, and losing
		// a statistic must not undo a delivery.
		if err := s.Store.RecordOriginAccepted(ctx, peer.ID, event.OrganizationName, s.now()); err != nil {
			log.WithError(err).WithField("peer", peer.Address).
				Warn("federation: could not count an accepted event against its origin")
		}
		accepted++
	}

	// Remember the batch only if it was fully consumed. A batch with
	// anything deferred for rate stays forgettable so the sender can send
	// it again; re-applying whatever WAS accepted is idempotent.
	//
	// The narrow cost: for a multi-event batch that was partly applied, a
	// retry re-applies the accepted rows. If the peer sent a newer update
	// for one of them in between, the retry reverts it. This instance's own
	// publisher sends one event per batch, so it cannot happen here — but
	// another implementation of this API could batch, and should send a new
	// batch id when it retries a partial.
	applied = rateLimited == 0

	receipt := csil.DeliveryReceipt{
		Accepted:    accepted,
		Rejected:    rejected + uint64(rateLimited),
		RateLimited: uint64(rateLimited),
	}
	if len(problems) > 0 {
		message := strings.Join(problems, "; ")
		receipt.Message = &message
	}
	return receipt, nil
}

// RequestPeering records a peer's request to be accepted.
//
// It always answers `pending`, and the row it creates is pending in the
// inbound direction only. Nobody is admitted by asking; an administrator
// here decides. Answering the same way whether or not the peer was already
// known keeps the op from being a way to enumerate who this directory
// trusts.
func (s *FederationService) RequestPeering(ctx context.Context, req csil.SignedPeeringRequest) (csil.PeeringReceipt, error) {
	handle, domain, ok := federation.SplitAddress(req.SenderAddress)
	if !ok {
		return csil.PeeringReceipt{}, Invalid("sender_address", "an address looks like handle@domain")
	}
	address := handle + "@" + domain

	// The sender's canonical identity travels in the envelope, alongside
	// sender_address — never taken from the decoded body, and never from a
	// stored peer row, because there usually is none yet: this may be the
	// first this instance has ever heard of the sender. The signature is
	// checked against exactly this claimed identity, which is what makes
	// that safe: an attacker can claim any identity, but can only produce a
	// signature that verifies for one it actually holds a currently
	// attested key for.
	claimed := federation.KeyIdentity{
		SubjectUserID: req.SubjectUserId, SubjectDomain: req.SubjectDomain,
		ApplicationID: req.ApplicationId, InstanceID: req.InstanceId,
	}
	if verifyErr := s.PeeringVerifier.Verify(ctx, address, claimed, req.Algorithm, req.KeyId, req.Body, []byte(req.Signature)); verifyErr != nil {
		return csil.PeeringReceipt{}, Forbidden("the request is not from who it claims")
	}

	body, err := csil.DecodePeeringBody(req.Body)
	if err != nil {
		return csil.PeeringReceipt{}, Invalid("body", "the signed body is not a peering request")
	}
	if strings.TrimSpace(body.BaseUrl) == "" {
		return csil.PeeringReceipt{}, Invalid("base_url", "a peer needs a base URL to be reached at")
	}

	peer, err := s.Store.PeerByAddress(ctx, address)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return csil.PeeringReceipt{}, err
	}
	if peer == nil {
		// The identity is captured NOW, at creation, because it just
		// verified. An EXISTING row's identity is never overwritten here —
		// see the note on Peer.subject_user_id: if this address later
		// belongs to a different account, that must not silently replace
		// what an administrator already approved.
		peer, err = s.Store.CreatePeer(ctx, store.PeerInput{
			Address: address, Handle: handle, Domain: domain,
			Identity: store.PeerIdentity{
				SubjectUserID: claimed.SubjectUserID, SubjectDomain: claimed.SubjectDomain,
				ApplicationID: claimed.ApplicationID, InstanceID: claimed.InstanceID,
			},
			BaseURL: strings.TrimSpace(body.BaseUrl),
			Note:    strings.TrimSpace(body.Note),
		})
		if err != nil {
			return csil.PeeringReceipt{}, err
		}
	}

	// A blocked peer is left blocked. Asking again is not a way out of a
	// decision somebody made.
	if peer.InboundStatus == store.PeerStatusBlocked {
		return csil.PeeringReceipt{Status: csil.PeerStatus(store.PeerStatusPending)}, nil
	}
	if peer.InboundStatus == store.PeerStatusNone {
		pending := store.PeerStatusPending
		if _, err := s.Store.SetPeerStatus(ctx, peer.ID, &pending, nil, nil); err != nil {
			return csil.PeeringReceipt{}, err
		}
	}
	log.WithField("peer", address).Info("federation: a peer asked to be accepted")
	return csil.PeeringReceipt{Status: csil.PeerStatus(store.PeerStatusPending)}, nil
}

// ListRemoteEvents is what makes a directory a directory. Anonymous callers
// are allowed: a directory exists to be read.
func (s *FederationService) ListRemoteEvents(ctx context.Context, req csil.ListRemoteEventsRequest) (csil.RemoteEventList, error) {
	f := store.RemoteEventFilter{Now: s.now(), Page: pageOf(req.Page)}
	if req.Query != nil {
		f.Query = strings.ToLower(strings.TrimSpace(*req.Query))
	}
	if req.StartsAfter != nil {
		f.StartsAfter = *req.StartsAfter
	}
	if req.StartsBefore != nil {
		f.StartsBefore = *req.StartsBefore
	}
	events, total, err := s.Store.ListRemoteEvents(ctx, f)
	if err != nil {
		return csil.RemoteEventList{}, err
	}
	out := make([]csil.RemoteEvent, 0, len(events))
	for i := range events {
		out = append(out, toRemoteEvent(&events[i]))
	}
	return csil.RemoteEventList{Events: out, Total: uint64(total)}, nil
}

// ListOriginVolume answers "which organization inside this peer is
// responsible for the volume".
//
// The limit is enforced per peer, because a peer is what a signature
// identifies and what can be suspended. That means a throttled peer does
// not say which of its organizations caused it. This does, so an operator
// can act on the origin rather than on the whole peer.
//
// Admins only: it is operational detail about somebody else's instance, not
// a public listing.
func (s *FederationService) ListOriginVolume(ctx context.Context, req csil.ListOriginVolumeRequest) (csil.OriginVolumeList, error) {
	if _, err := s.admin(ctx, "read the delivery volume"); err != nil {
		return csil.OriginVolumeList{}, err
	}
	peerID := ""
	if req.PeerId != nil {
		peerID = *req.PeerId
	}
	settings, err := s.Store.InstanceSettings(ctx)
	if err != nil {
		return csil.OriginVolumeList{}, err
	}
	origins, total, err := s.Store.ListOriginVolume(
		ctx, peerID, settings.OriginRateLimitPerMinute, s.now(), pageOf(req.Page))
	if err != nil {
		return csil.OriginVolumeList{}, err
	}
	out := make([]csil.OriginVolume, 0, len(origins))
	for i := range origins {
		out = append(out, toOriginVolume(&origins[i]))
	}
	return csil.OriginVolumeList{Origins: out, Total: uint64(total)}, nil
}

// SetOriginRateLimit throttles one organization inside a peer without
// touching the peer's other organizations — which is the whole reason the
// limit exists at two levels.
func (s *FederationService) SetOriginRateLimit(ctx context.Context, req csil.SetOriginRateLimitRequest) (csil.OriginVolume, error) {
	if _, err := s.admin(ctx, "change an origin's rate limit"); err != nil {
		return csil.OriginVolume{}, err
	}
	settings, err := s.Store.InstanceSettings(ctx)
	if err != nil {
		return csil.OriginVolume{}, err
	}
	var limit *int64
	if req.RateLimitPerMinute != nil {
		value := int64(*req.RateLimitPerMinute)
		limit = &value
	}
	origin, err := s.Store.SetOriginRateLimit(
		ctx, req.PeerId, req.OrganizationName, limit, settings.OriginRateLimitPerMinute, s.now())
	if errors.Is(err, store.ErrNotFound) {
		return csil.OriginVolume{}, NotFound("origin", "nothing has been delivered from that organization")
	}
	if err != nil {
		return csil.OriginVolume{}, err
	}
	return toOriginVolume(origin), nil
}

func toOriginVolume(o *store.OriginVolume) csil.OriginVolume {
	return csil.OriginVolume{
		PeerId:                 o.PeerID,
		PeerAddress:            o.PeerAddress,
		OrganizationName:       o.OrganizationName,
		Held:                   uint64(o.Held),
		AcceptedTotal:          uint64(o.AcceptedTotal),
		AcceptedThisMinute:     uint64(o.AcceptedThisMinute),
		LastReceivedAt:         o.LastReceivedAt,
		PeerRateLimitPerMinute: uint64(o.PeerRateLimitPerMinute),
		PeerRateLimitedTotal:   uint64(o.PeerRateLimitedTotal),
		PeerSuspended:          o.PeerSuspended,

		RateLimitPerMinute:          uintPtr(o.RateLimitPerMinute),
		EffectiveRateLimitPerMinute: uint64(o.EffectiveRateLimitPerMinute),
		RateLimitedTotal:            uint64(o.RateLimitedTotal),
	}
}

// ---- Shared -----------------------------------------------------------

// verifiedPeer checks a signature and returns who made it. The peer must
// already be known, because verification is checked against the peer's own
// STORED canonical identity, not anything the request itself claims — see
// the note on Peer.subject_user_id. A delivery from a stranger is therefore
// refused rather than treated as an introduction.
//
// This is also what keeps a handle change from transferring trust: if
// `sender_address` later belongs to a different linkkeys account, this
// still verifies against the OLD, approved identity's attested keys, which
// the new holder of the address does not control the private keys for — so
// their signature does not verify, and nothing here re-derives identity
// from the address to "catch up".
func (s *FederationService) verifiedPeer(ctx context.Context, senderAddress, algorithm, keyID string, body, signature []byte) (*store.Peer, error) {
	handle, domain, ok := federation.SplitAddress(senderAddress)
	if !ok {
		return nil, Invalid("sender_address", "an address looks like handle@domain")
	}
	peer, err := s.Store.PeerByAddress(ctx, handle+"@"+domain)
	if errors.Is(err, store.ErrNotFound) {
		return nil, Forbidden("this instance does not accept deliveries from you")
	}
	if err != nil {
		return nil, err
	}
	identity := federation.KeyIdentity{
		SubjectUserID: peer.Identity.SubjectUserID, SubjectDomain: peer.Identity.SubjectDomain,
		ApplicationID: peer.Identity.ApplicationID, InstanceID: peer.Identity.InstanceID,
	}
	if err := s.Verifier.Verify(ctx, peer.Address, identity, algorithm, keyID, body, signature); err != nil {
		return nil, Forbidden("the delivery is not from who it claims")
	}
	return peer, nil
}

// validateFederatedEvent applies the same shape rules a local event obeys,
// because a peer's bug must not become this instance's bad data. It returns
// a reason rather than an error: one bad row in a batch is rejected, not
// the whole delivery.
func validateFederatedEvent(e csil.FederatedEvent) string {
	if strings.TrimSpace(e.RemoteId) == "" {
		return "no id"
	}
	if strings.TrimSpace(e.Title) == "" {
		return "no title"
	}
	if strings.TrimSpace(e.CanonicalUrl) == "" {
		return "no canonical URL"
	}
	if !isWebURL(e.CanonicalUrl) {
		// A peer's URL ends up in an href on a page this instance serves.
		// A browser runs `javascript:` out of an href against THIS origin,
		// with this instance's session cookie, so the scheme is checked
		// before the row is ever stored.
		return "canonical URL is not http or https"
	}
	if !e.IsOnline && !e.IsInPerson {
		return "neither online nor in person"
	}
	if e.EndsAt.Before(e.StartsAt) {
		return "ends before it starts"
	}
	if _, err := time.LoadLocation(e.Timezone); err != nil {
		return fmt.Sprintf("unknown timezone %q", e.Timezone)
	}
	return ""
}

// isWebURL reports whether a URL is one this instance will put in an href.
//
// An allow-list of two schemes rather than a deny-list of the dangerous
// ones: the set of schemes a browser understands grows, and a scheme
// nobody has thought about yet must be refused rather than admitted.
func isWebURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if parsed.Host == "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return true
	default:
		return false
	}
}

func remoteEventInput(e csil.FederatedEvent, originDomain string) store.RemoteEventInput {
	in := store.RemoteEventInput{
		RemoteID:         e.RemoteId,
		OriginDomain:     originDomain,
		CanonicalURL:     e.CanonicalUrl,
		Title:            e.Title,
		IsOnline:         e.IsOnline,
		IsInPerson:       e.IsInPerson,
		Location:         fromLocation(e.Location),
		StartsAt:         e.StartsAt.UTC(),
		EndsAt:           e.EndsAt.UTC(),
		Timezone:         e.Timezone,
		GatheringName:    e.GatheringName,
		OrganizationName: e.OrganizationName,
	}
	in.SearchText = searchText(append(
		[]string{in.Title, in.GatheringName, in.OrganizationName},
		locationSearchParts(in.Location)...)...)
	return in
}

func toPeer(p *store.Peer) csil.Peer {
	return csil.Peer{
		Id:                p.ID,
		Address:           p.Address,
		Handle:            p.Handle,
		Domain:            p.Domain,
		SubjectUserId:     nonEmptyPtr(p.Identity.SubjectUserID),
		SubjectDomain:     nonEmptyPtr(p.Identity.SubjectDomain),
		ApplicationId:     nonEmptyPtr(p.Identity.ApplicationID),
		InstanceId:        nonEmptyPtr(p.Identity.InstanceID),
		BaseUrl:           p.BaseURL,
		InboundStatus:     csil.PeerStatus(p.InboundStatus),
		OutboundStatus:    csil.PeerStatus(p.OutboundStatus),
		Note:              p.Note,
		Suspended:         p.Suspended(),
		SuspendedAt:       p.SuspendedAt,
		FirstFailureAt:    p.FirstFailureAt,
		LastFailureAt:     p.LastFailureAt,
		LastFailureReason: p.LastFailureReason,
		LastSuccessAt:     p.LastSuccessAt,
		PendingDeliveries: uint64(p.PendingDeliveries),
		CreatedAt:         p.CreatedAt,
		UpdatedAt:         p.UpdatedAt,
	}
}

func toRemoteEvent(e *store.RemoteEvent) csil.RemoteEvent {
	return csil.RemoteEvent{
		Id:               e.ID,
		Origin:           remoteOrigin(e.OriginDomain, e.PeerAddress),
		PeerAddress:      e.PeerAddress,
		OriginDomain:     e.OriginDomain,
		RemoteId:         e.RemoteID,
		CanonicalUrl:     e.CanonicalURL,
		Title:            e.Title,
		IsOnline:         e.IsOnline,
		IsInPerson:       e.IsInPerson,
		Location:         toLocation(e.Location),
		StartsAt:         e.StartsAt,
		EndsAt:           e.EndsAt,
		Timezone:         e.Timezone,
		GatheringName:    e.GatheringName,
		OrganizationName: e.OrganizationName,
		ReceivedAt:       e.ReceivedAt,
	}
}

// uintPtr converts an optional limit for the wire. Absent means "no
// override", which is a different thing from a limit of zero — zero means
// no limit at all.
func uintPtr(v *int64) *uint64 {
	if v == nil {
		return nil
	}
	out := uint64(*v)
	return &out
}

// nonEmptyPtr reports an unresolved identity field as absent rather than
// as an empty string on the wire — Peer.subject_user_id and its three
// siblings are `? text` for exactly this: absent means "not resolved yet",
// which is a different thing from an empty value.
func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
