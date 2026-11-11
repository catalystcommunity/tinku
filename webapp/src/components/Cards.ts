// The list-item shapes, so an organization, a gathering and an event look
// the same wherever they are listed — the discover screen and the "mine"
// screen use the same builders rather than two near-identical markups.
//
// A card is plain DOM with no state. A list makes its cards again when its
// data changes (see `syncKeyed` in ui/dom.ts to keep the focus on a link).
import type { Event, EventSeries, Gathering, Organization, RemoteEvent } from "~/gen/types.gen";
import { dateTime, plural, t } from "~/i18n";
import { describeRecurrence } from "~/i18n/recurrence";
import { safeUrl } from "~/lib/safeUrl";
import { h } from "~/ui/dom";
import { originBadge } from "./OriginBadge";

const badge = (text: string, strong = false) =>
  h("span", { class: strong ? "badge badge-strong" : "badge" }, text);

export function organizationCard(organization: Organization): HTMLLIElement {
  return h(
    "li",
    { class: "card" },
    h("h3", null, h("a", { href: `/organizations/${organization.id}` }, organization.name)),
    h("p", null, originBadge(organization.origin, organization.slug)),
    organization.blurb ? h("p", { class: "card-blurb" }, organization.blurb) : null,
    h(
      "p",
      { class: "card-meta" },
      plural("organization.members", organization.memberCount),
      " · ",
      plural("organization.owners", organization.ownerCount),
      organization.viewer.isOwner && [" · ", badge(t("roles.owner"))],
    ),
  );
}

export function gatheringCard(gathering: Gathering): HTMLLIElement {
  return h(
    "li",
    { class: "card" },
    h("h3", null, h("a", { href: `/gatherings/${gathering.id}` }, gathering.name)),
    h("p", null, originBadge(gathering.origin, gathering.slug)),
    gathering.blurb ? h("p", { class: "card-blurb" }, gathering.blurb) : null,
    h(
      "p",
      { class: "card-meta" },
      plural("gathering.members", gathering.memberCount),
      " · ",
      plural("gathering.events", gathering.eventCount),
      gathering.viewer.isOwner && [" · ", badge(t("roles.owner"))],
      gathering.viewer.isMember && !gathering.viewer.isOwner && [" · ", badge(t("roles.member"))],
    ),
    h(
      "p",
      { class: "card-when" },
      gathering.nextEventAt
        ? t("gathering.nextEvent", { when: dateTime(gathering.nextEventAt) })
        : t("gathering.nothingScheduled"),
    ),
  );
}

function eventMode(event: { isOnline: boolean; isInPerson: boolean }): string {
  if (event.isOnline && event.isInPerson) return t("event.onlineAndInPerson");
  return event.isOnline ? t("event.online") : t("event.inPerson");
}

/*
 * The instant is rendered in the event's OWN timezone. A meetup at 19:00 in
 * Denver is at 19:00 in Denver whoever is reading, and silently converting
 * it to the reader's zone is how somebody arrives at the wrong hour.
 */
function when(startsAt: Date, timezone: string): HTMLElement {
  return h(
    "p",
    { class: "card-when" },
    h("time", { datetime: new Date(startsAt).toISOString() }, dateTime(startsAt, timezone)),
    " ",
    h("span", { class: "card-tz" }, `(${timezone})`),
  );
}

export function eventCard(event: Event): HTMLLIElement {
  return h(
    "li",
    { class: event.locked ? "card card-locked" : "card" },
    h("h3", null, h("a", { href: `/events/${event.id}` }, event.title)),
    when(event.startsAt, event.timezone),
    h(
      "p",
      { class: "card-meta" },
      eventMode(event),
      event.location?.locality ? ` · ${event.location.locality}` : null,
      " · ",
      plural("event.attendees", event.attendeeCount),
      event.seriesId && [" · ", badge(t("event.partOfSeries"))],
      event.viewerAttending && [" · ", badge(t("event.attending"), true)],
    ),
    event.locked ? h("p", { class: "card-locked-note" }, t("event.started")) : null,
  );
}

export function seriesCard(series: EventSeries): HTMLLIElement {
  return h(
    "li",
    { class: "card" },
    h("h3", null, h("a", { href: `/gatherings/${series.gatheringId}` }, series.title)),
    h(
      "p",
      { class: "card-when" },
      describeRecurrence(t, series.recurrence, series.startTime),
      " ",
      h("span", { class: "card-tz" }, `(${series.timezone})`),
    ),
    h(
      "p",
      { class: "card-meta" },
      plural("event.occurrences", series.occurrenceCount),
      series.nextOccurrenceAt
        ? ` · ${t("gathering.nextEvent", { when: dateTime(series.nextOccurrenceAt, series.timezone) })}`
        : null,
    ),
  );
}

/**
 * An event another instance sent.
 *
 * It links OUT rather than in: a delivery is a summary, and everything it
 * leaves out — the description, who is attending, whether you may join —
 * lives only at the origin. Pretending otherwise would be showing a page
 * that cannot answer the next question a reader has.
 */
export function remoteEventCard(event: RemoteEvent): HTMLLIElement {
  // A peer wrote this URL. It is a link only if it is one this app will
  // vouch for; anything else shows as plain text rather than as an href
  // pointing wherever the sender chose.
  const href = safeUrl(event.canonicalUrl);
  return h(
    "li",
    { class: "card" },
    h("h3", null, href ? h("a", { href, rel: "noreferrer noopener" }, event.title) : event.title),
    when(event.startsAt, event.timezone),
    h(
      "p",
      { class: "card-meta" },
      eventMode(event),
      event.location?.locality ? ` · ${event.location.locality}` : null,
      event.gatheringName ? ` · ${event.gatheringName}` : null,
    ),
    // Where it came from, said plainly. A directory that hides the origin
    // of what it lists is asking to be trusted for somebody else's content.
    h("p", null, originBadge(event.origin)),
  );
}

/** A list of cards, or the empty text. */
export function cardList<T>(items: readonly T[] | undefined, card: (item: T) => HTMLElement, empty: string): HTMLElement {
  if (!items || items.length === 0) return h("p", null, empty);
  return h("ul", { class: "cards" }, items.map(card));
}
