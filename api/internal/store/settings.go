package store

import "strconv"

// The instance_settings table is key/value. These are the keys, and the two
// functions that translate between a row and the struct.
//
// They live here rather than in a backend because they are not SQL: both
// backends store the same keys with the same spellings, and duplicating the
// translation would let the two drift into writing settings the other
// cannot read. What each backend owns is still its own SQL.
const (
	// SettingPublishDefault is what a gathering publishes when nothing
	// below it has said otherwise.
	SettingPublishDefault = "federation.publish.default"
	// SettingOrganizationOverride says whether an organization may
	// disagree with the instance default.
	SettingOrganizationOverride = "federation.publish.organization_override_allowed"
	// SettingGatheringOverride says whether a gathering may disagree with
	// what is above it.
	SettingGatheringOverride = "federation.publish.gathering_override_allowed"
	// SettingRetentionDays is how long a directory keeps what a peer sent,
	// after the event ends.
	SettingRetentionDays = "federation.retention_days"
	// SettingPeerRateLimit is how many events one peer may have accepted
	// per minute.
	SettingPeerRateLimit = "federation.peer_rate_limit_per_minute"
	// SettingOriginRateLimit is the same for one organization inside a peer.
	SettingOriginRateLimit = "federation.origin_rate_limit_per_minute"
)

// ApplySetting folds one stored row into settings.
//
// A value that does not parse is IGNORED and the default stands. One
// unreadable row must not make an instance unconfigurable, and a setting
// that silently keeps its default is far easier to notice and correct than
// a service that will not start.
func ApplySetting(settings *InstanceSettings, key, value string) {
	switch key {
	case SettingPublishDefault:
		if candidate := PublishSetting(value); ValidPublishSetting(candidate) && candidate != PublishUnset {
			settings.PublishDefault = candidate
		}
	case SettingOrganizationOverride:
		if parsed, err := strconv.ParseBool(value); err == nil {
			settings.OrganizationOverrideAllowed = parsed
		}
	case SettingGatheringOverride:
		if parsed, err := strconv.ParseBool(value); err == nil {
			settings.GatheringOverrideAllowed = parsed
		}
	case SettingRetentionDays:
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed >= 0 {
			settings.RetentionDays = parsed
		}
	case SettingPeerRateLimit:
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed >= 0 {
			settings.PeerRateLimitPerMinute = parsed
		}
	case SettingOriginRateLimit:
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed >= 0 {
			settings.OriginRateLimitPerMinute = parsed
		}
	}
}

// SettingRows flattens settings into the rows to write.
func SettingRows(in InstanceSettings) [][2]string {
	return [][2]string{
		{SettingPublishDefault, string(in.PublishDefault)},
		{SettingOrganizationOverride, strconv.FormatBool(in.OrganizationOverrideAllowed)},
		{SettingGatheringOverride, strconv.FormatBool(in.GatheringOverrideAllowed)},
		{SettingRetentionDays, strconv.FormatInt(in.RetentionDays, 10)},
		{SettingPeerRateLimit, strconv.FormatInt(in.PeerRateLimitPerMinute, 10)},
		{SettingOriginRateLimit, strconv.FormatInt(in.OriginRateLimitPerMinute, 10)},
	}
}
