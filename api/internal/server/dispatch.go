// Package server is tinku-api's HTTP surface: the CSIL-RPC carrier, the
// session middleware, and the one HTTP-native route the login flow needs.
//
// This file maps CSIL (service, op) pairs to the generated service
// interfaces. csilgen does not generate a router for its Go target, so the
// table is maintained by hand — adding an op to csil/tinku.csil means
// adding a row here, and the compiler catches the signature if the row is
// wrong.
//
// # Wire naming
//
// The strings a real request carries are not the Go interface and method
// names. They are what csilgen's client generators put on the wire:
//
//   - service: the CSIL service name with a trailing "Service" stripped and
//     lowercased — "AuthService" becomes "auth". The generated clients emit
//     the full name, so dispatch normalizes both spellings and accepts
//     either.
//   - op: the operation name exactly as declared in the schema, kebab-case
//     — "begin-login". The table is keyed directly on those, so nothing is
//     converted at request time.
package server

import (
	"context"
	"errors"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/csilservices"
	"github.com/catalystcommunity/tinku/api/internal/metrics"
	"github.com/catalystcommunity/tinku/api/internal/transport"
)

// Services bundles one implementation per generated CSIL service interface.
// DevAuth is nil unless config.DevAuthAllowed passed at boot, and a nil
// DevAuth leaves the whole service out of the routing table — the wire then
// answers "unknown service or op", which is the honest answer for a server
// that does not offer it.
type Services struct {
	Auth         csil.AuthService
	DevAuth      csil.DevAuthService
	Greeting     csil.GreetingService
	Organization csil.OrganizationService
	Gathering    csil.GatheringService
	Event        csil.EventService
	Search       csil.SearchService
	Admin        csil.AdminService
	// Federation is nil unless config.FederationAllowed passed at boot. A
	// nil Federation leaves the whole service out of the routing table, so
	// an instance that does not federate answers "unknown service or op" —
	// which is the honest answer, and tells a prober nothing.
	Federation csil.FederationService
	Webhook    csil.WebhookService
}

// typedHandler decodes a request payload, calls a service method, and
// encodes the result (or maps the failure) into a transport outcome.
type typedHandler func(ctx context.Context, payload []byte) transport.HandlerOutcome

// routeFallible wires an op whose CSIL declaration carries a `/ ServiceError`
// arm. reply is the generated `csil.Encode<Service><Op>Reply`: it names the
// reply variant from the schema, so no variant string is written here and
// none can drift from the schema.
//
// An *AppError becomes that declared arm — a typed reply at transport status
// 0. Any other error becomes a transport-level internal failure with no
// detail, because nothing else is known to be safe to show a caller. A reply
// that cannot be encoded (csil.RPCEncodeError) is also an internal failure.
// See the csilservices package comment for the whole contract.
func routeFallible[Req any, Resp any](
	decode func([]byte) (Req, error),
	fn func(context.Context, Req) (Resp, error),
	reply func(Resp, error) (string, []byte, error),
) typedHandler {
	return func(ctx context.Context, payload []byte) transport.HandlerOutcome {
		req, err := decode(payload)
		if err != nil {
			return transport.Transport(transport.StatusMalformedEnvelope, "decode request: "+err.Error())
		}
		resp, err := fn(ctx, req)
		if appErr, ok := csilservices.AsAppError(err); ok {
			err = &csil.RPCApplicationError[csil.ServiceError]{Value: appErr.ServiceError()}
		}
		variant, body, err := reply(resp, err)
		if err != nil {
			var encErr *csil.RPCEncodeError
			if errors.As(err, &encErr) {
				log.WithError(err).Error("reply could not be encoded")
			} else {
				log.WithError(err).Error("unhandled error from service method")
			}
			return transport.Transport(transport.StatusInternal, "internal error")
		}
		return transport.Reply(variant, body)
	}
}

