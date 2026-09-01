// The three list-item shapes, so an organization, a gathering and an event look the
// same wherever they are listed — the discover screen and the "mine" screen
// render the same components rather than two near-identical markups.
import { A } from "@solidjs/router";
import { Show, type JSX } from "solid-js";
import type { Event, EventSeries, Gathering, Organization, RemoteEvent } from "~/gen/types.gen";
import { useI18n } from "~/i18n";
import { OriginBadge } from "./OriginBadge";
import { describeRecurrence } from "~/i18n/recurrence";
import { safeUrl } from "~/lib/safeUrl";

export function OrganizationCard(props: { organization: Organization }): JSX.Element {
  const { t, plural } = useI18n();
  return (
    <li class="card">
      <h3>
        <A href={`/organizations/${props.organization.id}`}>{props.organization.name}</A>
      </h3>
      <p>
        <OriginBadge origin={props.organization.origin} local={props.organization.slug} />
      </p>
      <Show when={props.organization.blurb}>
        <p class="card-blurb">{props.organization.blurb}</p>
      </Show>
      <p class="card-meta">
        {plural("organization.members", props.organization.memberCount)} ·{" "}
        {plural("organization.owners", props.organization.ownerCount)}
        <Show when={props.organization.viewer.isOwner}>
          {" "}
          · <span class="badge">{t("roles.owner")}</span>
        </Show>
      </p>
    </li>
  );
}

export function GatheringCard(props: { gathering: Gathering }): JSX.Element {
  const { t, plural, dateTime } = useI18n();
  return (
    <li class="card">
      <h3>
        <A href={`/gatherings/${props.gathering.id}`}>{props.gathering.name}</A>
      </h3>
      <p>
        <OriginBadge origin={props.gathering.origin} local={props.gathering.slug} />
      </p>
      <Show when={props.gathering.blurb}>
        <p class="card-blurb">{props.gathering.blurb}</p>
      </Show>
      <p class="card-meta">
        {plural("gathering.members", props.gathering.memberCount)} ·{" "}
        {plural("gathering.events", props.gathering.eventCount)}
        <Show when={props.gathering.viewer.isOwner}>
          {" "}
          · <span class="badge">{t("roles.owner")}</span>
        </Show>
        <Show when={props.gathering.viewer.isMember && !props.gathering.viewer.isOwner}>
          {" "}
          · <span class="badge">{t("roles.member")}</span>
        </Show>
      </p>
      <p class="card-when">
        <Show
          when={props.gathering.nextEventAt}
          fallback={t("gathering.nothingScheduled")}
        >
          {(when) => t("gathering.nextEvent", { when: dateTime(when()) })}
        </Show>
      </p>
    </li>
  );
}

export function EventCard(props: { event: Event }): JSX.Element {
  const { t, plural, dateTime } = useI18n();
  const mode = () => {
    if (props.event.isOnline && props.event.isInPerson) return t("event.onlineAndInPerson");
    return props.event.isOnline ? t("event.online") : t("event.inPerson");
  };
  return (
    <li class="card" classList={{ "card-locked": props.event.locked }}>
      <h3>
        <A href={`/events/${props.event.id}`}>{props.event.title}</A>
      </h3>
      {/*
        The instant is rendered in the event's OWN timezone. A meetup at
        19:00 in Denver is at 19:00 in Denver whoever is reading, and
        silently converting it to the reader's zone is how somebody arrives
        at the wrong hour.
      */}
      <p class="card-when">
        <time datetime={new Date(props.event.startsAt).toISOString()}>
          {dateTime(props.event.startsAt, props.event.timezone)}
        </time>{" "}
        <span class="card-tz">({props.event.timezone})</span>
      </p>
      <p class="card-meta">
        {mode()}
        <Show when={props.event.location?.locality}>
          {(locality) => <> · {locality()}</>}
        </Show>{" "}
        · {plural("event.attendees", props.event.attendeeCount)}
        <Show when={props.event.seriesId}>
          {" "}
          · <span class="badge">{t("event.partOfSeries")}</span>
        </Show>
        <Show when={props.event.viewerAttending}>
          {" "}
          · <span class="badge badge-strong">{t("event.attending")}</span>
        </Show>
      </p>
      <Show when={props.event.locked}>
        <p class="card-locked-note">{t("event.started")}</p>
      </Show>
    </li>
  );
}

export function SeriesCard(props: { series: EventSeries }): JSX.Element {
  const { t, plural, dateTime } = useI18n();
  return (
    <li class="card">
      <h3>
        <A href={`/gatherings/${props.series.gatheringId}`}>{props.series.title}</A>
      </h3>
      <p class="card-when">
        {describeRecurrence(t, props.series.recurrence, props.series.startTime)}{" "}
        <span class="card-tz">({props.series.timezone})</span>
      </p>
      <p class="card-meta">
        {plural("event.occurrences", props.series.occurrenceCount)}
        <Show when={props.series.nextOccurrenceAt}>
          {(when) => <> · {t("gathering.nextEvent", { when: dateTime(when(), props.series.timezone) })}</>}
        </Show>
      </p>
    </li>
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
export function RemoteEventCard(props: { event: RemoteEvent }): JSX.Element {
  const { t, dateTime } = useI18n();
  const mode = () => {
    if (props.event.isOnline && props.event.isInPerson) return t("event.onlineAndInPerson");
    return props.event.isOnline ? t("event.online") : t("event.inPerson");
  };
  return (
    <li class="card">
      <h3>
        {/* A peer wrote this URL. It is a link only if it is one this app
            will vouch for; anything else shows as plain text rather than as
            an href pointing wherever the sender chose. */}
        <Show when={safeUrl(props.event.canonicalUrl)} fallback={props.event.title}>
          {(href) => (
            <a href={href()} rel="noreferrer noopener">
              {props.event.title}
            </a>
          )}
        </Show>
      </h3>
      <p class="card-when">
        <time datetime={new Date(props.event.startsAt).toISOString()}>
          {dateTime(props.event.startsAt, props.event.timezone)}
        </time>{" "}
        <span class="card-tz">({props.event.timezone})</span>
      </p>
      <p class="card-meta">
        {mode()}
        <Show when={props.event.location?.locality}>{(locality) => <> · {locality()}</>}</Show>
        <Show when={props.event.gatheringName}>{(name) => <> · {name()}</>}</Show>
      </p>
      {/* Where it came from, said plainly. A directory that hides the origin
          of what it lists is asking to be trusted for somebody else's
          content. */}
      <p>
        <OriginBadge origin={props.event.origin} />
      </p>
    </li>
  );
}
