import type { Event, EventList, ListEventsRequest, Organization, SearchRequest, SearchResults } from "~/gen/types.gen";
import { plural, t } from "~/i18n";
import { api } from "~/lib/api";
import { SESSION, useSession } from "~/lib/session";
import { monthWindow } from "~/lib/month";
import { errorAlert } from "~/components/Alert";
import { calendar } from "~/components/Calendar";
import { eventCard, gatheringCard, organizationCard, remoteEventCard, seriesCard } from "~/components/Cards";
import { button, checkField, dateTimeField, setButtonText, setDisabled, textField } from "~/components/Field";
import { organizationPicker } from "~/components/OrganizationPicker";
import { fill, h } from "~/ui/dom";
import { Resource, TinkuElement, define } from "~/ui/element";

const NO_EVENTS: readonly Event[] = [];

type View = "search" | "calendar";

/**
 * One search across every kind. The form is a single <form> with three
 * fieldsets — text, place, time — so a keyboard user can tab through it in
 * the order the questions are asked, and a screen reader announces which
 * organization each control belongs to.
 */
export class DiscoverPage extends TinkuElement {
  static watches = [SESSION];
  routeId?: string;

  // Two ways to look at the same directory: a search that answers a
  // question, and a calendar that answers "what is on". The tab is the
  // whole difference — neither view is a filter of the other, and putting
  // both on screen at once made the page twice as long as either job.
  #view: View = "search";

  #month = { year: new Date().getFullYear(), month: new Date().getMonth() };
  #mineOnly = false;
  #organization: Organization | undefined;

  // The calendar asks the server for one window at a time. Everything it
  // keys on is in the key, so changing a toggle loads again rather than
  // filtering a stale answer in the browser.
  #calendarEvents = new Resource<EventList>(this, () => {
    const { from, to } = monthWindow(this.#month.year, this.#month.month);
    const req: ListEventsRequest = {
      startsAfter: from,
      startsBefore: to,
      // A calendar shows the past: the month you are looking at is often
      // one that has already happened, and a grid with holes in it where
      // last week was is not a calendar.
      includeStarted: true,
      page: { limit: 250, offset: 0 },
    };
    if (this.#mineOnly && useSession().user()) req.mine = true;
    if (this.#organization) req.ownedByOrganization = this.#organization.id;
    return api.event.listEvents(req);
  });
  /** The key of the last calendar load. */
  #calendarKey = "";

  #form = {
    query: "",
    locality: "",
    region: "",
    from: "",
    to: "",
    onlineOnly: false,
    inPersonOnly: false,
    includeStarted: false,
  };
  #results: SearchResults | undefined;
  #shownResults: SearchResults | undefined | null = null;
  #busy = false;
  #alert = errorAlert();

  #tabs!: Record<View, HTMLButtonElement>;
  #panels!: Record<View, HTMLElement>;
  #calendar!: ReturnType<typeof calendar>;
  #mineField!: ReturnType<typeof checkField>;
  #submit!: ReturnType<typeof button>;
  #announce!: HTMLElement;
  #resultList!: HTMLElement;

