package csilservices

import (
	"context"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/store"
	"github.com/catalystcommunity/tinku/api/internal/webhooks"
)

// The seam between "something changed" and "somebody is told".
//
// Every call here is fire-and-forget by design. The change has already been
// written and the caller's request has already succeeded; failing it
// afterwards because a notification could not be queued would report a
// failure that did not happen. webhooks.Dispatch logs instead.
//
// Two rules the services must keep:
//
//   - Dispatch AFTER the write, never before. A notification for a change
//     that then failed is worse than a late one, because a receiver acts on
//     it and there is nothing to act on.
//
//   - Attach the details every time. The dispatcher DROPS them for any
//     webhook whose owner did not ask for them, so this layer has no
//     permission question to answer: the one decision — send the record or
//     send a pointer — was made by the owner when they set the webhook up,
//     with the warning in front of them.

// organizationsOwning lists the organizations that own one gathering.
//
// A gathering can be owned by several at once, and each of them decides for
// itself whether it wants to hear about this — so the answer is a list, not
// "the organization".
func organizationsOwning(ctx context.Context, st store.Store, gatheringID string) []string {
	gathering, err := st.GatheringByID(ctx, gatheringID)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, owner := range gathering.Owners {
		if owner.Kind == store.OwnerKindOrganization {
			out = append(out, owner.ID)
		}
	}
	return out
}

// notifyGathering reports a change to a gathering itself.
func (s *GatheringService) notifyGathering(ctx context.Context, action webhooks.Action, gathering *store.Gathering, organizations []string) {
	s.Notify.Dispatch(ctx, webhooks.Change{
		Action:          action,
		Subject:         webhooks.SubjectGathering,
		ID:              gathering.ID,
		Name:            gathering.Name,
		GatheringID:     gathering.ID,
		OrganizationIDs: organizations,
		Details: &webhooks.Details{
			Blurb:       gathering.Blurb,
			Description: gathering.Description,
		},
		At: time.Now(),
	})
}

// notifyOrganization reports a change to an organization itself. The
// organization is its own audience: a webhook on it hears about it.
func (s *OrganizationService) notifyOrganization(ctx context.Context, action webhooks.Action, organization *store.Organization) {
	s.Notify.Dispatch(ctx, webhooks.Change{
		Action:          action,
		Subject:         webhooks.SubjectOrganization,
		ID:              organization.ID,
		Name:            organization.Name,
		OrganizationIDs: []string{organization.ID},
		Details: &webhooks.Details{
			Blurb:       organization.Blurb,
			Description: organization.Description,
		},
		At: time.Now(),
	})
}

// notifyEvent reports a change to one event.
//
// Deleting an OCCURRENCE of a series is reported as a cancellation rather
// than a deletion: the series still stands and the other occurrences still
// happen, which is exactly what a person means when they call off one
// evening. Deleting a standalone event is a deletion, because there is
// nothing left behind it.
func (s *EventService) notifyEvent(ctx context.Context, action webhooks.Action, event *store.Event) {
	if action == webhooks.ActionDeleted && event.SeriesID != nil && *event.SeriesID != "" {
		action = webhooks.ActionCancelled
	}
	s.Notify.Dispatch(ctx, webhooks.Change{
		Action:          action,
		Subject:         webhooks.SubjectEvent,
		ID:              event.ID,
		Name:            event.Title,
		GatheringID:     event.GatheringID,
		OrganizationIDs: organizationsOwning(ctx, s.Store, event.GatheringID),
		Details:         eventDetails(event),
		At:              time.Now(),
	})
}

// notifySeries reports a change to a rule. The occurrences a rule writes are
// not reported one by one: a monthly rule with a year's horizon would send
// twelve deliveries for one edit, and the rule is the thing that changed.
func (s *EventService) notifySeries(ctx context.Context, action webhooks.Action, series *store.EventSeries) {
	s.Notify.Dispatch(ctx, webhooks.Change{
		Action:          action,
		Subject:         webhooks.SubjectSeries,
		ID:              series.ID,
		Name:            series.Title,
		GatheringID:     series.GatheringID,
		OrganizationIDs: organizationsOwning(ctx, s.Store, series.GatheringID),
		Details:         seriesDetails(series),
		At:              time.Now(),
	})
}

// eventDetails is one event as a reader of it would see it.
func eventDetails(event *store.Event) *webhooks.Details {
	details := &webhooks.Details{
		Description: event.Description,
		StartsAt:    &event.StartsAt,
		EndsAt:      &event.EndsAt,
		Timezone:    event.Timezone,
		IsOnline:    event.IsOnline,
		IsInPerson:  event.IsInPerson,
		OnlineURL:   event.OnlineURL,
		Location:    locationDetails(event.Location),
	}
	return details
}

// seriesDetails is one rule, said in the schema's own terms.
//
// Not a sentence: this server does not know the language of whoever reads
// the delivery, and a rule written out in words cannot be acted on by a
// program. start_time and timezone travel together and are NOT flattened
// into an instant — together they are the rule, and the same rule is a
// different UTC time in summer and in winter.
func seriesDetails(series *store.EventSeries) *webhooks.Details {
	return &webhooks.Details{
		Description: series.Description,
		Timezone:    series.Timezone,
		IsOnline:    series.IsOnline,
		IsInPerson:  series.IsInPerson,
		OnlineURL:   series.OnlineURL,
		Location:    locationDetails(series.Location),
		Recurrence: &webhooks.Recurrence{
			Freq:       string(series.Recurrence.Freq),
			Interval:   series.Recurrence.Interval,
			Ordinal:    series.Recurrence.Ordinal,
			DayOfMonth: series.Recurrence.DayOfMonth,
			Weekday:    weekdayString(series.Recurrence.Weekday),
			StartTime:  series.StartTime,
			Timezone:   series.Timezone,
		},
	}
}

func locationDetails(location store.Location) *webhooks.Location {
	if location.IsZero() {
		return nil
	}
	return &webhooks.Location{
		Name:       location.Name,
		Address:    location.Address,
		Locality:   location.Locality,
		Region:     location.Region,
		PostalCode: location.PostalCode,
		Country:    location.Country,
	}
}

// weekdayString flattens the optional weekday of a rule. A rule that names
// no weekday — "the 15th of every month" — sends none rather than an empty
// string that reads as a day.
func weekdayString(weekday *store.Weekday) string {
	if weekday == nil {
		return ""
	}
	return string(*weekday)
}
