package csilservices

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/config"
	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/store"
)

// WebhooksPerOwner is how many a single organization or gathering may have.
//
// Five is a product decision, not a technical one: it is enough for the
// integrations a group actually runs, and small enough that the list stays
// readable and one owner cannot turn the sender into their own queue.
const WebhooksPerOwner = 5

// WebhookService implements csil.WebhookService.
//
// Every op here answers the same permission question — does the caller own
// the level this webhook hangs off — and the answer comes from the same
// places every other permission does: OrganizationRoleFor for an
// organization, GatheringAccessFor for a gathering.
type WebhookService struct {
	Store store.Store
	Cfg   config.Config
}

var _ csil.WebhookService = (*WebhookService)(nil)

// canManage reports whether the caller may see and change the webhooks on
// one level.
//
// A webhook's URL and its delivery history say where somebody's systems
// are and whether they are healthy, so reading the list is an owner's
// power, not a member's. An administrator is included: an instance
// operator answering "what is this server POSTing, and where" cannot be
// told to go and ask five different owners.
func (s *WebhookService) canManage(ctx context.Context, c caller, kind store.WebhookOwnerKind, ownerID string) (bool, error) {
	if c.IsAdmin {
		return true, nil
	}
	switch kind {
	case store.WebhookOwnerOrganization:
		role, onRoster, err := s.Store.OrganizationRoleFor(ctx, ownerID, c.ID)
		if err != nil {
			return false, err
		}
		return onRoster && role == store.OrganizationRoleOwner, nil
	case store.WebhookOwnerGathering:
		access, err := s.Store.GatheringAccessFor(ctx, ownerID, c.ID)
		if err != nil {
			return false, err
		}
		return access.IsOwner, nil
	default:
		return false, nil
	}
}

// ownerKindOf validates the wire value. An unknown kind is a bad request
// rather than a lookup that finds nothing.
func ownerKindOf(kind csil.WebhookOwnerKind) (store.WebhookOwnerKind, error) {
	switch store.WebhookOwnerKind(kind) {
	case store.WebhookOwnerOrganization:
		return store.WebhookOwnerOrganization, nil
	case store.WebhookOwnerGathering:
		return store.WebhookOwnerGathering, nil
	default:
		return "", Invalid("owner_kind", "a webhook belongs to an organization or a gathering")
	}
}

func scopeOf(scope csil.WebhookScope) (store.WebhookScope, error) {
	switch store.WebhookScope(scope) {
	case store.WebhookScopeAll:
		return store.WebhookScopeAll, nil
	case store.WebhookScopeStructure:
		return store.WebhookScopeStructure, nil
	default:
		return "", Invalid("scope", "a scope is all or structure_only")
	}
}

// checkURL refuses what must not be delivered to.
//
// HTTPS outside a dev environment, because a delivery carries a signature
// and the names of things that are not always public, and plain HTTP hands
// both to anybody on the path. In a dev environment an http:// endpoint is
// the only way to try a webhook at all, so it is allowed there and nowhere
// else — the same shape the dev signer uses.
func (s *WebhookService) checkURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return Invalid("url", "that is not a URL this server can post to")
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if s.Cfg.DevAuthAllowed() {
			return nil
		}
		return Invalid("url", "a webhook URL must be https")
	default:
		return Invalid("url", "a webhook URL must be https")
	}
}

func (s *WebhookService) ListWebhooks(ctx context.Context, req csil.ListWebhooksRequest) (csil.WebhookList, error) {
	c, err := authenticated(ctx, "read webhooks")
	if err != nil {
		return csil.WebhookList{}, err
	}
	kind, err := ownerKindOf(req.OwnerKind)
	if err != nil {
		return csil.WebhookList{}, err
	}
	allowed, err := s.canManage(ctx, c, kind, req.OwnerId)
	if err != nil {
		return csil.WebhookList{}, err
	}
	if !allowed {
		return csil.WebhookList{}, Forbidden("only an owner can read the webhooks here")
	}

	hooks, err := s.Store.ListWebhooks(ctx, kind, req.OwnerId)
	if err != nil {
		return csil.WebhookList{}, err
	}
	out := make([]csil.Webhook, 0, len(hooks))
	for i := range hooks {
		out = append(out, toWebhook(&hooks[i]))
	}
	return csil.WebhookList{Webhooks: out, Limit: uint64(WebhooksPerOwner)}, nil
}