// routeInfallible wires an op with NO declared error arm: there is no typed
// channel for a failure, so every error becomes a transport-level internal
// failure regardless of its type. csilgen makes no reply encoder for such an
// op, so the variant is written here.
func routeInfallible[Req any, Resp any](
	decode func([]byte) (Req, error),
	fn func(context.Context, Req) (Resp, error),
	encode func(Resp) ([]byte, error),
	variant string,
) typedHandler {
	return func(ctx context.Context, payload []byte) transport.HandlerOutcome {
		req, err := decode(payload)
		if err != nil {
			return transport.Transport(transport.StatusMalformedEnvelope, "decode "+variant+" request: "+err.Error())
		}
		resp, err := fn(ctx, req)
		if err != nil {
			log.WithError(err).WithField("variant", variant).
				Error("unhandled error from service method (op has no declared error arm)")
			return transport.Transport(transport.StatusInternal, "internal error")
		}
		body, err := encode(resp)
		if err != nil {
			log.WithError(err).WithField("variant", variant).Error("reply could not be encoded")
			return transport.Transport(transport.StatusInternal, "internal error")
		}
		return transport.Reply(variant, body)
	}
}

// buildRoutes constructs the (service, op) table from svcs — one row per
// operation declared in csil/tinku.csil, in schema order.
func buildRoutes(svcs Services) map[string]map[string]typedHandler {
	routes := map[string]map[string]typedHandler{
		"auth": {
			"begin-login": routeFallible(csil.DecodeBeginLoginRequest, svcs.Auth.BeginLogin, csil.EncodeAuthBeginLoginReply),
			"logout":      routeInfallible(csil.DecodeEmpty, svcs.Auth.Logout, csil.EncodeEmpty, "Empty"),
			"whoami":      routeFallible(csil.DecodeEmpty, svcs.Auth.Whoami, csil.EncodeAuthWhoamiReply),
		},
		"greeting": {
			"list-greetings":  routeInfallible(csil.DecodeEmpty, svcs.Greeting.ListGreetings, csil.EncodeGreetingList, "GreetingList"),
			"get-greeting":    routeFallible(csil.DecodeGetGreetingRequest, svcs.Greeting.GetGreeting, csil.EncodeGreetingGetGreetingReply),
			"create-greeting": routeFallible(csil.DecodeCreateGreetingRequest, svcs.Greeting.CreateGreeting, csil.EncodeGreetingCreateGreetingReply),
		},
		"organization": {
			"list-organizations":         routeFallible(csil.DecodeListOrganizationsRequest, svcs.Organization.ListOrganizations, csil.EncodeOrganizationListOrganizationsReply),
			"get-organization":           routeFallible(csil.DecodeGetOrganizationRequest, svcs.Organization.GetOrganization, csil.EncodeOrganizationGetOrganizationReply),
			"create-organization":        routeFallible(csil.DecodeCreateOrganizationRequest, svcs.Organization.CreateOrganization, csil.EncodeOrganizationCreateOrganizationReply),
			"update-organization":        routeFallible(csil.DecodeUpdateOrganizationRequest, svcs.Organization.UpdateOrganization, csil.EncodeOrganizationUpdateOrganizationReply),
			"delete-organization":        routeFallible(csil.DecodeDeleteOrganizationRequest, svcs.Organization.DeleteOrganization, csil.EncodeOrganizationDeleteOrganizationReply),
			"list-organization-members":  routeFallible(csil.DecodeListOrganizationMembersRequest, svcs.Organization.ListOrganizationMembers, csil.EncodeOrganizationListOrganizationMembersReply),
			"set-organization-member":    routeFallible(csil.DecodeSetOrganizationMemberRequest, svcs.Organization.SetOrganizationMember, csil.EncodeOrganizationSetOrganizationMemberReply),
			"remove-organization-member": routeFallible(csil.DecodeRemoveOrganizationMemberRequest, svcs.Organization.RemoveOrganizationMember, csil.EncodeOrganizationRemoveOrganizationMemberReply),
		},
		"gathering": {
			"list-gatherings":        routeFallible(csil.DecodeListGatheringsRequest, svcs.Gathering.ListGatherings, csil.EncodeGatheringListGatheringsReply),
			"get-gathering":          routeFallible(csil.DecodeGetGatheringRequest, svcs.Gathering.GetGathering, csil.EncodeGatheringGetGatheringReply),
			"create-gathering":       routeFallible(csil.DecodeCreateGatheringRequest, svcs.Gathering.CreateGathering, csil.EncodeGatheringCreateGatheringReply),
			"update-gathering":       routeFallible(csil.DecodeUpdateGatheringRequest, svcs.Gathering.UpdateGathering, csil.EncodeGatheringUpdateGatheringReply),
			"delete-gathering":       routeFallible(csil.DecodeDeleteGatheringRequest, svcs.Gathering.DeleteGathering, csil.EncodeGatheringDeleteGatheringReply),
			"list-gathering-members": routeFallible(csil.DecodeListGatheringMembersRequest, svcs.Gathering.ListGatheringMembers, csil.EncodeGatheringListGatheringMembersReply),
			"join-gathering":         routeFallible(csil.DecodeJoinGatheringRequest, svcs.Gathering.JoinGathering, csil.EncodeGatheringJoinGatheringReply),
			"leave-gathering":        routeFallible(csil.DecodeLeaveGatheringRequest, svcs.Gathering.LeaveGathering, csil.EncodeGatheringLeaveGatheringReply),
			"add-gathering-owner":    routeFallible(csil.DecodeAddGatheringOwnerRequest, svcs.Gathering.AddGatheringOwner, csil.EncodeGatheringAddGatheringOwnerReply),
			"remove-gathering-owner": routeFallible(csil.DecodeRemoveGatheringOwnerRequest, svcs.Gathering.RemoveGatheringOwner, csil.EncodeGatheringRemoveGatheringOwnerReply),
			// Moving a gathering under an organization: offered and
			// accepted by two sides, or adopted by an administrator.
			"offer-gathering":            routeFallible(csil.DecodeOfferGatheringRequest, svcs.Gathering.OfferGathering, csil.EncodeGatheringOfferGatheringReply),
			"list-gathering-offers":      routeFallible(csil.DecodeListGatheringOffersRequest, svcs.Gathering.ListGatheringOffers, csil.EncodeGatheringListGatheringOffersReply),
			"respond-to-gathering-offer": routeFallible(csil.DecodeRespondToGatheringOfferRequest, svcs.Gathering.RespondToGatheringOffer, csil.EncodeGatheringRespondToGatheringOfferReply),
			"withdraw-gathering-offer":   routeFallible(csil.DecodeWithdrawGatheringOfferRequest, svcs.Gathering.WithdrawGatheringOffer, csil.EncodeGatheringWithdrawGatheringOfferReply),
			"adopt-gathering":            routeFallible(csil.DecodeAdoptGatheringRequest, svcs.Gathering.AdoptGathering, csil.EncodeGatheringAdoptGatheringReply),
		},
		"webhook": {
			"list-webhooks":  routeFallible(csil.DecodeListWebhooksRequest, svcs.Webhook.ListWebhooks, csil.EncodeWebhookListWebhooksReply),
			"create-webhook": routeFallible(csil.DecodeCreateWebhookRequest, svcs.Webhook.CreateWebhook, csil.EncodeWebhookCreateWebhookReply),
			"update-webhook": routeFallible(csil.DecodeUpdateWebhookRequest, svcs.Webhook.UpdateWebhook, csil.EncodeWebhookUpdateWebhookReply),
			"delete-webhook": routeFallible(csil.DecodeDeleteWebhookRequest, svcs.Webhook.DeleteWebhook, csil.EncodeWebhookDeleteWebhookReply),
		},
		"event": {
			"list-events":         routeFallible(csil.DecodeListEventsRequest, svcs.Event.ListEvents, csil.EncodeEventListEventsReply),
			"get-event":           routeFallible(csil.DecodeGetEventRequest, svcs.Event.GetEvent, csil.EncodeEventGetEventReply),
			"create-event":        routeFallible(csil.DecodeCreateEventRequest, svcs.Event.CreateEvent, csil.EncodeEventCreateEventReply),
			"update-event":        routeFallible(csil.DecodeUpdateEventRequest, svcs.Event.UpdateEvent, csil.EncodeEventUpdateEventReply),
			"delete-event":        routeFallible(csil.DecodeDeleteEventRequest, svcs.Event.DeleteEvent, csil.EncodeEventDeleteEventReply),
			"attend-event":        routeFallible(csil.DecodeAttendEventRequest, svcs.Event.AttendEvent, csil.EncodeEventAttendEventReply),
			"unattend-event":      routeFallible(csil.DecodeUnattendEventRequest, svcs.Event.UnattendEvent, csil.EncodeEventUnattendEventReply),
			"list-attendees":      routeFallible(csil.DecodeListAttendeesRequest, svcs.Event.ListAttendees, csil.EncodeEventListAttendeesReply),
			"list-event-series":   routeFallible(csil.DecodeListEventSeriesRequest, svcs.Event.ListEventSeries, csil.EncodeEventListEventSeriesReply),
			"get-event-series":    routeFallible(csil.DecodeGetEventSeriesRequest, svcs.Event.GetEventSeries, csil.EncodeEventGetEventSeriesReply),
			"create-event-series": routeFallible(csil.DecodeCreateEventSeriesRequest, svcs.Event.CreateEventSeries, csil.EncodeEventCreateEventSeriesReply),
			"update-event-series": routeFallible(csil.DecodeUpdateEventSeriesRequest, svcs.Event.UpdateEventSeries, csil.EncodeEventUpdateEventSeriesReply),
			"delete-event-series": routeFallible(csil.DecodeDeleteEventSeriesRequest, svcs.Event.DeleteEventSeries, csil.EncodeEventDeleteEventSeriesReply),
			"expand-event-series": routeFallible(csil.DecodeExpandEventSeriesRequest, svcs.Event.ExpandEventSeries, csil.EncodeEventExpandEventSeriesReply),
			"set-event-role":      routeFallible(csil.DecodeSetEventRoleRequest, svcs.Event.SetEventRole, csil.EncodeEventSetEventRoleReply),
			"remove-event-role":   routeFallible(csil.DecodeRemoveEventRoleRequest, svcs.Event.RemoveEventRole, csil.EncodeEventRemoveEventRoleReply),
			"list-event-roles":    routeFallible(csil.DecodeListEventRolesRequest, svcs.Event.ListEventRoles, csil.EncodeEventListEventRolesReply),
		},
		"search": {
			"search": routeFallible(csil.DecodeSearchRequest, svcs.Search.Search, csil.EncodeSearchSearchReply),
		},
		"admin": {
			"list-admins":              routeFallible(csil.DecodeEmpty, svcs.Admin.ListAdmins, csil.EncodeAdminListAdminsReply),
			"set-admin":                routeFallible(csil.DecodeSetAdminRequest, svcs.Admin.SetAdmin, csil.EncodeAdminSetAdminReply),
			"find-user":                routeFallible(csil.DecodeFindUserRequest, svcs.Admin.FindUser, csil.EncodeAdminFindUserReply),
			"search-users":             routeFallible(csil.DecodeSearchUsersRequest, svcs.Admin.SearchUsers, csil.EncodeAdminSearchUsersReply),
			"get-instance-settings":    routeFallible(csil.DecodeEmpty, svcs.Admin.GetInstanceSettings, csil.EncodeAdminGetInstanceSettingsReply),
			"update-instance-settings": routeFallible(csil.DecodeUpdateInstanceSettingsRequest, svcs.Admin.UpdateInstanceSettings, csil.EncodeAdminUpdateInstanceSettingsReply),
		},
	}

	// Registered only when federation is switched on and this instance has
	// an account of its own to sign with.
	if svcs.Federation != nil {
		routes["federation"] = map[string]typedHandler{
			"federation-identity":   routeFallible(csil.DecodeEmpty, svcs.Federation.FederationIdentity, csil.EncodeFederationFederationIdentityReply),
			"list-peers":            routeFallible(csil.DecodeListPeersRequest, svcs.Federation.ListPeers, csil.EncodeFederationListPeersReply),
			"add-peer":              routeFallible(csil.DecodeAddPeerRequest, svcs.Federation.AddPeer, csil.EncodeFederationAddPeerReply),
			"set-peer-status":       routeFallible(csil.DecodeSetPeerStatusRequest, svcs.Federation.SetPeerStatus, csil.EncodeFederationSetPeerStatusReply),
			"resume-peer":           routeFallible(csil.DecodeResumePeerRequest, svcs.Federation.ResumePeer, csil.EncodeFederationResumePeerReply),
			"remove-peer":           routeFallible(csil.DecodeRemovePeerRequest, svcs.Federation.RemovePeer, csil.EncodeFederationRemovePeerReply),
			"deliver-events":        routeFallible(csil.DecodeSignedDelivery, svcs.Federation.DeliverEvents, csil.EncodeFederationDeliverEventsReply),
			"request-peering":       routeFallible(csil.DecodeSignedPeeringRequest, svcs.Federation.RequestPeering, csil.EncodeFederationRequestPeeringReply),
			"list-remote-events":    routeFallible(csil.DecodeListRemoteEventsRequest, svcs.Federation.ListRemoteEvents, csil.EncodeFederationListRemoteEventsReply),
			"set-peer-rate-limit":   routeFallible(csil.DecodeSetPeerRateLimitRequest, svcs.Federation.SetPeerRateLimit, csil.EncodeFederationSetPeerRateLimitReply),
			"list-origin-volume":    routeFallible(csil.DecodeListOriginVolumeRequest, svcs.Federation.ListOriginVolume, csil.EncodeFederationListOriginVolumeReply),
			"set-origin-rate-limit": routeFallible(csil.DecodeSetOriginRateLimitRequest, svcs.Federation.SetOriginRateLimit, csil.EncodeFederationSetOriginRateLimitReply),
		}
	}

	// Registered only when the gates passed at boot. An unregistered
	// service is indistinguishable on the wire from one this build does not
	// have, which is the point.
	if svcs.DevAuth != nil {
		routes["devauth"] = map[string]typedHandler{
			"dev-login": routeFallible(csil.DecodeDevLoginRequest, svcs.DevAuth.DevLogin, csil.EncodeDevAuthDevLoginReply),
		}
	}
	return routes
}

