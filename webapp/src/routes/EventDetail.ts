import type { AttendeeList, Event, EventRoleList } from "~/gen/types.gen";
import { dateTime, plural, t } from "~/i18n";
import { api } from "~/lib/api";
import { navigate } from "~/app/router";
import { safeUrl } from "~/lib/safeUrl";
import { SESSION, useSession } from "~/lib/session";
import { errorAlert, errorAlertFor } from "~/components/Alert";
import { button, selectField, setDisabled, textField } from "~/components/Field";
import { fill, h } from "~/ui/dom";
import { Resource, TinkuElement, define } from "~/ui/element";

type EventRole = "organizer" | "presenter";

/**
 * One event.
 *
 * The screen's shape changes once the event has started: the description is
 * gone (the server did not send it), attendance is closed, and the edit and
 * delete controls disappear. The reason is stated rather than left for the
 * reader to infer from missing controls — a screen that quietly loses half
 * its buttons reads as broken.
 */
export class EventDetailPage extends TinkuElement {
  static watches = [SESSION];
  routeId?: string;

  #event = new Resource<Event>(this, () => api.event.getEvent({ id: this.#id }));
  #attendees = new Resource<AttendeeList>(this, () => api.event.listAttendees({ eventId: this.#id }));
  #roles = new Resource<EventRoleList>(this, () => api.event.listEventRoles({ eventId: this.#id }));

  #busy = false;
  #address = "";
  #role: EventRole = "presenter";
  #alert = errorAlert();

  #loading!: HTMLElement;
  #content!: HTMLElement;
  #heading!: HTMLElement;
  #when!: HTMLElement;
  #lock!: HTMLElement;
  #description!: HTMLElement;
  #meta!: HTMLElement;
  #online!: HTMLElement;
  #location!: HTMLElement;
  #back!: HTMLAnchorElement;
  #count!: HTMLElement;
  #attendance!: HTMLElement;
  #attendeeList!: HTMLElement;
  #roleList!: HTMLElement;
  #roleForm!: HTMLFormElement;
  #addressField!: ReturnType<typeof textField>;
  #roleSubmit!: ReturnType<typeof button>;
  #danger!: HTMLElement;

  #eventShows: unknown = null;
  #attendanceShows = "";
  #attendeesShows: unknown = null;
  #rolesShows: unknown = null;

