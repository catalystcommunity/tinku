package csilservices

import (
	"testing"

	"github.com/catalystcommunity/tinku/api/internal/store"
)

// The three-level publish rule, as a table. It is pure logic over three
// inputs, so it is tested as pure logic — the end-to-end tests then only
// have to prove it is actually consulted.

func gatheringWith(setting store.PublishSetting) *store.Gathering {
	return &store.Gathering{PublishEvents: setting}
}

func organizationsWith(settings ...store.PublishSetting) []store.Organization {
	out := make([]store.Organization, 0, len(settings))
	for _, s := range settings {
		out = append(out, store.Organization{PublishEvents: s})
	}
	return out
}

func TestPublishResolution(t *testing.T) {
	allowAll := store.InstanceSettings{
		PublishDefault:              store.PublishIn,
		OrganizationOverrideAllowed: true,
		GatheringOverrideAllowed:    true,
	}
	defaultOut := allowAll
	defaultOut.PublishDefault = store.PublishOut

	noOrgOverride := allowAll
	noOrgOverride.OrganizationOverrideAllowed = false

	noGatheringOverride := allowAll
	noGatheringOverride.GatheringOverrideAllowed = false

	cases := []struct {
		name          string
		settings      store.InstanceSettings
		gathering     *store.Gathering
		organizations []store.Organization
		wantPublish   bool
		wantSource    string
		wantOverride  bool
	}{
		{
			name:     "nothing set anywhere follows the instance default",
			settings: allowAll, gathering: gatheringWith(store.PublishUnset),
			wantPublish: true, wantSource: "instance", wantOverride: true,
		},
		{
			name:     "an instance default of out publishes nothing",
			settings: defaultOut, gathering: gatheringWith(store.PublishUnset),
			wantPublish: false, wantSource: "instance", wantOverride: true,
		},
		{
			name:     "an organization can turn it off",
			settings: allowAll, gathering: gatheringWith(store.PublishUnset),
			organizations: organizationsWith(store.PublishOut),
			wantPublish:   false, wantSource: "organization", wantOverride: true,
		},
		{
			name:     "an organization can turn it on against an instance default of out",
			settings: defaultOut, gathering: gatheringWith(store.PublishUnset),
			organizations: organizationsWith(store.PublishIn),
			wantPublish:   true, wantSource: "organization", wantOverride: true,
		},
		{
			// Publishing somebody's events is the act that cannot be taken
			// back, so among several owners the most restrictive wins.
			name:     "among owners that disagree, out wins",
			settings: allowAll, gathering: gatheringWith(store.PublishUnset),
			organizations: organizationsWith(store.PublishIn, store.PublishOut),
			wantPublish:   false, wantSource: "organization", wantOverride: true,
		},
		{
			name:     "a gathering has the last word",
			settings: allowAll, gathering: gatheringWith(store.PublishOut),
			organizations: organizationsWith(store.PublishIn),
			wantPublish:   false, wantSource: "gathering", wantOverride: true,
		},
		{
			name:     "a gathering can turn it on over an organization that turned it off",
			settings: allowAll, gathering: gatheringWith(store.PublishIn),
			organizations: organizationsWith(store.PublishOut),
			wantPublish:   true, wantSource: "gathering", wantOverride: true,
		},
		{
			// A level that may not override is skipped entirely, not
			// consulted and ignored.
			name:     "an organization that may not override is not consulted",
			settings: noOrgOverride, gathering: gatheringWith(store.PublishUnset),
			organizations: organizationsWith(store.PublishOut),
			wantPublish:   true, wantSource: "instance", wantOverride: true,
		},
		{
			name:     "a gathering that may not override is not consulted",
			settings: noGatheringOverride, gathering: gatheringWith(store.PublishOut),
			wantPublish: true, wantSource: "instance", wantOverride: false,
		},
		{
			name:     "with a gathering barred, an organization still decides",
			settings: noGatheringOverride, gathering: gatheringWith(store.PublishIn),
			organizations: organizationsWith(store.PublishOut),
			wantPublish:   false, wantSource: "organization", wantOverride: false,
		},
		{
			// An individual owner has no publish setting of its own, so a
			// gathering owned only by people follows the instance.
			name:     "a gathering with no organization owner follows the instance",
			settings: allowAll, gathering: gatheringWith(store.PublishUnset),
			wantPublish: true, wantSource: "instance", wantOverride: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolvePublish(tc.settings, tc.gathering, tc.organizations)
			if got.Publishing != tc.wantPublish {
				t.Errorf("publishing = %v, want %v", got.Publishing, tc.wantPublish)
			}
			if string(got.Source) != tc.wantSource {
				t.Errorf("source = %q, want %q", got.Source, tc.wantSource)
			}
			if got.CanOverride != tc.wantOverride {
				t.Errorf("can_override = %v, want %v", got.CanOverride, tc.wantOverride)
			}
		})
	}
}

// The defaults have to be the ones the domain states, because an instance
// that sets nothing runs on them.
func TestDefaultInstanceSettings(t *testing.T) {
	settings := store.DefaultInstanceSettings()
	if settings.PublishDefault != store.PublishIn {
		t.Errorf("publish default is %q, want in", settings.PublishDefault)
	}
	if !settings.OrganizationOverrideAllowed || !settings.GatheringOverrideAllowed {
		t.Error("overrides are barred by default; they should be allowed")
	}
	if settings.RetentionDays != 365 {
		t.Errorf("retention is %d days, want 365", settings.RetentionDays)
	}
	if settings.PeerRateLimitPerMinute <= 0 {
		t.Errorf("the peer rate limit is %d, want a positive default", settings.PeerRateLimitPerMinute)
	}
}

// A value that does not parse leaves the default standing. One unreadable
// row must not make an instance unconfigurable.
func TestUnreadableSettingKeepsTheDefault(t *testing.T) {
	settings := store.DefaultInstanceSettings()
	store.ApplySetting(&settings, store.SettingRetentionDays, "not a number")
	store.ApplySetting(&settings, store.SettingPublishDefault, "sideways")
	store.ApplySetting(&settings, store.SettingOrganizationOverride, "perhaps")

	if settings.RetentionDays != 365 {
		t.Errorf("retention became %d, want the default 365", settings.RetentionDays)
	}
	if settings.PublishDefault != store.PublishIn {
		t.Errorf("publish default became %q, want in", settings.PublishDefault)
	}
	if !settings.OrganizationOverrideAllowed {
		t.Error("an unparsable override value changed the setting")
	}
}

// A round trip through the key/value rows must not lose anything, or a
// setting an administrator changed would silently revert on the next read.
func TestSettingsRoundTrip(t *testing.T) {
	original := store.InstanceSettings{
		PublishDefault:              store.PublishOut,
		OrganizationOverrideAllowed: false,
		GatheringOverrideAllowed:    true,
		RetentionDays:               30,
		PeerRateLimitPerMinute:      5,
	}
	restored := store.DefaultInstanceSettings()
	for _, row := range store.SettingRows(original) {
		store.ApplySetting(&restored, row[0], row[1])
	}
	if restored != original {
		t.Errorf("round trip gave %+v, want %+v", restored, original)
	}
}