// unroutedLabel stands in for a service or op name that did not resolve, so
// no metric label ever carries a string a caller chose.
const unroutedLabel = "unrouted"

// dispatch resolves req against routes and records the call's metrics.
func dispatch(ctx context.Context, routes map[string]map[string]typedHandler, req *transport.RpcRequest) transport.HandlerOutcome {
	service := strings.ToLower(strings.TrimSuffix(req.Service, "Service"))

	// An unrouted call is counted under a FIXED pair of labels, never under
	// the strings the request carried. A metric label taken from an
	// unauthenticated request body is unbounded cardinality: a prober
	// looping over random service and op names would mint a new time series
	// per request until the scrape target ran out of memory.
	ops, ok := routes[service]
	if !ok {
		metrics.RPCRequests.WithLabelValues(unroutedLabel, unroutedLabel, "unknown_service_or_op").Inc()
		return transport.Transport(transport.StatusUnknownServiceOrOp, "unknown service: "+req.Service)
	}
	handler, ok := ops[req.Op]
	if !ok {
		// The service resolved, so its name is one of ours and bounded; the
		// op did not, so it is not.
		metrics.RPCRequests.WithLabelValues(service, unroutedLabel, "unknown_service_or_op").Inc()
		return transport.Transport(transport.StatusUnknownServiceOrOp, "unknown op: "+req.Service+"/"+req.Op)
	}

	start := time.Now()
	outcome := handler(ctx, req.Payload)
	metrics.RPCDuration.WithLabelValues(service, req.Op).Observe(time.Since(start).Seconds())
	metrics.RPCRequests.WithLabelValues(service, req.Op, outcomeLabel(outcome)).Inc()
	return outcome
}

// outcomeLabel keeps the three distinct results distinct on the dashboard:
// a typed reply, a declared application error, and a transport failure.
func outcomeLabel(outcome transport.HandlerOutcome) string {
	if !outcome.IsReply {
		return outcome.Status.Name()
	}
	if outcome.Variant == "ServiceError" {
		return "service_error"
	}
	return "ok"
}
