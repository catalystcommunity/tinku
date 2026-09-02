import { useNavigate, useParams } from "@solidjs/router";
import { For, Show, createEffect, createResource, createSignal, on, type JSX } from "solid-js";
import { createStore } from "solid-js/store";
import type { CreateEventSeriesRequest, RecurrenceFreq, Weekday } from "~/gen/types.gen";
import { useI18n } from "~/i18n";
import { api } from "~/lib/api";
import { useSession } from "~/lib/session";
import { ErrorAlert, StatusMessage } from "~/components/Alert";
import { CheckField, Field } from "~/components/Field";
import { EventCard, SeriesCard } from "~/components/Cards";
import { AdoptGathering, OfferGathering } from "~/components/Offers";
import { Webhooks } from "~/components/Webhooks";
import { OriginBadge } from "~/components/OriginBadge";
import { PublishControl } from "~/components/PublishControl";
import { Select } from "~/components/Select";
import { browserTimezone, instantFromWallClock } from "~/lib/zonedTime";

const WEEKDAYS: Weekday[] = [
  "monday",
  "tuesday",
  "wednesday",
  "thursday",
  "friday",
  "saturday",
  "sunday",
];
const FREQUENCIES: RecurrenceFreq[] = ["weekly", "monthly", "quarterly", "yearly"];
// A const tuple, so `recurrence.ordinal.${o}` is a catalog key TypeScript
// can check rather than an arbitrary `${number}`.
const ORDINALS = [1, 2, 3, 4, 5, -1] as const;

/**
 * The IANA zones this browser knows, for the picker. A free-text field
 * would let an organizer type a zone the server then rejects, and the
 * rejection would arrive after they had filled in the whole form.
 */
