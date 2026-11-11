import type { Organization, OrganizationMemberList } from "~/gen/types.gen";
import { date, plural, t } from "~/i18n";
import { api } from "~/lib/api";
import { navigate } from "~/app/router";
import { errorAlert, errorAlertFor, statusMessage } from "~/components/Alert";
import { button, selectField, setDisabled, textField } from "~/components/Field";
import { offerInbox } from "~/components/Offers";
import { originBadge } from "~/components/OriginBadge";
import { publishControl } from "~/components/PublishControl";
import { webhooksPanel } from "~/components/Webhooks";
import { fill, h } from "~/ui/dom";
import { Resource, TinkuElement, define } from "~/ui/element";

type MemberRole = "owner" | "member";

/** One organization: its blurb, its roster, and the roster controls its owners get. */
export class OrganizationDetailPage extends TinkuElement {
  routeId?: string;

  #organization = new Resource<Organization>(this, () => api.organization.getOrganization({ id: this.#id }));
  #members = new Resource<OrganizationMemberList>(this, () =>
    api.organization.listOrganizationMembers({ organizationId: this.#id }),
  );

  #address = "";
  #role: MemberRole = "member";
  #busy = false;
  #alert = errorAlert();
  #status = statusMessage();

  #loading!: HTMLElement;
  #content!: HTMLElement;
  #heading!: HTMLElement;
  #address_!: HTMLElement;
  #external!: HTMLElement;
  #meta!: HTMLElement;
  #blurb!: HTMLElement;
  #description!: HTMLElement;
  #publish!: ReturnType<typeof publishControl>;
  #roster!: HTMLElement;
  #addForm!: HTMLFormElement;
  #addressField!: ReturnType<typeof textField>;
  #addSubmit!: ReturnType<typeof button>;
  #ownerPanels!: HTMLElement;
  #danger!: HTMLElement;

  #shows: unknown = null;
  #rosterShows: unknown = null;
  #rosterManage = false;

  get #id(): string {
    return this.routeId ?? "";
  }

