import type {
  CreateEventSeriesRequest,
  EventList,
  EventSeriesList,
  Gathering,
  PublishSetting,
  RecurrenceFreq,
  Weekday,
} from "~/gen/types.gen";
import { dateTime, plural, t } from "~/i18n";
import { api } from "~/lib/api";
import { SESSION, useSession } from "~/lib/session";
import { browserTimezone, instantFromWallClock } from "~/lib/zonedTime";
import { navigate } from "~/app/router";
import { errorAlert, errorAlertFor, statusMessage } from "~/components/Alert";
import { eventCard, seriesCard } from "~/components/Cards";
import {
  button,
  checkField,
  dateTimeField,
  numberField,
  selectField,
  setButtonText,
  setDisabled,
  textArea,
  textField,
  type Field,
  type TextArea,
  type TextField,
} from "~/components/Field";
import { offerGathering, adoptGathering } from "~/components/Offers";
import { originBadge } from "~/components/OriginBadge";
import { publishControl } from "~/components/PublishControl";
import { webhooksPanel } from "~/components/Webhooks";
import { fill, h } from "~/ui/dom";
import { Resource, TinkuElement, define } from "~/ui/element";

const WEEKDAYS: Weekday[] = ["monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"];
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
 * and a text field where it cannot. Both spellings reach the same value, so
 * the caller does not care which one it got.
 */
function timezoneField(value: string, onChange: (timezone: string) => void): Field<HTMLElement> & { set(zone: string): void } {
  const zones = knownTimezones();
  const opts = { label: t("event.timezoneLabel"), hint: t("event.timezoneHint"), required: true };
  if (zones.length > 0) {
    // The organizer's own zone may be missing from an old browser's list;
    // it is still a valid answer, so it is offered.
    const all = zones.includes(value) ? zones : [value, ...zones];
    const field = selectField<string>({
      ...opts,
      value,
      options: all.map((zone) => ({ value: zone, label: zone })),
      onChange,
    });
    return { ...field, set: (zone) => void (field.control.value = zone) };
  }
  const field = textField({ ...opts, value, onInput: onChange });
  return { ...field, set: (zone) => void (field.control.value = zone) };
}

type Single = {
  title: string;
  description: string;
  isOnline: boolean;
  isInPerson: boolean;
  onlineUrl: string;
  startsAt: string;
  endsAt: string;
  timezone: string;
  locationName: string;
  address: string;
  locality: string;
  region: string;
  postalCode: string;
  country: string;
};

type Recurring = {
  title: string;
  description: string;
  isOnline: boolean;
  isInPerson: boolean;
  freq: RecurrenceFreq;
  interval: number | null;
  weekday: Weekday;
  ordinal: number;
  startsOn: string;
  endsOn: string;
  startTime: string;
  durationMinutes: number | null;
  timezone: string;
  locality: string;
  locationName: string;
};

/**
 * One gathering: what it is, who owns it, what is scheduled, and — for an
 * owner — the forms that schedule more.
 *
 * The shell makes a new page for each path, so walking from one gathering
 * to another starts with empty forms and no status message: nothing that
 * was typed for the first gathering can be sent to the second.
 */
export class GatheringDetailPage extends TinkuElement {
  static watches = [SESSION];
  routeId?: string;

  #gathering = new Resource<Gathering>(this, () => api.gathering.getGathering({ id: this.#id }));
  #events = new Resource<EventList>(this, () => api.event.listEvents({ gatheringId: this.#id }));
  #series = new Resource<EventSeriesList>(this, () => api.event.listEventSeries({ gatheringId: this.#id }));

  #alert = errorAlert();
  #status = statusMessage();
  #busy = false;
  // Which of the two schedule forms is on screen. Both are always in the
  // document — `hidden`, not removed — so that what a person has typed into
  // one survives a look at the other.
  #tab: "single" | "series" = "single";