function knownTimezones(): string[] {
  const supported = (Intl as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf;
  try {
    return supported ? supported("timeZone") : [];
  } catch {
    return [];
  }
}

/**
 * A timezone control: a select where the browser can list the IANA zones,
 * and a text input where it cannot. Both spellings reach the same field, so
 * the caller does not care which one rendered.
 */
function TimezonePicker(props: {
  control: Record<string, unknown>;
  value: string;
  onChange: (timezone: string) => void;
}): JSX.Element {
  const zones = knownTimezones();
  return (
    <Show
      when={zones.length > 0}
      fallback={
        <input
          {...props.control}
          type="text"
          value={props.value}
          onInput={(e) => props.onChange(e.currentTarget.value)}
        />
      }
    >
      <Select
        control={props.control}
        value={props.value}
        options={zones}
        label={(zone) => zone}
        onChange={props.onChange}
      />
    </Show>
  );
}

/**
 * One gathering: what it is, who owns it, what is scheduled, and — for an
 * owner — the forms that schedule more.
 */
export default function GatheringDetail(): JSX.Element {
  const { t, plural, dateTime } = useI18n();
  const params = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { user } = useSession();

  const [gathering, { refetch: refetchGathering, mutate }] = createResource(
    () => params.id,
    (id) => api.gathering.getGathering({ id }),
  );
  const [events, { refetch: refetchEvents }] = createResource(
    () => params.id,
    (gatheringId) => api.event.listEvents({ gatheringId }),
  );
  const [series, { refetch: refetchSeries }] = createResource(
    () => params.id,
    (gatheringId) => api.event.listEventSeries({ gatheringId }),
  );

  const [error, setError] = createSignal<unknown>();
  const [status, setStatus] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  // Which of the two schedule forms is on screen. Both are always in the
  // document — `hidden` rather than `Show` — so that what a person has typed
  // into one survives a look at the other.
  const [scheduleTab, setScheduleTab] = createSignal<"single" | "series">("single");

  const refetchAll = () => Promise.all([refetchGathering(), refetchEvents(), refetchSeries()]);

  const membership = async (join: boolean) => {
    setBusy(true);
    setError(undefined);
    try {
      const updated = join
        ? await api.gathering.joinGathering({ gatheringId: params.id })
        : await api.gathering.leaveGathering({ gatheringId: params.id });
      mutate(updated);
      setStatus(t(join ? "gathering.joined" : "gathering.left"));
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    const current = gathering();
    if (!current) return;
    if (!window.confirm(t("common.confirmDelete", { name: current.name }))) return;
    try {
      await api.gathering.deleteGathering({ id: params.id });
      navigate("/gatherings");
    } catch (err) {
      setError(err);
    }
  };

  // ---- Scheduling a single event ----
  const blankSingle = () => ({
    title: "",
    description: "",
    isOnline: true,
    isInPerson: false,
    onlineUrl: "",
    startsAt: "",
    endsAt: "",
    timezone: browserTimezone(),
    locationName: "",
    address: "",
    locality: "",
    region: "",
    postalCode: "",
    country: "",
  });
  const [single, setSingle] = createStore(blankSingle());

  // What the two controls add up to, in UTC and in the reader's own zone.
  // Empty until both are filled, so it never shows a half-formed answer.
  const singlePreview = () => {
    if (!single.startsAt || !single.timezone) return "";
    try {
      const instant = instantFromWallClock(single.startsAt, single.timezone);
      return t("event.resolvesTo", {
        utc: instant.toISOString().replace(".000Z", "Z"),
        local: dateTime(instant),
      });
    } catch {
      return "";
    }
  };

  const createEvent = async (e: SubmitEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      await api.event.createEvent({
        gatheringId: params.id,
        title: single.title,
        description: single.description,
        isOnline: single.isOnline,
        isInPerson: single.isInPerson,
        onlineUrl: single.onlineUrl || undefined,
        location: single.isInPerson
          ? {
              name: single.locationName,
              address: single.address,
              locality: single.locality,
              region: single.region,
              postalCode: single.postalCode,
              country: single.country.toUpperCase(),
            }
          : undefined,
        // Read in the zone the organizer chose, not the browser's — see
        // lib/zonedTime.ts for what goes wrong otherwise.
        startsAt: instantFromWallClock(single.startsAt, single.timezone),
        endsAt: instantFromWallClock(single.endsAt, single.timezone),
        timezone: single.timezone,
      });
      setSingle({ title: "", description: "", startsAt: "", endsAt: "" });
      await refetchAll();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  // ---- Scheduling a recurring set ----
  const blankRecurring = () => ({
    title: "",
    description: "",
    isOnline: true,
    isInPerson: false,
    freq: "monthly" as RecurrenceFreq,
    interval: 1,
    weekday: "thursday" as Weekday,
    ordinal: 2,
    startsOn: "",
    endsOn: "",
    startTime: "19:00",
    durationMinutes: 120,
    timezone: browserTimezone(),
    locality: "",
    locationName: "",
  });
  const [recurring, setRecurring] = createStore(blankRecurring());

  // The router REUSES this component when only :id changes, so the
  // resources refetch and nothing else does. Without this, walking from one
  // gathering to another leaves the first one's status message on screen
  // and a half-typed event still sitting in the form — pointed, now, at the
  // second gathering.
  createEffect(
    on(
      () => params.id,
      () => {
        setStatus("");
        setError(undefined);
        setSingle(blankSingle());
        setRecurring(blankRecurring());
      },
      { defer: true },
    ),
  );

  const createSeries = async (e: SubmitEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      const req: CreateEventSeriesRequest = {
        gatheringId: params.id,
        title: recurring.title,
        description: recurring.description,
        isOnline: recurring.isOnline,
        isInPerson: recurring.isInPerson,
        recurrence: {
          freq: recurring.freq,
          interval: recurring.interval,
          weekday: recurring.weekday,
          // A weekly rule has exactly one of each weekday per week, so an
          // ordinal would mean nothing there and is left off.
          ordinal: recurring.freq === "weekly" ? undefined : recurring.ordinal,
        },
        startsOn: instantFromWallClock(`${recurring.startsOn}T00:00`, recurring.timezone),
        endsOn: recurring.endsOn
          ? instantFromWallClock(`${recurring.endsOn}T23:59`, recurring.timezone)
          : undefined,
        startTime: recurring.startTime,
        durationMinutes: recurring.durationMinutes,
        timezone: recurring.timezone,
      };
      if (recurring.isInPerson) {
        req.location = {
          name: recurring.locationName,
          address: "",
          locality: recurring.locality,
          region: "",
          postalCode: "",
          country: "",
        };
      }
      await api.event.createEventSeries(req);
      setRecurring({ title: "", description: "", startsOn: "" });
      await refetchAll();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Show when={gathering()} fallback={<p>{t("common.loading")}</p>}>
      {(g) => (
        <>
          <h1>{g().name}</h1>
          <p class="page-address">
            <OriginBadge origin={g().origin} local={g().slug} />
          </p>
          {/* An external record that shares a local name is the case worth
              spelling out, not merely badging. */}
          <Show when={g().origin.isExternal}>
            <p class="alert alert-status" role="status">
              {t("origin.externalWarning", { domain: g().origin.domain })}
            </p>
          </Show>
          <p class="page-meta">
            <span>{plural("gathering.members", g().memberCount)}</span>
            <span>{plural("gathering.events", g().eventCount)}</span>
          </p>
          <Show when={g().blurb}>
            <p>{g().blurb}</p>
          </Show>
          <Show when={g().description}>
            <p class="long-form">{g().description}</p>
          </Show>

          {/* Publishing is a three-level choice; the server resolves it and
              sends the answer, so this renders rather than re-derives. */}
          <Show when={g().viewer.canEdit || g().publish.publishing}>
            <PublishControl
              value={g().publishEvents}
              decision={g().publish}
              canEdit={g().viewer.canEdit}
              onChange={(setting) =>
                void (async () => {
                  setError(undefined);
                  try {
                    mutate(
                      await api.gathering.updateGathering({
                        id: params.id,
                        publishEvents: setting,
                      }),
                    );
                  } catch (err) {
                    setError(err);
                  }
                })()
              }
            />
          </Show>

          <section>
            <h2>{t("gathering.owners")}</h2>
            <ul class="roster">
              <For each={g().owners}>
                {(owner) => (
                  <li>
                    <span class="roster-name">{owner.displayName}</span>{" "}
                    <span class="card-address">
                      {owner.handle}@{owner.originDomain}
                    </span>{" "}
                    <span class="badge">
                      {owner.kind === "organization" ? t("organization.singular") : t("roles.owner")}
                    </span>
                  </li>
                )}
              </For>
            </ul>
          </section>

          <ErrorAlert error={error()} />
          <Show when={status()}>{(s) => <StatusMessage>{s()}</StatusMessage>}</Show>

          <Show when={user()}>
            <p class="page-actions">
              <Show
                when={g().viewer.hasJoined}
                fallback={
                  <button type="button" onClick={() => membership(true)} disabled={busy()}>
                    {busy() ? t("gathering.joining") : t("gathering.join")}
                  </button>
                }
              >
                <button type="button" class="secondary" onClick={() => membership(false)} disabled={busy()}>
                  {busy() ? t("gathering.leaving") : t("gathering.leave")}
                </button>
              </Show>
            </p>
          </Show>

          <section>
            <h2>{t("event.upcoming")}</h2>
            <Show when={events()} fallback={<p>{t("common.loading")}</p>}>
              {(list) => (
                <Show when={list().events.length > 0} fallback={<p>{t("event.emptyList")}</p>}>
                  <ul class="cards">
                    <For each={list().events}>{(e) => <EventCard event={e} />}</For>
                  </ul>
                  <p class="card-meta">
                    {t("common.showing", { shown: list().events.length, total: list().total })}
                  </p>
                </Show>
              )}
            </Show>
          </section>

          <Show when={(series()?.series.length ?? 0) > 0}>
            <section>
              <h2>{t("event.seriesHeading")}</h2>
              <ul class="cards">
                <For each={series()?.series}>{(s) => <SeriesCard series={s} />}</For>
              </ul>
            </section>
          </Show>

          {/* Only an owner can schedule. The forms are absent for everybody
              else rather than disabled, because a form nobody may submit is
              a thing to tab through for no reason. */}
          <Show when={g().viewer.canEdit}>
            <section>
              <h2>{t("event.scheduleHeading")}</h2>
              {/*
                One event and a rule that makes many are two shapes of the
                same task, so they are one section with a switch. Both forms
                used to be open at once, which made the page twice as long
                as the job and left the reader to work out which half
                applied to them.
              */}
              <div class="tabs" role="tablist" aria-label={t("event.scheduleHeading")}>
                <button
                  type="button"
                  role="tab"
                  id="schedule-tab-single"
                  aria-selected={scheduleTab() === "single"}
                  aria-controls="schedule-panel-single"
                  onClick={() => setScheduleTab("single")}
                >
                  {t("event.tabSingle")}
                </button>
                <button
                  type="button"
                  role="tab"
                  id="schedule-tab-series"
                  aria-selected={scheduleTab() === "series"}
                  aria-controls="schedule-panel-series"
                  onClick={() => setScheduleTab("series")}
                >
                  {t("event.tabSeries")}
                </button>
              </div>

              <div
                id="schedule-panel-single"
                role="tabpanel"
                aria-labelledby="schedule-tab-single"
                hidden={scheduleTab() !== "single"}
              >
              <form onSubmit={createEvent}>
                <Field label={t("event.titleLabel")} required requiredText={t("common.required")}>
                  {(control) => (
                    <input
                      {...control}
                      type="text"
                      value={single.title}
                      onInput={(e) => setSingle("title", e.currentTarget.value)}
                    />
                  )}
                </Field>
                <Field label={t("event.descriptionLabel")} optionalText={t("common.optional")}>
                  {(control) => (
                    <textarea
                      {...control}
                      rows={4}
                      value={single.description}
                      onInput={(e) => setSingle("description", e.currentTarget.value)}
                    />
                  )}
                </Field>
                <div class="field-row">
  <Field label={t("event.startsAtLabel")} required requiredText={t("common.required")}>
                    {(control) => (
                      <input
                        {...control}
                        type="datetime-local"
                        value={single.startsAt}
                        onInput={(e) => setSingle("startsAt", e.currentTarget.value)}
                      />
                    )}
                  </Field>
                  <Field label={t("event.endsAtLabel")} required requiredText={t("common.required")}>
                    {(control) => (
                      <input
                        {...control}
                        type="datetime-local"
                        value={single.endsAt}
                        onInput={(e) => setSingle("endsAt", e.currentTarget.value)}
                      />
                    )}
                  </Field>
</div>
                <Field
                  label={t("event.timezoneLabel")}
                  hint={t("event.timezoneHint")}
                  required
                  requiredText={t("common.required")}
                >
                  {(control) => (
                    <TimezonePicker
                      control={control}
                      value={single.timezone}
                      onChange={(tz) => setSingle("timezone", tz)}
                    />
                  )}
                </Field>
                {/*
                  The instant the form is about to send, said back in plain
                  words. The zone and the clock reading are two controls, and
                  nothing else on screen shows what they add up to — which is
                  exactly where a two-hour mistake used to hide.
                */}
                <Show when={singlePreview()}>
                  {(preview) => (
                    <p class="field-hint" role="status" aria-live="polite">
                      {preview()}
                    </p>
                  )}
                </Show>
                <div class="check-row">
                  <CheckField
                    label={t("event.onlineLabel")}
                    checked={single.isOnline}
                    onChange={(v) => setSingle("isOnline", v)}
                  />
                  <CheckField
                    label={t("event.inPersonLabel")}
                    checked={single.isInPerson}
                    onChange={(v) => setSingle("isInPerson", v)}
                  />
                </div>
                <Show when={single.isOnline}>
                  <Field label={t("event.onlineUrlLabel")} optionalText={t("common.optional")}>
                    {(control) => (
                      <input
                        {...control}
                        type="url"
                        value={single.onlineUrl}
                        onInput={(e) => setSingle("onlineUrl", e.currentTarget.value)}
                      />
                    )}
                  </Field>
                </Show>
                {/* An in-person event needs a location, so the fieldset
                    appears exactly when the rule applies. */}
                <Show when={single.isInPerson}>
                  <fieldset>
                    <legend>{t("event.locationLegend")}</legend>
                    <Field label={t("event.locationNameLabel")}>
                      {(control) => (
                        <input
                          {...control}
                          type="text"
                          value={single.locationName}
                          onInput={(e) => setSingle("locationName", e.currentTarget.value)}
                        />
                      )}
                    </Field>
                    <Field label={t("event.addressLabel")}>
                      {(control) => (
                        <input
                          {...control}
                          type="text"
                          autocomplete="street-address"
                          value={single.address}
                          onInput={(e) => setSingle("address", e.currentTarget.value)}
                        />
                      )}
                    </Field>
                    <Field label={t("event.localityLabel")}>
                      {(control) => (
                        <input
                          {...control}
                          type="text"
                          autocomplete="address-level2"
                          value={single.locality}
                          onInput={(e) => setSingle("locality", e.currentTarget.value)}
                        />
                      )}
                    </Field>
                    <Field label={t("event.regionLabel")}>
                      {(control) => (
                        <input
                          {...control}
                          type="text"
                          autocomplete="address-level1"
                          value={single.region}
                          onInput={(e) => setSingle("region", e.currentTarget.value)}
                        />
                      )}
                    </Field>
                    <Field label={t("event.postalCodeLabel")}>
                      {(control) => (
                        <input
                          {...control}
                          type="text"
                          autocomplete="postal-code"
                          value={single.postalCode}
                          onInput={(e) => setSingle("postalCode", e.currentTarget.value)}
                        />
                      )}
                    </Field>
                    <Field label={t("event.countryLabel")}>
                      {(control) => (
                        <input
                          {...control}
                          type="text"
                          maxlength={2}
                          autocomplete="country"
                          value={single.country}
                          onInput={(e) => setSingle("country", e.currentTarget.value)}
                        />
                      )}
                    </Field>
                  </fieldset>
                </Show>
                <div class="form-actions">
                  <button
                    type="submit"
                    disabled={busy() || !single.title.trim() || !single.startsAt || !single.endsAt}
                  >
                    {busy() ? t("common.creating") : t("gathering.schedule")}
                  </button>
                </div>
              </form>
              </div>

              <div
                id="schedule-panel-series"
                role="tabpanel"
                aria-labelledby="schedule-tab-series"
                hidden={scheduleTab() !== "series"}
              >
              <form onSubmit={createSeries}>
                <Field label={t("event.titleLabel")} required requiredText={t("common.required")}>
                  {(control) => (
                    <input
                      {...control}
                      type="text"
                      value={recurring.title}
                      onInput={(e) => setRecurring("title", e.currentTarget.value)}
                    />
                  )}
                </Field>
                <Field label={t("event.descriptionLabel")} optionalText={t("common.optional")}>
                  {(control) => (
                    <textarea
                      {...control}
                      rows={3}
                      value={recurring.description}
                      onInput={(e) => setRecurring("description", e.currentTarget.value)}
                    />
                  )}
                </Field>
                <fieldset>
                  <legend>{t("recurrence.freqLabel")}</legend>
                  <div class="field-row">
  <Field label={t("recurrence.freqLabel")}>
                      {(control) => (
                        <Select
                          control={control}
                          value={recurring.freq}
                          options={FREQUENCIES}
                          label={(f) => t(`recurrence.freq.${f}`)}
                          onChange={(f) => setRecurring("freq", f)}
                        />
                      )}
                    </Field>
                    {/* An ordinal is meaningless for a weekly rule, so the
                        control is not offered for one. */}
                    <Show when={recurring.freq !== "weekly"}>
                      <Field label={t("recurrence.ordinalLabel")}>
                        {(control) => (
                          <Select
                            control={control}
                            value={String(recurring.ordinal)}
                            options={ORDINALS.map(String)}
                            label={(o) => t(`recurrence.ordinal.${Number(o) as (typeof ORDINALS)[number]}`)}
                            onChange={(o) => setRecurring("ordinal", Number(o))}
                          />
                        )}
                      </Field>
                    </Show>
                    <Field label={t("recurrence.weekdayLabel")}>
                      {(control) => (
                        <Select
                          control={control}
                          value={recurring.weekday}
                          options={WEEKDAYS}
                          label={(d) => t(`weekday.${d}`)}
                          onChange={(d) => setRecurring("weekday", d)}
                        />
                      )}
                    </Field>
                    <Field label={t("recurrence.intervalLabel")}>
                      {(control) => (
                        <input
                          {...control}
                          type="number"
                          min={1}
                          max={52}
                          value={recurring.interval}
                          onInput={(e) => setRecurring("interval", Number(e.currentTarget.value))}
                        />
                      )}
                    </Field>
                </div>
                </fieldset>
                <div class="field-row">
  <Field
                    label={t("recurrence.startsOnLabel")}
                    required
                    requiredText={t("common.required")}
                  >
                    {(control) => (
                      <input
                        {...control}
                        type="date"
                        value={recurring.startsOn}
                        onInput={(e) => setRecurring("startsOn", e.currentTarget.value)}
                      />
                    )}
                  </Field>
                  <Field label={t("recurrence.endsOnLabel")} optionalText={t("common.optional")}>
                    {(control) => (
                      <input
                        {...control}
                        type="date"
                        value={recurring.endsOn}
                        onInput={(e) => setRecurring("endsOn", e.currentTarget.value)}
                      />
                    )}
                  </Field>
                </div>
                <div class="field-row">
  <Field
                    label={t("recurrence.startTimeLabel")}
                    required
                    requiredText={t("common.required")}
                  >
                    {(control) => (
                      <input
                        {...control}
                        type="time"
                        value={recurring.startTime}
                        onInput={(e) => setRecurring("startTime", e.currentTarget.value)}
                      />
                    )}
                  </Field>
                  <Field
                    label={t("recurrence.durationLabel")}
                    required
                    requiredText={t("common.required")}
                  >
                    {(control) => (
                      <input
                        {...control}
                        type="number"
                        min={5}
                        value={recurring.durationMinutes}
                        onInput={(e) =>
                          setRecurring("durationMinutes", Number(e.currentTarget.value))
                        }
                      />
                    )}
                  </Field>
                </div>
                <Field
                  label={t("event.timezoneLabel")}
                  hint={t("event.timezoneHint")}
                  required
                  requiredText={t("common.required")}
                >
                  {(control) => (
                    <TimezonePicker
                      control={control}
                      value={recurring.timezone}
                      onChange={(tz) => setRecurring("timezone", tz)}
                    />
                  )}
                </Field>
                <div class="check-row">
                  <CheckField
                    label={t("event.onlineLabel")}
                    checked={recurring.isOnline}
                    onChange={(v) => setRecurring("isOnline", v)}
                  />
                  <CheckField
                    label={t("event.inPersonLabel")}
                    checked={recurring.isInPerson}
                    onChange={(v) => setRecurring("isInPerson", v)}
                  />
                </div>
                <Show when={recurring.isInPerson}>
                  <fieldset>
                    <legend>{t("event.locationLegend")}</legend>
                    <Field label={t("event.locationNameLabel")}>
                      {(control) => (
                        <input
                          {...control}
                          type="text"
                          value={recurring.locationName}
                          onInput={(e) => setRecurring("locationName", e.currentTarget.value)}
                        />
                      )}
                    </Field>
                    <Field label={t("event.localityLabel")}>
                      {(control) => (
                        <input
                          {...control}
                          type="text"
                          autocomplete="address-level2"
                          value={recurring.locality}
                          onInput={(e) => setRecurring("locality", e.currentTarget.value)}
                        />
                      )}
                    </Field>
                  </fieldset>
                </Show>
                <div class="form-actions">
                  <button
                    type="submit"
                    disabled={busy() || !recurring.title.trim() || !recurring.startsOn}
                  >
                    {busy() ? t("common.creating") : t("gathering.schedule")}
                  </button>
                </div>
              </form>
              </div>
            </section>
          </Show>

          {/* Ownership moving is an owner's to offer and somebody else's to
              accept; an administrator has a separate, one-sided path. */}
          <Show when={g().viewer.canEdit}>
            <OfferGathering gatheringId={params.id} />
          </Show>
          <Show when={g().viewer.isAdmin}>
            <AdoptGathering gatheringId={params.id} onAdopted={() => void refetchGathering()} />
          </Show>

          {/* Webhooks are an owner's to set: the URL and the failure history
              say where their systems are. */}
          <Show when={g().viewer.canEdit}>
            <Webhooks ownerKind="gathering" ownerId={params.id} />
          </Show>

          {/* Deletion is a DIFFERENT permission from editing. The server
              gives an owner both but gives an administrator only the
              second (perms.go, gatheringViewer), so rendering this under
              canEdit would leave an admin with no way to remove a gathering
              they do not own — which is the one thing the role exists for. */}
          <Show when={g().viewer.canDelete}>
            <section class="danger-zone">
              <p>{t("gathering.dangerNote")}</p>
              <button type="button" class="danger" onClick={remove}>
                {t("common.delete")}
              </button>
            </section>
          </Show>

          <Show when={g().nextEventAt}>
            {(when) => (
              <p class="visually-hidden">
                {t("gathering.nextEvent", { when: dateTime(when()) })}
              </p>
            )}
          </Show>
        </>
      )}
    </Show>
  );
}
