package csilservices

import (
	"context"
	"errors"
	"strings"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/store"
)

// AdminService implements csil.AdminService: the global admin role, and the
// people lookups a roster editor needs.
//
// The two halves have different gates on purpose. Granting and revoking the
// role needs the role. Looking a person up needs only a session, because
// every owner adding somebody to a roster has to resolve a handle first, and
// making that an admin-only power would make rosters unmanageable.
type AdminService struct {
	Store store.Store
}

var _ csil.AdminService = (*AdminService)(nil)

func (s *AdminService) ListAdmins(ctx context.Context, _ csil.Empty) (csil.AdminList, error) {
	c, err := authenticated(ctx, "list administrators")
	if err != nil {
		return csil.AdminList{}, err
	}
	if !c.IsAdmin {
		return csil.AdminList{}, Forbidden("only an administrator can list administrators")
	}
	return s.list(ctx)
}

func (s *AdminService) list(ctx context.Context) (csil.AdminList, error) {
	admins, err := s.Store.ListAdmins(ctx)
	if err != nil {
		return csil.AdminList{}, err
	}
	out := make([]csil.AdminUser, 0, len(admins))
	for i := range admins {
		out = append(out, toAdminUser(&admins[i]))
	}
	return csil.AdminList{Admins: out}, nil
}

// SetAdmin grants or revokes the role. The last admin cannot be revoked: a
// deployment with no admin has no way to delete an organization, and no way to make
// an admin again — the role would be gone for good, and only a hand-written
// UPDATE would bring it back.
func (s *AdminService) SetAdmin(ctx context.Context, req csil.SetAdminRequest) (csil.AdminList, error) {
	c, err := authenticated(ctx, "change administrators")
	if err != nil {
		return csil.AdminList{}, err
	}
	if !c.IsAdmin {
		return csil.AdminList{}, Forbidden("only an administrator can change administrators")
	}
	if !req.Granted {
		admins, err := s.Store.ListAdmins(ctx)
		if err != nil {
			return csil.AdminList{}, err
		}
		if len(admins) <= 1 {
			return csil.AdminList{}, Invalid("user_id", "this deployment must keep at least one administrator")
		}
	}
	if err := s.Store.SetAdmin(ctx, string(req.UserId), req.Granted); errors.Is(err, store.ErrNotFound) {
		return csil.AdminList{}, NotFound("user", "no user with that id")
	} else if err != nil {
		return csil.AdminList{}, err
	}
	return s.list(ctx)
}

// FindUser resolves one federated address. It needs a session but no role:
// resolving `handle@domain` to an id is the first step of every roster
// edit, and the answer is not private — it is how people address each other.
func (s *AdminService) FindUser(ctx context.Context, req csil.FindUserRequest) (csil.UserRef, error) {
	if _, err := authenticated(ctx, "look somebody up"); err != nil {
		return csil.UserRef{}, err
	}
	handle := strings.ToLower(strings.TrimSpace(req.Handle))
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	user, err := s.Store.UserByHandle(ctx, handle, domain)
	if errors.Is(err, store.ErrNotFound) {
		return csil.UserRef{}, NotFound("user", "nobody here has that address")
	}
	if err != nil {
		return csil.UserRef{}, err
	}
	return toUserRef(user), nil
}

func (s *AdminService) SearchUsers(ctx context.Context, req csil.SearchUsersRequest) (csil.UserRefList, error) {
	if _, err := authenticated(ctx, "look somebody up"); err != nil {
		return csil.UserRefList{}, err
	}
	prefix := strings.ToLower(strings.TrimSpace(req.Query))
	if prefix == "" {
		return csil.UserRefList{Users: []csil.UserRef{}}, nil
	}
	users, err := s.Store.SearchUsers(ctx, prefix, pageOf(req.Page))
	if err != nil {
		return csil.UserRefList{}, err
	}
	out := make([]csil.UserRef, 0, len(users))
	for i := range users {
		out = append(out, toUserRef(&users[i]))
	}
	return csil.UserRefList{Users: out}, nil
}

// GetInstanceSettings reads what an administrator can change while the
// service runs.
func (s *AdminService) GetInstanceSettings(ctx context.Context, _ csil.Empty) (csil.InstanceSettings, error) {
	c, err := authenticated(ctx, "read the instance settings")
	if err != nil {
		return csil.InstanceSettings{}, err
	}
	if !c.IsAdmin {
		return csil.InstanceSettings{}, Forbidden("only an administrator can read the instance settings")
	}
	settings, err := s.Store.InstanceSettings(ctx)
	if err != nil {
		return csil.InstanceSettings{}, err
	}
	return toInstanceSettings(settings), nil
}

// UpdateInstanceSettings changes them. Read-modify-write, so an absent
// field is left alone and two administrators editing different settings do
// not undo each other's work by omission.
func (s *AdminService) UpdateInstanceSettings(ctx context.Context, req csil.UpdateInstanceSettingsRequest) (csil.InstanceSettings, error) {
	c, err := authenticated(ctx, "change the instance settings")
	if err != nil {
		return csil.InstanceSettings{}, err
	}
	if !c.IsAdmin {
		return csil.InstanceSettings{}, Forbidden("only an administrator can change the instance settings")
	}

	settings, err := s.Store.InstanceSettings(ctx)
	if err != nil {
		return csil.InstanceSettings{}, err
	}
	if req.PublishDefault != nil {
		value, ok := publishSettingOf(req.PublishDefault)
		if !ok || value == store.PublishUnset {
			// The instance is the bottom of the chain. "Unset" there would
			// leave nothing to fall back to, so it is not a legal value.
			return csil.InstanceSettings{}, Invalid("publish_default", "the instance default is in or out")
		}
		settings.PublishDefault = value
	}
	if req.OrganizationOverrideAllowed != nil {
		settings.OrganizationOverrideAllowed = *req.OrganizationOverrideAllowed
	}
	if req.GatheringOverrideAllowed != nil {
		settings.GatheringOverrideAllowed = *req.GatheringOverrideAllowed
	}
	if req.RetentionDays != nil {
		settings.RetentionDays = int64(*req.RetentionDays)
	}
	if req.PeerRateLimitPerMinute != nil {
		settings.PeerRateLimitPerMinute = int64(*req.PeerRateLimitPerMinute)
	}
	if req.OriginRateLimitPerMinute != nil {
		settings.OriginRateLimitPerMinute = int64(*req.OriginRateLimitPerMinute)
	}

	if err := s.Store.PutInstanceSettings(ctx, settings); err != nil {
		return csil.InstanceSettings{}, err
	}
	return toInstanceSettings(settings), nil
}

func toInstanceSettings(in store.InstanceSettings) csil.InstanceSettings {
	return csil.InstanceSettings{
		PublishDefault:              wirePublishSetting(in.PublishDefault),
		OrganizationOverrideAllowed: in.OrganizationOverrideAllowed,
		GatheringOverrideAllowed:    in.GatheringOverrideAllowed,
		RetentionDays:               uint64(in.RetentionDays),
		PeerRateLimitPerMinute:      uint64(in.PeerRateLimitPerMinute),
		OriginRateLimitPerMinute:    uint64(in.OriginRateLimitPerMinute),
	}
}
