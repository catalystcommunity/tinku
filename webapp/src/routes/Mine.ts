import type { Event, EventList, GatheringList, OrganizationList } from "~/gen/types.gen";
import { t } from "~/i18n";
import { api } from "~/lib/api";
import { SESSION, useSession } from "~/lib/session";
import { monthWindow } from "~/lib/month";
import { calendar } from "~/components/Calendar";
import { cardList, eventCard, gatheringCard, organizationCard } from "~/components/Cards";
import { fill, h } from "~/ui/dom";
import { Resource, TinkuElement, define } from "~/ui/element";

const NO_EVENTS: readonly Event[] = [];

/** Everything that is the caller's: what they own or joined, and what they
 *  said they are attending. */
export class MinePage extends TinkuElement {
  static watches = [SESSION];
  routeId?: string;

  #gatherings = new Resource<GatheringList>(this, () => api.gathering.listGatherings({ mine: true }));
  #organizations = new Resource<OrganizationList>(this, () => api.organization.listOrganizations({ mine: true }));
  #attending = new Resource<EventList>(this, () => api.event.listEvents({ attendingOnly: true }));

  // The calendar here has no toggles at all. This page IS the filter — it
  // is what is yours — so a control offering to widen it would answer a
  // question the reader did not come here to ask. Discover is where the
  // whole directory is.
  #month = { year: new Date().getFullYear(), month: new Date().getMonth() };
  #calendarEvents = new Resource<EventList>(this, () => {
    const { from, to } = monthWindow(this.#month.year, this.#month.month);
    return api.event.listEvents({
      attendingOnly: true,
      startsAfter: from,
      startsBefore: to,
      // A month that has been and gone is still a month somebody wants to
      // look at.
      includeStarted: true,
      page: { limit: 250, offset: 0 },
    });
  });

  /** The user the loads are for. Each load keys on the signed-in user, so
   *  signing in or out loads again rather than leaving somebody else's page
   *  on screen. */
  #loadedFor: string | null | undefined = undefined;

  #signInFirst!: HTMLElement;
  #content!: HTMLElement;
  #calendar!: ReturnType<typeof calendar>;
  #gatheringList!: HTMLElement;
  #attendingList!: HTMLElement;
  #organizationList!: HTMLElement;
  #shows = new Map<HTMLElement, unknown>();

  protected build(): void {
    this.#calendar = calendar({
      onMove: (year, month) => {
        this.#month = { year, month };
        void this.#calendarEvents.load();
      },
    });
    this.#gatheringList = h("div");
    this.#attendingList = h("div");
    this.#organizationList = h("div");
    this.#signInFirst = h("p", null, t("mine.signInFirst"));
    this.#content = h(
      "div",
      null,
      h("section", null, h("h2", null, t("mine.calendar")), h("p", { class: "card-meta" }, t("mine.calendarNote")), this.#calendar.el),
      h("section", null, h("h2", null, t("gathering.mine")), this.#gatheringList),
      h("section", null, h("h2", null, t("event.mine")), this.#attendingList),
      h("section", null, h("h2", null, t("organization.mine")), this.#organizationList),
    );
    this.replaceChildren(h("h1", null, t("mine.title")), this.#signInFirst, this.#content);
  }

  /** Fill `target` only when what it shows changed. */
  #show(target: HTMLElement, data: unknown, make: () => HTMLElement): void {
    if (this.#shows.get(target) === data) return;
    this.#shows.set(target, data);
    fill(target, make());
  }

  protected update(): void {
    const user = useSession().user();
    this.#signInFirst.hidden = !!user;
    this.#content.hidden = !user;
    if (!user) {
      this.#loadedFor = null;
      return;
    }

    if (this.#loadedFor !== user.id) {
      this.#loadedFor = user.id;
      void this.#gatherings.load();
      void this.#organizations.load();
      void this.#attending.load();
      void this.#calendarEvents.load();
    }

    this.#calendar.update({
      events: this.#calendarEvents.value?.events ?? NO_EVENTS,
      year: this.#month.year,
      month: this.#month.month,
      loading: this.#calendarEvents.loading,
    });

    const nothing = t("common.nothingHere");
    const gatherings = this.#gatherings.value;
    this.#show(this.#gatheringList, gatherings, () => cardList(gatherings?.gatherings, gatheringCard, nothing));
    const attending = this.#attending.value;
    this.#show(this.#attendingList, attending, () => cardList(attending?.events, eventCard, nothing));
    const organizations = this.#organizations.value;
    this.#show(this.#organizationList, organizations, () =>
      cardList(organizations?.organizations, organizationCard, nothing),
    );
  }
}
define("tinku-mine-page", MinePage);