  get #id(): string {
    return this.routeId ?? "";
  }

  protected build(): void {
    this.#loading = h("p", null, t("common.loading"));
    this.#heading = h("h1");
    this.#when = h("p", { class: "page-when" });
    // The lock is announced, not merely implied by what is missing.
    this.#lock = h(
      "p",
      { class: "alert alert-status", role: "status", hidden: true },
      h("strong", null, t("event.started")),
      " ",
      t("event.startedExplanation"),
    );
    this.#description = h("div");
    this.#meta = h("p", { class: "page-meta" });
    this.#online = h("div");
    this.#location = h("div");
    this.#back = h("a", null, t("event.backToGathering"));

    this.#count = h("p", { class: "card-meta", role: "status", "aria-live": "polite" });
    this.#attendance = h("div");
    this.#attendeeList = h("div");

    this.#roleList = h("div");
    this.#addressField = textField({
      label: t("organization.addMemberAddress"),
      hint: t("organization.addMemberAddressHint"),
      required: true,
      onInput: (v) => {
        this.#address = v;
        this.invalidate();
      },
    });
    const roleSelect = selectField<EventRole>({
      label: t("roles.legend"),
      value: this.#role,
      options: [
        { value: "presenter", label: t("roles.presenter") },
        { value: "organizer", label: t("roles.organizer") },
      ],
      onChange: (v) => {
        this.#role = v;
      },
    });
    this.#roleSubmit = button(t("organization.addMemberAction"), { type: "submit" });
    this.#roleForm = h(
      "form",
      { hidden: true },
      h("h3", null, t("roles.assign")),
      h("div", { class: "field-row" }, this.#addressField.el, roleSelect.el),
      h("div", { class: "form-actions" }, this.#roleSubmit),
    );
    this.#roleForm.addEventListener("submit", (e) => {
      e.preventDefault();
      void this.#assignRole();
    });

    this.#danger = h(
      "section",
      { class: "danger-zone", hidden: true },
      h("p", null, t("event.dangerNote")),
      button(t("common.delete"), { variant: "danger", onClick: () => void this.#delete() }),
    );

    this.#content = h(
      "div",
      { hidden: true },
      this.#heading,
      this.#when,
      this.#lock,
      this.#description,
      this.#meta,
      this.#online,
      this.#location,
      h("p", { class: "page-actions" }, this.#back),
      this.#alert.el,
      h("section", null, h("h2", null, t("event.attendeeList")), this.#count, this.#attendance, this.#attendeeList),
      h(
        "section",
        null,
        h("h2", null, t("roles.legend")),
        h("p", { class: "card-meta" }, t("roles.presenterNote")),
        this.#roleList,
        this.#roleForm,
      ),
      this.#danger,
    );
    this.replaceChildren(this.#loading, this.#content);
    void this.#event.load();
    void this.#attendees.load();
    void this.#roles.load();
  }

  async #run(fn: () => Promise<void>): Promise<void> {
    this.#busy = true;
    this.#alert.set(undefined);
    this.#attendanceShows = "";
    this.invalidate();
    try {
      await fn();
    } catch (err) {
      this.#alert.set(err);
    } finally {
      this.#busy = false;
      this.#attendanceShows = "";
      this.invalidate();
    }
  }

  #setAttendance(attending: boolean): void {
    void this.#run(async () => {
      const updated = attending
        ? await api.event.attendEvent({ eventId: this.#id })
        : await api.event.unattendEvent({ eventId: this.#id });
      this.#event.value = updated;
      await this.#attendees.load();
    });
  }

  async #assignRole(): Promise<void> {
    if (this.#busy || !this.#address.includes("@")) return;
    await this.#run(async () => {
      const [handle, domain] = this.#address.split("@");
      const found = await api.admin.findUser({ handle: handle ?? "", domain: domain ?? "" });
      await api.event.setEventRole({ eventId: this.#id, userId: found.userId, role: this.#role });
      this.#address = "";
      this.#addressField.control.value = "";
      await this.#roles.load();
    });
  }

  async #delete(): Promise<void> {
    const current = this.#event.value;
    if (!current) return;
    if (!window.confirm(t("common.confirmDelete", { name: current.title }))) return;
    this.#alert.set(undefined);
    try {
      await api.event.deleteEvent({ id: this.#id });
      // Navigate AWAY rather than load again: the id no longer exists.
      navigate(`/gatherings/${current.gatheringId}`);
    } catch (err) {
      this.#alert.set(err);
    }
  }

  protected update(): void {
    const e = this.#event.value;
    if (!e) {
      this.#content.hidden = true;
      this.#loading.hidden = false;
      fill(this.#loading, this.#event.error ? errorAlertFor(this.#event.error) : t("common.loading"));
      return;
    }
    this.#loading.hidden = true;
    this.#content.hidden = false;

    if (e !== this.#eventShows) {
      this.#eventShows = e;
      this.#showEvent(e);
    }

    // Attendance: the person, the membership, and the lock decide it.
    const user = useSession().user();
    const attendanceKey = `${!!user}|${e.locked}|${e.viewer.isMember}|${e.viewerAttending}|${this.#busy}`;
    if (attendanceKey !== this.#attendanceShows) {
      this.#attendanceShows = attendanceKey;
      if (!user || e.locked) fill(this.#attendance);
      else if (!e.viewer.isMember) fill(this.#attendance, h("p", { class: "card-meta" }, t("event.joinToAttend")));
      else
        fill(
          this.#attendance,
          e.viewerAttending
            ? button(t("event.unattend"), { disabled: this.#busy, onClick: () => this.#setAttendance(false) })
            : button(t("event.attend"), { disabled: this.#busy, onClick: () => this.#setAttendance(true) }),
        );
    }

    const attendees = this.#attendees.value;
    if (attendees !== this.#attendeesShows) {
      this.#attendeesShows = attendees;
      fill(
        this.#attendeeList,
        attendees &&
          h(
            "ul",
            { class: "roster" },
            attendees.attendees.map((a) =>
              h(
                "li",
                null,
                h("span", { class: "roster-name" }, a.displayName || a.handle),
                " ",
                h("span", { class: "card-address" }, `${a.handle}@${a.linkkeysDomain}`),
              ),
            ),
          ),
      );
    }

    const roles = this.#roles.value;
    if (roles !== this.#rolesShows) {
      this.#rolesShows = roles;
      fill(
        this.#roleList,
        roles &&
          h(
            "ul",
            { class: "roster" },
            roles.roles.map((r) =>
              h(
                "li",
                null,
                h("span", { class: "roster-name" }, r.displayName || r.handle),
                " ",
                h("span", { class: "badge" }, r.role === "organizer" ? t("roles.organizer") : t("roles.presenter")),
              ),
            ),
          ),
      );
    }

    this.#roleForm.hidden = !e.viewer.canManageMembers;
    setDisabled(this.#roleSubmit, this.#busy || !this.#address.includes("@"));
    this.#danger.hidden = !e.viewer.canDelete;
  }

  #showEvent(e: Event): void {
    this.#heading.textContent = e.title;
    // The event's own timezone, whoever reads it.
    fill(
      this.#when,
      h("time", { datetime: new Date(e.startsAt).toISOString() }, dateTime(e.startsAt, e.timezone)),
      " – ",
      h("time", { datetime: new Date(e.endsAt).toISOString() }, dateTime(e.endsAt, e.timezone)),
      " ",
      h("span", { class: "card-tz" }, `(${e.timezone})`),
    );
    this.#lock.hidden = !e.locked;
    fill(
      this.#description,
      e.description
        ? h("p", { class: "long-form" }, e.description)
        : e.locked
          ? h("p", { class: "card-meta" }, t("event.descriptionWithheld"))
          : null,
    );
    fill(
      this.#meta,
      e.isOnline && h("span", { class: "badge" }, t("event.online")),
      e.isInPerson && h("span", { class: "badge" }, t("event.inPerson")),
      e.seriesId && h("span", { class: "badge" }, t("event.partOfSeries")),
    );
    // Whoever runs the gathering typed this URL. It is rendered as a link
    // only if it is one this app will vouch for — a browser runs
    // `javascript:` out of an href on THIS origin, session cookie and all.
    const url = safeUrl(e.onlineUrl);
    fill(this.#online, url && h("p", null, h("a", { href: url, rel: "noreferrer noopener" }, t("event.onlineUrlLabel"))));
    const loc = e.location;
    const br = () => h("br");
    fill(
      this.#location,
      loc &&
        h(
          "address",
          null,
          loc.name && [h("span", null, loc.name), br()],
          loc.address && [h("span", null, loc.address), br()],
          h("span", null, [loc.locality, loc.region, loc.postalCode].filter(Boolean).join(", ")),
          loc.country && [br(), h("span", null, loc.country)],
        ),
    );
    this.#back.href = `/gatherings/${e.gatheringId}`;
    const count = plural("event.attendees", e.attendeeCount);
    if (this.#count.textContent !== count) this.#count.textContent = count;
  }
}

define("tinku-event-page", EventDetailPage);
