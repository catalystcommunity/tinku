package csilservices

import (
	"context"
	"errors"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/store"
)

// Whether a gathering's events go to peer directories.
//
// # The rule
//
// Three levels answer, and the MOST SPECIFIC level that is both SET and
// ALLOWED wins:
//
//	gathering        if the instance allows a gathering to override
//	organization     if the instance allows an organization to override
//	instance default otherwise
//
// "Unset" is a real third state at each level, not a synonym for "out": it
// means that level said nothing and the one above decides. That is why the
// columns are nullable text and the wire type is an enumeration — a boolean
// cannot tell "no" from "nothing".
//
// A gathering can be owned by several organizations at once. Among them, an
// explicit `out` beats an explicit `in`: publishing somebody's events is
// the act that cannot be taken back, so the most restrictive owner wins.
//
// The instance can withdraw the right to override at either level. A level
// that may not override is skipped entirely rather than consulted and
// ignored, and the answer says so — which is what lets a screen hide a
// control instead of showing one that silently does nothing.
func resolvePublish(
	settings store.InstanceSettings,
	gathering *store.Gathering,
	owningOrganizations []store.Organization,
) csil.PublishDecision {
	decision := csil.PublishDecision{
		Publishing: settings.PublishDefault == store.PublishIn,
		Source:     "instance",
		// Whether THIS gathering may still change the answer. It is the
		// gathering's own control a client renders, so this reports the
		// gathering's right, not the organization's.
		CanOverride: settings.GatheringOverrideAllowed,
	}

	if settings.OrganizationOverrideAllowed {
		var sawIn, sawOut bool
		for i := range owningOrganizations {
			switch owningOrganizations[i].PublishEvents {
			case store.PublishIn:
				sawIn = true
			case store.PublishOut:
				sawOut = true
			}
		}
		// Most restrictive owner wins.
		if sawOut {
			decision.Publishing = false
			decision.Source = "organization"
		} else if sawIn {
			decision.Publishing = true
			decision.Source = "organization"
		}
	}

	if settings.GatheringOverrideAllowed && gathering != nil {
		switch gathering.PublishEvents {
		case store.PublishIn:
			decision.Publishing = true
			decision.Source = "gathering"
		case store.PublishOut:
			decision.Publishing = false
			decision.Source = "gathering"
		}
	}

	return decision
}

// publishDecisionFor resolves the rule for one gathering, reading the
// settings and the owning organizations it needs.
//
// It is deliberately a lookup per call rather than something cached: the
// settings are five rows, the owners are already on the gathering, and a
// stale cache here would mean publishing something an administrator has
// just told the instance not to.
func publishDecisionFor(ctx context.Context, st store.Store, gathering *store.Gathering) (csil.PublishDecision, error) {
	settings, err := st.InstanceSettings(ctx)
	if err != nil {
		return csil.PublishDecision{}, err
	}

	var organizations []store.Organization
	if settings.OrganizationOverrideAllowed && gathering != nil {
		for _, owner := range gathering.Owners {
			if owner.Kind != store.OwnerKindOrganization {
				continue
			}
			organization, err := st.OrganizationByID(ctx, owner.ID)
			if errors.Is(err, store.ErrNotFound) {
				// An owner row that points at nothing says nothing. There is
				// no setting to read, so there is nothing to skip.
				continue
			}
			if err != nil {
				// An owner that cannot be READ must not be silently dropped.
				// Dropping it is not conservative: an organization holding
				// `out` would vanish from the resolution and the instance
				// default — which may be `in` — would publish events that
				// organization opted out of. Publishing cannot be undone, so
				// a failure to read fails the whole decision.
				return csil.PublishDecision{}, err
			}
			organizations = append(organizations, *organization)
		}
	}
	return resolvePublish(settings, gathering, organizations), nil
}

// publishSettingOf converts the wire enumeration, treating anything
// unrecognized as unset.
func publishSettingOf(p *csil.PublishSetting) (store.PublishSetting, bool) {
	if p == nil {
		return store.PublishUnset, false
	}
	switch *p {
	case "in":
		return store.PublishIn, true
	case "out":
		return store.PublishOut, true
	case "unset":
		return store.PublishUnset, true
	}
	return store.PublishUnset, false
}

// wirePublishSetting is the inverse: an unset column reads as "unset" on
// the wire rather than as an empty string.
func wirePublishSetting(p store.PublishSetting) csil.PublishSetting {
	if p == store.PublishUnset {
		return "unset"
	}
	return csil.PublishSetting(p)
}