func (s *WebhookService) CreateWebhook(ctx context.Context, req csil.CreateWebhookRequest) (csil.WebhookWithSecret, error) {
	c, err := authenticated(ctx, "add a webhook")
	if err != nil {
		return csil.WebhookWithSecret{}, err
	}
	kind, err := ownerKindOf(req.OwnerKind)
	if err != nil {
		return csil.WebhookWithSecret{}, err
	}
	scope, err := scopeOf(req.Scope)
	if err != nil {
		return csil.WebhookWithSecret{}, err
	}
	allowed, err := s.canManage(ctx, c, kind, req.OwnerId)
	if err != nil {
		return csil.WebhookWithSecret{}, err
	}
	if !allowed {
		return csil.WebhookWithSecret{}, Forbidden("only an owner can add a webhook here")
	}
	if err := s.checkURL(req.Url); err != nil {
		return csil.WebhookWithSecret{}, err
	}

	// The level has to exist. Without this, a typo in an id makes a webhook
	// that can never fire and that nobody can find to delete.
	switch kind {
	case store.WebhookOwnerOrganization:
		if _, err := s.Store.OrganizationByID(ctx, req.OwnerId); errors.Is(err, store.ErrNotFound) {
			return csil.WebhookWithSecret{}, NotFound("organization", "no organization with that id")
		} else if err != nil {
			return csil.WebhookWithSecret{}, err
		}
	case store.WebhookOwnerGathering:
		if _, err := s.Store.GatheringByID(ctx, req.OwnerId); errors.Is(err, store.ErrNotFound) {
			return csil.WebhookWithSecret{}, NotFound("gathering", "no gathering with that id")
		} else if err != nil {
			return csil.WebhookWithSecret{}, err
		}
	}

	secret, err := newWebhookSecret()
	if err != nil {
		return csil.WebhookWithSecret{}, err
	}

	created, err := s.Store.CreateWebhook(ctx, store.Webhook{
		OwnerKind: kind,
		OwnerID:   req.OwnerId,
		URL:       strings.TrimSpace(req.Url),
		Secret:    secret,
		Scope:     scope,
		Note:      req.Note,
		// The owner's own call, made with the warning in front of them.
		// The server records it and does not second-guess it: this is their
		// description and their address to send.
		IncludeDetails: req.IncludeDetails,
	}, WebhooksPerOwner)
	if errors.Is(err, store.ErrLimitReached) {
		return csil.WebhookWithSecret{}, Invalid("url", "this level already has as many webhooks as it may have")
	}
	if err != nil {
		return csil.WebhookWithSecret{}, err
	}

	// The only time the secret is ever sent. It is not readable afterwards:
	// a value a server hands back on demand leaks through every screen that
	// shows it.
	return csil.WebhookWithSecret{Webhook: toWebhook(created), Secret: secret}, nil
}

func (s *WebhookService) UpdateWebhook(ctx context.Context, req csil.UpdateWebhookRequest) (csil.Webhook, error) {
	c, err := authenticated(ctx, "change a webhook")
	if err != nil {
		return csil.Webhook{}, err
	}
	current, err := s.Store.WebhookByID(ctx, string(req.Id))
	if errors.Is(err, store.ErrNotFound) {
		return csil.Webhook{}, NotFound("webhook", "no webhook with that id")
	}
	if err != nil {
		return csil.Webhook{}, err
	}
	allowed, err := s.canManage(ctx, c, current.OwnerKind, current.OwnerID)
	if err != nil {
		return csil.Webhook{}, err
	}
	if !allowed {
		return csil.Webhook{}, Forbidden("only an owner can change this webhook")
	}

	in := store.WebhookInput{
		URL:            current.URL,
		Scope:          current.Scope,
		Note:           current.Note,
		Active:         current.Active,
		IncludeDetails: current.IncludeDetails,
	}
	if req.Url != nil {
		if err := s.checkURL(*req.Url); err != nil {
			return csil.Webhook{}, err
		}
		in.URL = strings.TrimSpace(*req.Url)
	}
	if req.Scope != nil {
		scope, err := scopeOf(*req.Scope)
		if err != nil {
			return csil.Webhook{}, err
		}
		in.Scope = scope
	}
	if req.Note != nil {
		in.Note = *req.Note
	}
	if req.Active != nil {
		in.Active = *req.Active
	}
	if req.IncludeDetails != nil {
		in.IncludeDetails = *req.IncludeDetails
	}

	updated, err := s.Store.UpdateWebhook(ctx, current.ID, in, time.Now())
	if err != nil {
		return csil.Webhook{}, err
	}
	return toWebhook(updated), nil
}

func (s *WebhookService) DeleteWebhook(ctx context.Context, req csil.DeleteWebhookRequest) (csil.Empty, error) {
	c, err := authenticated(ctx, "remove a webhook")
	if err != nil {
		return csil.Empty{}, err
	}
	current, err := s.Store.WebhookByID(ctx, string(req.Id))
	if errors.Is(err, store.ErrNotFound) {
		return csil.Empty{}, NotFound("webhook", "no webhook with that id")
	}
	if err != nil {
		return csil.Empty{}, err
	}
	allowed, err := s.canManage(ctx, c, current.OwnerKind, current.OwnerID)
	if err != nil {
		return csil.Empty{}, err
	}
	if !allowed {
		return csil.Empty{}, Forbidden("only an owner can remove this webhook")
	}
	if err := s.Store.DeleteWebhook(ctx, current.ID); err != nil {
		return csil.Empty{}, err
	}
	return csil.Empty{}, nil
}

// newWebhookSecret mints the HMAC key for one webhook. 32 bytes from the
// system source, hex so an operator can paste it into a receiver's
// configuration without worrying about encoding.
func newWebhookSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func toWebhook(w *store.Webhook) csil.Webhook {
	out := csil.Webhook{
		Id:             csil.WebhookID(w.ID),
		OwnerKind:      csil.WebhookOwnerKind(w.OwnerKind),
		OwnerId:        w.OwnerID,
		Url:            w.URL,
		Scope:          csil.WebhookScope(w.Scope),
		Note:           w.Note,
		Active:         w.Active,
		IncludeDetails: w.IncludeDetails,
		FailureCount:   uint64(w.FailureCount),
		CreatedAt:      w.CreatedAt,
		UpdatedAt:      w.UpdatedAt,
	}
	if w.LastStatus != nil {
		status := uint64(*w.LastStatus)
		out.LastStatus = &status
	}
	out.LastAttemptAt = w.LastAttemptAt
	return out
}