  #totalFound(): number {
    const r = this.#results;
    if (!r) return 0;
    return r.organizations.length + r.gatherings.length + r.events.length + r.series.length + r.remoteEvents.length;
  }

  protected build(): void {
    const tab = (view: View, text: string) => {
      // A native button: role="tab" and aria-selected must be on the
      // element that has the focus.
      const el = h(
        "button",
        {
          type: "button",
          role: "tab",
          id: `discover-tab-${view}`,
          "aria-controls": `discover-panel-${view}`,
        },
        text,
      );
      el.addEventListener("click", () => {
        this.#view = view;
        this.invalidate();
      });
      return el;
    };
    this.#tabs = { search: tab("search", t("discover.viewSearch")), calendar: tab("calendar", t("discover.viewCalendar")) };

    // "Mine" needs a session to mean anything, so it is offered only to
    // somebody who has one.
    this.#mineField = checkField({
      label: t("discover.mineOnly"),
      checked: false,
      onChange: (v) => {
        this.#mineOnly = v;
        this.invalidate();
      },
    });
    const picker = organizationPicker({
      label: t("discover.byOrganization"),
      placeholder: t("discover.byOrganizationPlaceholder"),
      onChange: (organization) => {
        this.#organization = organization;
        this.invalidate();
      },
    });
    this.#calendar = calendar({
      onMove: (year, month) => {
        this.#month = { year, month };
        this.invalidate();
      },
      controls: [this.#mineField.el, picker.el],
    });

    const f = this.#form;
    const query = textField({
      label: t("discover.searchLabel"),
      type: "search",
      placeholder: t("discover.searchPlaceholder"),
      onInput: (v) => (f.query = v),
    });
    const locality = textField({
      label: t("discover.localityLabel"),
      autocomplete: "address-level2",
      onInput: (v) => (f.locality = v),
    });
    const region = textField({
      label: t("discover.regionLabel"),
      autocomplete: "address-level1",
      onInput: (v) => (f.region = v),
    });
    const from = dateTimeField({ label: t("discover.fromLabel"), mode: "date", value: "", onChange: (v) => (f.from = v) });
    const to = dateTimeField({ label: t("discover.toLabel"), mode: "date", value: "", onChange: (v) => (f.to = v) });
    const online = checkField({ label: t("discover.onlineOnly"), checked: false, onChange: (v) => (f.onlineOnly = v) });
    const inPerson = checkField({ label: t("discover.inPersonOnly"), checked: false, onChange: (v) => (f.inPersonOnly = v) });
    const started = checkField({
      label: t("discover.includeStarted"),
      checked: false,
      onChange: (v) => (f.includeStarted = v),
    });
    this.#submit = button(t("discover.searchAction"), { type: "submit" });

    const form = h(
      "form",
      null,
      query.el,
      h(
        "fieldset",
        null,
        h("legend", null, t("discover.placeLegend")),
        h("div", { class: "field-row" }, locality.el, region.el),
      ),
      h(
        "fieldset",
        null,
        h("legend", null, t("discover.timeLegend")),
        h("div", { class: "field-row" }, from.el, to.el),
        h("div", { class: "check-row" }, online.el, inPerson.el, started.el),
      ),
      h("div", { class: "form-actions" }, this.#submit),
    );
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      void this.#search();
    });

    // The result count is announced rather than only shown: a search that
    // replaces the page below the form is a change a sighted user sees at a
    // glance and a screen-reader user is told nothing about.
    this.#announce = h("p", { class: "visually-hidden", role: "status", "aria-live": "polite" });
    this.#resultList = h("div");

    this.#panels = {
      calendar: h(
        "div",
        { id: "discover-panel-calendar", role: "tabpanel", "aria-labelledby": "discover-tab-calendar" },
        this.#calendar.el,
      ),
      search: h(
        "div",
        { id: "discover-panel-search", role: "tabpanel", "aria-labelledby": "discover-tab-search" },
        form,
        this.#alert.el,
        h("section", { "aria-label": t("discover.resultsLabel") }, this.#announce, this.#resultList),
      ),
    };

    this.replaceChildren(
      h("h1", null, t("discover.title")),
      h(
        "div",
        { class: "tabs", role: "tablist", "aria-label": t("discover.viewLabel") },
        this.#tabs.search,
        this.#tabs.calendar,
      ),
      this.#panels.calendar,
      this.#panels.search,
    );
  }

  async #search(): Promise<void> {
    if (this.#busy) return;
    const f = this.#form;
    this.#busy = true;
    this.#alert.set(undefined);
    this.invalidate();
    try {
      const req: SearchRequest = { includeStarted: f.includeStarted };
      if (f.query.trim()) req.query = f.query.trim();
      if (f.locality.trim() || f.region.trim()) {
        req.location = {};
        if (f.locality.trim()) req.location.locality = f.locality.trim();
        if (f.region.trim()) req.location.region = f.region.trim();
      }
      // A date field gives a local calendar date. Reading it as the start
      // of that day, and the "to" bound as the end of it, is what a person
      // means by "between the 1st and the 3rd".
      if (f.from) req.startsAfter = new Date(`${f.from}T00:00`);
      if (f.to) req.startsBefore = new Date(`${f.to}T23:59`);
      if (f.onlineOnly) req.onlineOnly = true;
      if (f.inPersonOnly) req.inPersonOnly = true;

      const results = await api.search.search(req);
      if (!this.isActive()) return;
      this.#results = results;
    } catch (err) {
      if (!this.isActive()) return;
      this.#alert.set(err);
      this.#results = undefined;
    } finally {
      this.#busy = false;
      this.invalidate();
    }
  }

  protected update(): void {
    const user = useSession().user();
    for (const view of ["search", "calendar"] as const) {
      this.#tabs[view].setAttribute("aria-selected", String(this.#view === view));
      this.#panels[view].hidden = this.#view !== view;
    }
    this.#mineField.el.hidden = !user;

    if (this.#view === "calendar") {
      // The signed-in person is part of the key: "mine" means nothing until
      // there is a "me", and signing in has to load again rather than leave
      // somebody else's answer on screen.
      const key = [
        this.#month.year,
        this.#month.month,
        this.#mineOnly && !!user,
        this.#organization?.id ?? "",
        user?.id ?? "",
      ].join("|");
      if (key !== this.#calendarKey) {
        this.#calendarKey = key;
        void this.#calendarEvents.load();
      }
      this.#calendar.update({
        events: this.#calendarEvents.value?.events ?? NO_EVENTS,
        year: this.#month.year,
        month: this.#month.month,
        loading: this.#calendarEvents.loading,
      });
    }

    setDisabled(this.#submit, this.#busy);
    setButtonText(this.#submit, this.#busy ? t("common.loading") : t("discover.searchAction"));

    if (this.#results === this.#shownResults) return;
    this.#shownResults = this.#results;
    const found = this.#results;
    const announced = found ? plural("discover.resultsAnnounced", this.#totalFound()) : "";
    if (this.#announce.textContent !== announced) this.#announce.textContent = announced;
    if (!found) {
      fill(this.#resultList);
      return;
    }
    if (this.#totalFound() === 0) {
      fill(this.#resultList, h("p", null, t("discover.noResults")));
      return;
    }
    const group = <T>(items: readonly T[], heading: string, card: (item: T) => HTMLElement) =>
      items.length > 0 ? [h("h2", null, heading), h("ul", { class: "cards" }, items.map(card))] : null;
    fill(
      this.#resultList,
      group(found.gatherings, t("gathering.plural"), gatheringCard),
      group(found.events, t("event.plural"), eventCard),
      group(found.remoteEvents, t("federation.remoteEvents"), remoteEventCard),
      group(found.series, t("event.newSeries"), seriesCard),
      group(found.organizations, t("organization.plural"), organizationCard),
    );
  }
}
define("tinku-discover-page", DiscoverPage);