  protected build(): void {
    this.#loading = h("p", null, t("common.loading"));
    this.#heading = h("h1");
    this.#address_ = h("p", { class: "page-address" });
    // An external record that shares a local name is the case worth
    // spelling out, not merely badging.
    this.#external = h("p", { class: "alert alert-status", role: "status", hidden: true });
    this.#meta = h("p", { class: "page-meta" });
    this.#blurb = h("p", { hidden: true });
    this.#description = h("p", { class: "long-form", hidden: true });

    this.#publish = publishControl((setting) => {
      void (async () => {
        this.#alert.set(undefined);
        try {
          await api.organization.updateOrganization({ id: this.#id, publishEvents: setting });
          await this.#organization.load();
        } catch (err) {
          this.#alert.set(err);
        }
      })();
    });

    this.#roster = h("div", null, t("common.loading"));
    this.#addressField = textField({
      label: t("organization.addMemberAddress"),
      hint: t("organization.addMemberAddressHint"),
      required: true,
      onInput: (v) => {
        this.#address = v;
        this.invalidate();
      },
    });
    const roleSelect = selectField<MemberRole>({
      label: t("roles.legend"),
      value: this.#role,
      options: [
        { value: "member", label: t("roles.member") },
        { value: "owner", label: t("roles.owner") },
      ],
      onChange: (v) => {
        this.#role = v;
      },
    });
    this.#addSubmit = button(t("organization.addMemberAction"), { type: "submit" });
    this.#addForm = h(
      "form",
      { hidden: true },
      h("h3", null, t("organization.addMemberLegend")),
      h("div", { class: "field-row" }, this.#addressField.el, roleSelect.el),
      h("div", { class: "form-actions" }, this.#addSubmit),
    );
    this.#addForm.addEventListener("submit", (e) => {
      e.preventDefault();
      void this.#addMember();
    });

    // Accepting puts the gathering's owners on the roster, so the roster
    // has to be loaded again too — the organization record and its members
    // are two resources, and only refreshing one shows a stale roster next
    // to a fresh count.
    this.#ownerPanels = h(
      "div",
      { hidden: true },
      offerInbox({
        organizationId: this.#id,
        onAccepted: () => {
          void this.#organization.load();
          void this.#members.load();
        },
      }),
      webhooksPanel({ ownerKind: "organization", ownerId: this.#id }),
    );

    // Deleting an organization is an administrator's power alone. No
    // fallback: an administrator-only power is not news to somebody who
    // never tried to use it.
    this.#danger = h(
      "section",
      { class: "danger-zone", hidden: true },
      h("p", null, t("organization.dangerNote")),
      button(t("common.delete"), { variant: "danger", onClick: () => void this.#delete() }),
    );

    this.#content = h(
      "div",
      { hidden: true },
      this.#heading,
      this.#address_,
      this.#external,
      this.#meta,
      this.#blurb,
      this.#description,
      this.#publish.el,
      this.#alert.el,
      this.#status.el,
      h("section", null, h("h2", null, t("organization.roster")), this.#roster, this.#addForm),
      this.#ownerPanels,
      this.#danger,
    );
    this.replaceChildren(this.#loading, this.#content);
    void this.#organization.load();
    void this.#members.load();
  }

  async #addMember(): Promise<void> {
    if (this.#busy || !this.#address.includes("@")) return;
    this.#busy = true;
    this.#alert.set(undefined);
    this.invalidate();
    try {
      // Somebody is addressed as handle@domain, so that is what the form
      // asks for; resolving it to an id is the server's job, not a thing a
      // person should have to do first.
      const [handle, domain] = this.#address.split("@");
      const found = await api.admin.findUser({ handle: handle ?? "", domain: domain ?? "" });
      await api.organization.setOrganizationMember({ organizationId: this.#id, userId: found.userId, role: this.#role });
      this.#address = "";
      this.#addressField.control.value = "";
      await Promise.all([this.#organization.load(), this.#members.load()]);
    } catch (err) {
      this.#alert.set(err);
    } finally {
      this.#busy = false;
      this.invalidate();
    }
  }

  async #removeMember(userId: string): Promise<void> {
    this.#alert.set(undefined);
    try {
      await api.organization.removeOrganizationMember({ organizationId: this.#id, userId });
      await Promise.all([this.#organization.load(), this.#members.load()]);
    } catch (err) {
      this.#alert.set(err);
    }
  }

  async #delete(): Promise<void> {
    const current = this.#organization.value;
    if (!current) return;
    if (!window.confirm(t("common.confirmDelete", { name: current.name }))) return;
    this.#alert.set(undefined);
    try {
      await api.organization.deleteOrganization({ id: this.#id });
      this.#status.set(t("common.loading"));
      navigate("/organizations");
    } catch (err) {
      this.#alert.set(err);
    }
  }

  protected update(): void {
    const g = this.#organization.value;
    if (!g) {
      this.#content.hidden = true;
      this.#loading.hidden = false;
      fill(this.#loading, this.#organization.error ? errorAlertFor(this.#organization.error) : t("common.loading"));
      return;
    }
    this.#loading.hidden = true;
    this.#content.hidden = false;

    if (g !== this.#shows) {
      this.#shows = g;
      this.#heading.textContent = g.name;
      fill(this.#address_, originBadge(g.origin, g.slug));
      this.#external.hidden = !g.origin.isExternal;
      if (g.origin.isExternal) this.#external.textContent = t("origin.externalWarning", { domain: g.origin.domain });
      this.#meta.textContent = `${plural("organization.members", g.memberCount)} · ${plural("organization.owners", g.ownerCount)}`;
      this.#blurb.hidden = !g.blurb;
      this.#blurb.textContent = g.blurb;
      this.#description.hidden = !g.description;
      this.#description.textContent = g.description;
      this.#publish.el.hidden = !g.viewer.canEdit;
      this.#publish.update({ value: g.publishEvents, canEdit: g.viewer.canEdit });
      this.#addForm.hidden = !g.viewer.canManageMembers;
      this.#ownerPanels.hidden = !g.viewer.canManageMembers;
      this.#danger.hidden = !g.viewer.canDelete;
    }

    setDisabled(this.#addSubmit, this.#busy || !this.#address.includes("@"));

    // The roster is made again only when the list or the power to manage
    // it changes, so a keystroke in the form does not rebuild it.
    const roster = this.#members.value;
    const manage = g.viewer.canManageMembers;
    if (roster && (roster !== this.#rosterShows || manage !== this.#rosterManage)) {
      this.#rosterShows = roster;
      this.#rosterManage = manage;
      fill(
        this.#roster,
        h(
          "ul",
          { class: "roster" },
          roster.members.map((m) =>
            h(
              "li",
              null,
              h("span", { class: "roster-name" }, m.displayName || m.handle),
              " ",
              h("span", { class: "card-address" }, `${m.handle}@${m.linkkeysDomain}`),
              " ",
              h("span", { class: "badge" }, m.role === "owner" ? t("roles.owner") : t("roles.member")),
              " ",
              h("span", { class: "card-meta" }, date(m.joinedAt)),
              // The accessible name says who is being removed. A row of
              // buttons all named "Remove" is unusable when the rows are
              // read out of context.
              g.viewer.canManageMembers
                ? button(t("common.delete"), {
                    variant: "danger",
                    compact: true,
                    ariaLabel: t("organization.removeMember", { name: m.displayName || m.handle }),
                    onClick: () => void this.#removeMember(m.userId),
                  })
                : null,
            ),
          ),
        ),
      );
    } else if (!roster && this.#members.error && this.#rosterShows !== this.#members.error) {
      this.#rosterShows = this.#members.error;
      fill(this.#roster, errorAlertFor(this.#members.error));
    }
  }
}

define("tinku-organization-page", OrganizationDetailPage);
