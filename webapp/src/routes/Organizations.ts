import type { OrganizationList } from "~/gen/types.gen";
import { plural, t } from "~/i18n";
import { api } from "~/lib/api";
import { SESSION, useSession } from "~/lib/session";
import { errorAlert, errorAlertFor } from "~/components/Alert";
import { cardList, organizationCard } from "~/components/Cards";
import { button, setButtonText, setDisabled, textArea, textField } from "~/components/Field";
import { fill, h } from "~/ui/dom";
import { Resource, TinkuElement, define } from "~/ui/element";

/** Every organization, plus the form to start one. */
export class OrganizationsPage extends TinkuElement {
  static watches = [SESSION];

  #organizations = new Resource<OrganizationList>(this, () => api.organization.listOrganizations({}));
  #name = "";
  #blurb = "";
  #description = "";
  #busy = false;
  #alert = errorAlert();

  #create!: HTMLElement;
  #nameField!: ReturnType<typeof textField>;
  #blurbField!: ReturnType<typeof textArea>;
  #descriptionField!: ReturnType<typeof textArea>;
  #count!: HTMLElement;
  #submit!: ReturnType<typeof button>;
  #list!: HTMLElement;
  /** What the list shows now. The list is made again only when it changes,
   *  so a keystroke in the form does not take the focus from a card. */
  #listShows: unknown = null;

  // The 300-word limit is a server rule. Counting here as well is not
  // duplication of the rule — it is telling somebody where they are before
  // they submit, which the server cannot do.
  #words(): number {
    return this.#blurb.trim().split(/\s+/).filter(Boolean).length;
  }

  protected build(): void {
    this.#nameField = textField({
      label: t("organization.nameLabel"),
      required: true,
      maxlength: 120,
      onInput: (v) => {
        this.#name = v;
        this.invalidate();
      },
    });
    this.#blurbField = textArea({
      label: t("organization.blurbLabel"),
      hint: t("organization.blurbHint"),
      optional: true,
      rows: 3,
      onInput: (v) => {
        this.#blurb = v;
        this.invalidate();
      },
    });
    this.#descriptionField = textArea({
      label: t("organization.descriptionLabel"),
      hint: t("organization.descriptionHint"),
      optional: true,
      rows: 6,
      onInput: (v) => {
        this.#description = v;
      },
    });
    // The running count is polite, not assertive: it changes on every
    // keystroke, and an assertive region would interrupt the typing it is
    // describing.
    this.#count = h("p", { class: "field-hint", role: "status", "aria-live": "polite" });
    this.#submit = button(t("common.create"), { type: "submit" });

    const form = h(
      "form",
      null,
      this.#nameField.el,
      this.#blurbField.el,
      this.#count,
      this.#descriptionField.el,
      h("div", { class: "form-actions" }, this.#submit),
      this.#alert.el,
    );
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      void this.#submitForm();
    });

    this.#create = h("section", null, h("h2", null, t("organization.new")), form);
    this.#list = h("section");
    this.replaceChildren(h("h1", null, t("organization.plural")), this.#create, this.#list);
    void this.#organizations.load();
  }

  async #submitForm(): Promise<void> {
    if (this.#busy || !this.#name.trim() || this.#words() > 300) return;
    this.#busy = true;
    this.#alert.set(undefined);
    this.invalidate();
    try {
      await api.organization.createOrganization({ name: this.#name, blurb: this.#blurb, description: this.#description });
      this.#name = this.#blurb = this.#description = "";
      this.#nameField.control.value = "";
      this.#blurbField.control.value = "";
      this.#descriptionField.control.value = "";
      await this.#organizations.load();
    } catch (err) {
      this.#alert.set(err);
    } finally {
      this.#busy = false;
      this.invalidate();
    }
  }

  protected update(): void {
    // An anonymous caller gets no create form: the whole form is absent
    // rather than present and disabled.
    this.#create.hidden = !useSession().user();

    const words = this.#words();
    this.#blurbField.setError(words > 300 ? plural("organization.blurbCount", words) : undefined);
    const count = plural("organization.blurbCount", words);
    if (this.#count.textContent !== count) this.#count.textContent = count;
    setDisabled(this.#submit, this.#busy || !this.#name.trim() || words > 300);
    setButtonText(this.#submit, this.#busy ? t("common.creating") : t("common.create"));

    const list = this.#organizations.value;
    const shows = list ?? this.#organizations.error ?? "loading";
    if (shows === this.#listShows) return;
    this.#listShows = shows;
    if (list) fill(this.#list, cardList(list.organizations, organizationCard, t("organization.emptyList")));
    else if (this.#organizations.error) fill(this.#list, errorAlertFor(this.#organizations.error));
    else fill(this.#list, h("p", null, t("common.loading")));
  }
}

define("tinku-organizations-page", OrganizationsPage);