  #single: Single = {
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
  };
  #recurring: Recurring = {
    title: "",
    description: "",
    isOnline: true,
    isInPerson: false,
    freq: "monthly",
    interval: 1,
    weekday: "thursday",
    ordinal: 2,
    startsOn: "",
    endsOn: "",
    startTime: "19:00",
    durationMinutes: 120,
    timezone: browserTimezone(),
    locality: "",
    locationName: "",
  };

  // The parts that update() changes.
  #loading!: HTMLElement;
  #body!: HTMLElement;
  #heading!: HTMLElement;
  #address!: HTMLElement;
  #external!: HTMLElement;
  #meta!: HTMLElement;
  #blurb!: HTMLElement;
  #description!: HTMLElement;
  #publish!: ReturnType<typeof publishControl>;
  #roster!: HTMLElement;
  #actions!: HTMLElement;
  #join!: ReturnType<typeof button>;
  #leave!: ReturnType<typeof button>;
  #eventList!: HTMLElement;
  #seriesSection!: HTMLElement;
  #seriesList!: HTMLElement;
  #schedule!: HTMLElement;
  #tabSingle!: HTMLButtonElement;
  #tabSeries!: HTMLButtonElement;
  #panelSingle!: HTMLElement;
  #panelSeries!: HTMLElement;
  #singlePreview!: HTMLElement;
  #singleUrl!: Field<TextField>;
  #singleLocation!: HTMLElement;
  #singleSubmit!: ReturnType<typeof button>;
  #singleTitle!: Field<TextField>;
  #singleDescription!: Field<TextArea>;
  #singleStarts!: ReturnType<typeof dateTimeField>;
  #singleEnds!: ReturnType<typeof dateTimeField>;
  #recurringTitle!: Field<TextField>;
  #recurringDescription!: Field<TextArea>;
  #recurringStartsOn!: ReturnType<typeof dateTimeField>;
  #ordinal!: HTMLElement;
  #recurringLocation!: HTMLElement;
  #recurringSubmit!: ReturnType<typeof button>;
  #offer!: HTMLElement;
  #adopt!: HTMLElement;
  #webhooks!: HTMLElement;
  #danger!: HTMLElement;
  #nextEvent!: HTMLElement;

  /** What each part shows now, so a part is made again only on a change. */
  #shownGathering: Gathering | undefined;
  #shownEvents: unknown = null;
  #shownSeries: unknown = null;

  get #id(): string {
    return this.routeId ?? "";
  }

  protected build(): void {
    this.#loading = h("p", null, t("common.loading"));

    this.#heading = h("h1");
    this.#address = h("p", { class: "page-address" });
    // An external record that shares a local name is the case worth
    // spelling out, not merely badging.
    this.#external = h("p", { class: "alert alert-status", role: "status", hidden: true });
    this.#meta = h("p", { class: "page-meta" });
    this.#blurb = h("p", { hidden: true });
    this.#description = h("p", { class: "long-form", hidden: true });

    // Publishing is a three-level choice; the server resolves it and sends
    // the answer, so this renders rather than re-derives.
    this.#publish = publishControl((setting) => void this.#setPublish(setting));

    this.#roster = h("ul", { class: "roster" });

    this.#join = button(t("gathering.join"), { onClick: () => void this.#membership(true) });
    this.#leave = button(t("gathering.leave"), { variant: "default", onClick: () => void this.#membership(false) });
    this.#actions = h("p", { class: "page-actions" }, this.#join, this.#leave);

    this.#eventList = h("div");
    this.#seriesList = h("div");
    this.#seriesSection = h("section", { hidden: true }, h("h2", null, t("event.seriesHeading")), this.#seriesList);

    this.#schedule = this.#buildSchedule();

    // Ownership moving is an owner's to offer and somebody else's to
    // accept; an administrator has a separate, one-sided path.
    this.#offer = h("div", { hidden: true }, offerGathering({ gatheringId: this.#id }));
    // An administrator puts a loose gathering under an organization, with
    // no offer.
    this.#adopt = h(
      "div",
      { hidden: true },
      adoptGathering({ gatheringId: this.#id, onAdopted: () => void this.#gathering.load() }),
    );

    // Webhooks are an owner's to set: the URL and the failure history say
    // where their systems are.
    this.#webhooks = h("div", { hidden: true }, webhooksPanel({ ownerKind: "gathering", ownerId: this.#id }));

    // Deletion is a DIFFERENT permission from editing. The server gives an
    // owner both but gives an administrator only the second (perms.go,
    // gatheringViewer), so showing this under canEdit would leave an admin
    // with no way to remove a gathering they do not own — which is the one
    // thing the role exists for.
    this.#danger = h(
      "section",
      { class: "danger-zone", hidden: true },
      h("p", null, t("gathering.dangerNote")),
      button(t("common.delete"), { variant: "danger", onClick: () => void this.#delete() }),
    );

    this.#nextEvent = h("p", { class: "visually-hidden" });

    this.#body = h(
      "div",
      { hidden: true },
      this.#heading,
      this.#address,
      this.#external,
      this.#meta,
      this.#blurb,
      this.#description,
      this.#publish.el,
      h("section", null, h("h2", null, t("gathering.owners")), this.#roster),
      this.#alert.el,
      this.#status.el,
      this.#actions,
      h("section", null, h("h2", null, t("event.upcoming")), this.#eventList),
      this.#seriesSection,
      this.#schedule,
      this.#offer,
      this.#adopt,
      this.#webhooks,
      this.#danger,
      this.#nextEvent,
    );
    this.replaceChildren(this.#loading, this.#body);
    void this.#gathering.load();
    void this.#events.load();
    void this.#series.load();
  }

  // ---- The two schedule forms ----

  #buildSchedule(): HTMLElement {
    const tab = (id: "single" | "series", text: string) => {
      // Native buttons: role="tab" belongs on the focusable element itself,
      // and a push-button's focusable element is inside it.
      const el = h(
        "button",
        {
          type: "button",
          role: "tab",
          id: `schedule-tab-${id}`,
          "aria-controls": `schedule-panel-${id}`,
          onClick: () => {
            this.#tab = id;
            this.invalidate();
          },
        },
        text,
      ) as HTMLButtonElement;
      return el;
    };
    this.#tabSingle = tab("single", t("event.tabSingle"));
    this.#tabSeries = tab("series", t("event.tabSeries"));

    this.#panelSingle = h(
      "div",
      { id: "schedule-panel-single", role: "tabpanel", "aria-labelledby": "schedule-tab-single" },
      this.#buildSingleForm(),
    );
    this.#panelSeries = h(
      "div",
      { id: "schedule-panel-series", role: "tabpanel", "aria-labelledby": "schedule-tab-series", hidden: true },
      this.#buildSeriesForm(),
    );

    // Only an owner can schedule. The forms are absent for everybody else
    // rather than disabled, because a form nobody may submit is a thing to
    // tab through for no reason.
    //
    // One event and a rule that makes many are two shapes of the same task,
    // so they are one section with a switch. Both forms used to be open at
    // once, which made the page twice as long as the job and left the
    // reader to work out which half applied to them.
    return h(
      "section",
      { hidden: true },
      h("h2", null, t("event.scheduleHeading")),
      h("div", { class: "tabs", role: "tablist", "aria-label": t("event.scheduleHeading") }, this.#tabSingle, this.#tabSeries),
      this.#panelSingle,
      this.#panelSeries,
    );
  }

  #buildSingleForm(): HTMLFormElement {
    const s = this.#single;
    const changed = () => this.invalidate();
    const text = (key: keyof Single, label: string, extra: { autocomplete?: string; maxlength?: number } = {}) =>
      textField({
        label,
        ...extra,
        onInput: (v) => {
          (s as Record<string, unknown>)[key] = v;
        },
      });

    this.#singleTitle = textField({
      label: t("event.titleLabel"),
      required: true,
      onInput: (v) => {
        s.title = v;
        changed();
      },
    });
    this.#singleDescription = textArea({
      label: t("event.descriptionLabel"),
      optional: true,
      rows: 4,
      onInput: (v) => {
        s.description = v;
      },
    });
    this.#singleStarts = dateTimeField({
      label: t("event.startsAtLabel"),
      required: true,
      mode: "datetime",
      value: "",
      timeZone: s.timezone,
      onChange: (v) => {
        s.startsAt = v;
        changed();
      },
    });
    this.#singleEnds = dateTimeField({
      label: t("event.endsAtLabel"),
      required: true,
      mode: "datetime",
      value: "",
      timeZone: s.timezone,
      onChange: (v) => {
        s.endsAt = v;
        changed();
      },
    });
    const zone = timezoneField(s.timezone, (tz) => {
      s.timezone = tz;
      this.#singleStarts.setTimeZone(tz);
      this.#singleEnds.setTimeZone(tz);
      changed();
    });

    // The instant the form is about to send, said back in plain words. The
    // zone and the clock reading are two controls, and nothing else on
    // screen shows what they add up to — which is exactly where a two-hour
    // mistake used to hide.
    this.#singlePreview = h("p", { class: "field-hint", role: "status", "aria-live": "polite" });

    this.#singleUrl = textField({
      label: t("event.onlineUrlLabel"),
      optional: true,
      type: "url",
      onInput: (v) => {
        s.onlineUrl = v;
      },
    });

    // An in-person event needs a location, so the fieldset appears exactly
    // when the rule applies.
    this.#singleLocation = h(
      "fieldset",
      { hidden: true },
      h("legend", null, t("event.locationLegend")),
      text("locationName", t("event.locationNameLabel")).el,
      text("address", t("event.addressLabel"), { autocomplete: "street-address" }).el,
      text("locality", t("event.localityLabel"), { autocomplete: "address-level2" }).el,
      text("region", t("event.regionLabel"), { autocomplete: "address-level1" }).el,
      text("postalCode", t("event.postalCodeLabel"), { autocomplete: "postal-code" }).el,
      text("country", t("event.countryLabel"), { autocomplete: "country", maxlength: 2 }).el,
    );

    this.#singleSubmit = button(t("gathering.schedule"), { type: "submit" });

    const form = h(
      "form",
      null,
      this.#singleTitle.el,
      this.#singleDescription.el,
      h("div", { class: "field-row" }, this.#singleStarts.el, this.#singleEnds.el),
      zone.el,
      this.#singlePreview,
      h(
        "div",
        { class: "check-row" },
        checkField({
          label: t("event.onlineLabel"),
          checked: s.isOnline,
          onChange: (v) => {
            s.isOnline = v;
            changed();
          },
        }).el,
        checkField({
          label: t("event.inPersonLabel"),
          checked: s.isInPerson,
          onChange: (v) => {
            s.isInPerson = v;
            changed();
          },
        }).el,
      ),
      this.#singleUrl.el,
      this.#singleLocation,
      h("div", { class: "form-actions" }, this.#singleSubmit),
    );
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      void this.#createEvent();
    });
    return form;
  }

  #buildSeriesForm(): HTMLFormElement {
    const r = this.#recurring;
    const changed = () => this.invalidate();

    this.#recurringTitle = textField({
      label: t("event.titleLabel"),
      required: true,
      onInput: (v) => {
        r.title = v;
        changed();
      },
    });
    this.#recurringDescription = textArea({
      label: t("event.descriptionLabel"),
      optional: true,
      rows: 3,
      onInput: (v) => {
        r.description = v;
      },
    });

    const freq = selectField<RecurrenceFreq>({
      label: t("recurrence.freqLabel"),
      value: r.freq,
      options: FREQUENCIES.map((f) => ({ value: f, label: t(`recurrence.freq.${f}`) })),
      onChange: (f) => {
        r.freq = f;
        changed();
      },
    });
    // An ordinal is meaningless for a weekly rule, so the control is not
    // offered for one.
    this.#ordinal = selectField<string>({
      label: t("recurrence.ordinalLabel"),
      value: String(r.ordinal),
      options: ORDINALS.map((o) => ({ value: String(o), label: t(`recurrence.ordinal.${o}`) })),
      onChange: (o) => {
        r.ordinal = Number(o);
      },
    }).el;
    const weekday = selectField<Weekday>({
      label: t("recurrence.weekdayLabel"),
      value: r.weekday,
      options: WEEKDAYS.map((d) => ({ value: d, label: t(`weekday.${d}`) })),
      onChange: (d) => {
        r.weekday = d;
      },
    });
    const interval = numberField({
      label: t("recurrence.intervalLabel"),
      value: r.interval,
      min: 1,
      max: 52,
      onChange: (v) => {
        r.interval = v;
        changed();
      },
    });

    this.#recurringStartsOn = dateTimeField({
      label: t("recurrence.startsOnLabel"),
      required: true,
      mode: "date",
      value: "",
      timeZone: r.timezone,
      onChange: (v) => {
        r.startsOn = v;
        changed();
      },
    });
    const endsOn = dateTimeField({
      label: t("recurrence.endsOnLabel"),
      optional: true,
      mode: "date",
      value: "",
      timeZone: r.timezone,
      onChange: (v) => {
        r.endsOn = v;
      },
    });
    const startTime = dateTimeField({
      label: t("recurrence.startTimeLabel"),
      required: true,
      mode: "time",
      value: r.startTime,
      onChange: (v) => {
        r.startTime = v;
        changed();
      },
    });
    const duration = numberField({
      label: t("recurrence.durationLabel"),
      required: true,
      value: r.durationMinutes,
      min: 5,
      onChange: (v) => {
        r.durationMinutes = v;
        changed();
      },
    });
    const zone = timezoneField(r.timezone, (tz) => {
      r.timezone = tz;
      this.#recurringStartsOn.setTimeZone(tz);
      endsOn.setTimeZone(tz);
    });

    this.#recurringLocation = h(
      "fieldset",
      { hidden: true },
      h("legend", null, t("event.locationLegend")),
      textField({
        label: t("event.locationNameLabel"),
        onInput: (v) => {
          r.locationName = v;
        },
      }).el,
      textField({
        label: t("event.localityLabel"),
        autocomplete: "address-level2",
        onInput: (v) => {
          r.locality = v;
        },
      }).el,
    );

    this.#recurringSubmit = button(t("gathering.schedule"), { type: "submit" });

    const form = h(
      "form",
      null,
      this.#recurringTitle.el,
      this.#recurringDescription.el,
      h(
        "fieldset",
        null,
        h("legend", null, t("recurrence.freqLabel")),
        h("div", { class: "field-row" }, freq.el, this.#ordinal, weekday.el, interval.el),
      ),
      h("div", { class: "field-row" }, this.#recurringStartsOn.el, endsOn.el),
      h("div", { class: "field-row" }, startTime.el, duration.el),
      zone.el,
      h(
        "div",
        { class: "check-row" },
        checkField({
          label: t("event.onlineLabel"),
          checked: r.isOnline,
          onChange: (v) => {
            r.isOnline = v;
          },
        }).el,
        checkField({
          label: t("event.inPersonLabel"),
          checked: r.isInPerson,
          onChange: (v) => {
            r.isInPerson = v;
            changed();
          },
        }).el,
      ),
      this.#recurringLocation,
      h("div", { class: "form-actions" }, this.#recurringSubmit),
    );
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      void this.#createSeries();
    });
    return form;
  }

  // ---- Actions ----

  async #reloadAll(): Promise<void> {
    await Promise.all([this.#gathering.load(), this.#events.load(), this.#series.load()]);
  }

  async #membership(join: boolean): Promise<void> {
    this.#busy = true;
    this.#alert.set(undefined);
    this.invalidate();
    try {
      const updated = join
        ? await api.gathering.joinGathering({ gatheringId: this.#id })
        : await api.gathering.leaveGathering({ gatheringId: this.#id });
      this.#gathering.value = updated;
      this.#status.set(t(join ? "gathering.joined" : "gathering.left"));
    } catch (err) {
      this.#alert.set(err);
    } finally {
      this.#busy = false;
      if (this.isActive()) this.invalidate();
    }
  }

  async #setPublish(setting: PublishSetting): Promise<void> {
    this.#alert.set(undefined);
    try {
      this.#gathering.value = await api.gathering.updateGathering({ id: this.#id, publishEvents: setting });
    } catch (err) {
      this.#alert.set(err);
    }
    if (this.isActive()) this.invalidate();
  }

  async #delete(): Promise<void> {
    const current = this.#gathering.value;
    if (!current) return;
    if (!window.confirm(t("common.confirmDelete", { name: current.name }))) return;
    try {
      await api.gathering.deleteGathering({ id: this.#id });
      navigate("/gatherings");
    } catch (err) {
      this.#alert.set(err);
    }
  }

  async #createEvent(): Promise<void> {
    const s = this.#single;
    if (this.#busy || !s.title.trim() || !s.startsAt || !s.endsAt) return;
    this.#busy = true;
    this.#alert.set(undefined);
    this.invalidate();
    try {
      await api.event.createEvent({
        gatheringId: this.#id,
        title: s.title,
        description: s.description,
        isOnline: s.isOnline,
        isInPerson: s.isInPerson,
        onlineUrl: s.isOnline && s.onlineUrl ? s.onlineUrl : undefined,
        location: s.isInPerson
          ? {
              name: s.locationName,
              address: s.address,
              locality: s.locality,
              region: s.region,
              postalCode: s.postalCode,
              country: s.country.toUpperCase(),
            }
          : undefined,
        // Read in the zone the organizer chose, not the browser's — see
        // lib/zonedTime.ts for what goes wrong otherwise.
        startsAt: instantFromWallClock(s.startsAt, s.timezone),
        endsAt: instantFromWallClock(s.endsAt, s.timezone),
        timezone: s.timezone,
      });
      s.title = s.description = s.startsAt = s.endsAt = "";
      this.#singleTitle.control.value = "";
      this.#singleDescription.control.value = "";
      this.#singleStarts.control.value = "";
      this.#singleEnds.control.value = "";
      if (this.isActive()) await this.#reloadAll();
    } catch (err) {
      this.#alert.set(err);
    } finally {
      this.#busy = false;
      if (this.isActive()) this.invalidate();
    }
  }

  async #createSeries(): Promise<void> {
    const r = this.#recurring;
    if (this.#busy || !r.title.trim() || !r.startsOn || r.interval === null || r.durationMinutes === null) return;
    this.#busy = true;
    this.#alert.set(undefined);
    this.invalidate();
    try {
      const req: CreateEventSeriesRequest = {
        gatheringId: this.#id,
        title: r.title,
        description: r.description,
        isOnline: r.isOnline,
        isInPerson: r.isInPerson,
        recurrence: {
          freq: r.freq,
          interval: r.interval,
          weekday: r.weekday,
          // A weekly rule has exactly one of each weekday per week, so an
          // ordinal would mean nothing there and is left off.
          ordinal: r.freq === "weekly" ? undefined : r.ordinal,
        },
        startsOn: instantFromWallClock(`${r.startsOn}T00:00`, r.timezone),
        endsOn: r.endsOn ? instantFromWallClock(`${r.endsOn}T23:59`, r.timezone) : undefined,
        startTime: r.startTime,
        durationMinutes: r.durationMinutes,
        timezone: r.timezone,
      };
      if (r.isInPerson) {
        req.location = { name: r.locationName, address: "", locality: r.locality, region: "", postalCode: "", country: "" };
      }
      await api.event.createEventSeries(req);
      r.title = r.description = r.startsOn = "";
      this.#recurringTitle.control.value = "";
      this.#recurringDescription.control.value = "";
      this.#recurringStartsOn.control.value = "";
      if (this.isActive()) await this.#reloadAll();
    } catch (err) {
      this.#alert.set(err);
    } finally {
      this.#busy = false;
      if (this.isActive()) this.invalidate();
    }
  }

  // ---- Rendering ----

  #singlePreviewText(): string {
    const s = this.#single;
    // Empty until both are filled, so it never shows a half-formed answer.
    if (!s.startsAt || !s.timezone) return "";
    try {
      const instant = instantFromWallClock(s.startsAt, s.timezone);
      return t("event.resolvesTo", { utc: instant.toISOString().replace(".000Z", "Z"), local: dateTime(instant) });
    } catch {
      return "";
    }
  }

  #showGathering(g: Gathering): void {
    if (this.#heading.textContent !== g.name) this.#heading.textContent = g.name;
    fill(this.#address, originBadge(g.origin, g.slug));
    this.#external.hidden = !g.origin.isExternal;
    if (g.origin.isExternal) this.#external.textContent = t("origin.externalWarning", { domain: g.origin.domain });
    fill(
      this.#meta,
      h("span", null, plural("gathering.members", g.memberCount)),
      h("span", null, plural("gathering.events", g.eventCount)),
    );
    this.#blurb.hidden = !g.blurb;
    this.#blurb.textContent = g.blurb;
    this.#description.hidden = !g.description;
    this.#description.textContent = g.description;

    this.#publish.el.hidden = !(g.viewer.canEdit || g.publish.publishing);
    this.#publish.update({ value: g.publishEvents, decision: g.publish, canEdit: g.viewer.canEdit });

    fill(
      this.#roster,
      g.owners.map((owner) =>
        h(
          "li",
          null,
          h("span", { class: "roster-name" }, owner.displayName),
          " ",
          h("span", { class: "card-address" }, `${owner.handle}@${owner.originDomain}`),
          " ",
          h("span", { class: "badge" }, owner.kind === "organization" ? t("organization.singular") : t("roles.owner")),
        ),
      ),
    );

    this.#schedule.hidden = !g.viewer.canEdit;
    this.#offer.hidden = !g.viewer.canEdit;
    this.#adopt.hidden = !g.viewer.isAdmin;
    this.#webhooks.hidden = !g.viewer.canEdit;
    this.#danger.hidden = !g.viewer.canDelete;

    this.#nextEvent.hidden = !g.nextEventAt;
    this.#nextEvent.textContent = g.nextEventAt ? t("gathering.nextEvent", { when: dateTime(g.nextEventAt) }) : "";
  }

  protected update(): void {
    const g = this.#gathering.value;
    if (!g) {
      this.#body.hidden = true;
      this.#loading.hidden = false;
      if (this.#gathering.error) fill(this.#loading, errorAlertFor(this.#gathering.error));
      return;
    }
    this.#loading.hidden = true;
    this.#body.hidden = false;
    if (g !== this.#shownGathering) {
      this.#shownGathering = g;
      this.#showGathering(g);
    }

    // Join and leave: only for a signed-in person, one of the two.
    this.#actions.hidden = !useSession().user();
    this.#join.hidden = g.viewer.hasJoined;
    this.#leave.hidden = !g.viewer.hasJoined;
    setDisabled(this.#join, this.#busy);
    setDisabled(this.#leave, this.#busy);
    setButtonText(this.#join, this.#busy ? t("gathering.joining") : t("gathering.join"));
    setButtonText(this.#leave, this.#busy ? t("gathering.leaving") : t("gathering.leave"));

    // Events.
    const events = this.#events.value;
    const eventsShows = events ?? this.#events.error ?? "loading";
    if (eventsShows !== this.#shownEvents) {
      this.#shownEvents = eventsShows;
      if (events) {
        if (events.events.length === 0) fill(this.#eventList, h("p", null, t("event.emptyList")));
        else
          fill(
            this.#eventList,
            h("ul", { class: "cards" }, events.events.map(eventCard)),
            h("p", { class: "card-meta" }, t("common.showing", { shown: events.events.length, total: events.total })),
          );
      } else if (this.#events.error) fill(this.#eventList, errorAlertFor(this.#events.error));
      else fill(this.#eventList, h("p", null, t("common.loading")));
    }

    // Series: the section is there only when there is one.
    const series = this.#series.value;
    if (series !== this.#shownSeries) {
      this.#shownSeries = series;
      const items = series?.series ?? [];
      this.#seriesSection.hidden = items.length === 0;
      fill(this.#seriesList, items.length ? h("ul", { class: "cards" }, items.map(seriesCard)) : null);
    }

    // The schedule forms.
    const single = this.#tab === "single";
    this.#tabSingle.setAttribute("aria-selected", String(single));
    this.#tabSeries.setAttribute("aria-selected", String(!single));
    this.#panelSingle.hidden = !single;
    this.#panelSeries.hidden = single;

    const s = this.#single;
    const preview = this.#singlePreviewText();
    if (this.#singlePreview.textContent !== preview) this.#singlePreview.textContent = preview;
    this.#singleUrl.el.hidden = !s.isOnline;
    this.#singleLocation.hidden = !s.isInPerson;
    setDisabled(this.#singleSubmit, this.#busy || !s.title.trim() || !s.startsAt || !s.endsAt);
    setButtonText(this.#singleSubmit, this.#busy ? t("common.creating") : t("gathering.schedule"));

    const r = this.#recurring;
    this.#ordinal.hidden = r.freq === "weekly";
    this.#recurringLocation.hidden = !r.isInPerson;
    setDisabled(
      this.#recurringSubmit,
      this.#busy || !r.title.trim() || !r.startsOn || r.interval === null || r.durationMinutes === null,
    );
    setButtonText(this.#recurringSubmit, this.#busy ? t("common.creating") : t("gathering.schedule"));
  }
}
define("tinku-gathering-page", GatheringDetailPage);
