import type { GatheringList, Organization } from "~/gen/types.gen";
import { t } from "~/i18n";
import { api } from "~/lib/api";
import { errorField } from "~/lib/messages";
import { SESSION, useSession } from "~/lib/session";
import { errorAlert, errorAlertFor } from "~/components/Alert";
import { cardList, gatheringCard } from "~/components/Cards";
import { button, setButtonText, setDisabled, textArea, textField } from "~/components/Field";
import { organizationPicker } from "~/components/OrganizationPicker";
import { fill, h } from "~/ui/dom";
import { Resource, TinkuElement, define } from "~/ui/element";

/** Every gathering, plus the form to start one. */
export class GatheringsPage extends TinkuElement {
  static watches = [SESSION];

  #gatherings = new Resource<GatheringList>(this, () => api.gathering.listGatherings({}));
  #name = "";
  #blurb = "";
  #description = "";
  // Optional, and offered only to somebody who belongs to an organization:
  // a picker that is always empty is a question with no answers.
  #owner: Organization | undefined;
  #busy = false;
  #error: unknown = undefined;
  #alert = errorAlert();

  #create!: HTMLElement;
  #nameField!: ReturnType<typeof textField>;
  #blurbField!: ReturnType<typeof textArea>;
  #descriptionField!: ReturnType<typeof textArea>;
  #picker!: ReturnType<typeof organizationPicker>;
  #submit!: ReturnType<typeof button>;
  #list!: HTMLElement;
  /** What the list shows now. The list is made again only when it changes. */
  #listShows: unknown = null;

  protected build(): void {
    this.#nameField = textField({
      label: t("gathering.nameLabel"),
      required: true,
      maxlength: 120,
      onInput: (v) => {
        this.#name = v;
        this.invalidate();
      },
    });
    this.#blurbField = textArea({
      label: t("gathering.blurbLabel"),
      optional: true,
      rows: 3,
      onInput: (v) => {
        this.#blurb = v;
      },
    });
    this.#descriptionField = textArea({
      label: t("gathering.descriptionLabel"),
      optional: true,
      rows: 6,
      onInput: (v) => {
        this.#description = v;
      },
    });
    // `mine` keeps this to organizations the caller belongs to. Anybody can
    // be shown every organization on the instance; only an owner of one can
    // put a gathering under it, so offering the rest would be offering a
    // refusal.
    this.#picker = organizationPicker({
      label: t("gathering.ownerLabel"),
      hint: t("gathering.ownerHint"),
      mine: true,
      onChange: (organization) => {
        this.#owner = organization;
      },
    });
    this.#submit = button(t("common.create"), { type: "submit" });

    const form = h(
      "form",
      null,
      this.#nameField.el,
      this.#blurbField.el,
      this.#descriptionField.el,
      this.#picker.el,
      h("div", { class: "form-actions" }, this.#submit),
      this.#alert.el,
    );
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      void this.#submitForm();
    });

    this.#create = h("section", null, h("h2", null, t("gathering.new")), form);
    this.#list = h("section");
    this.replaceChildren(h("h1", null, t("gathering.plural")), this.#create, this.#list);
    void this.#gatherings.load();
  }

  async #submitForm(): Promise<void> {
    if (this.#busy || !this.#name.trim()) return;
    this.#busy = true;
    this.#error = undefined;
    this.invalidate();
    try {
      const organization = this.#owner;
      await api.gathering.createGathering({
        name: this.#name,
        blurb: this.#blurb,
        description: this.#description,
        // Absent means the caller owns it themselves. Naming an
        // organization they do not own is refused by the server, which is
        // the only place that decision belongs.
        owner: organization ? { kind: "organization", id: organization.id } : undefined,
      });
      this.#name = this.#blurb = this.#description = "";
      this.#nameField.control.value = "";
      this.#blurbField.control.value = "";
      this.#descriptionField.control.value = "";
      this.#owner = undefined;
      this.#picker.set(undefined);
      await this.#gatherings.load();
    } catch (err) {
      this.#error = err;
    } finally {
      this.#busy = false;
      this.invalidate();
    }
  }

  protected update(): void {
    // An anonymous caller gets no create form: the whole form is absent
    // rather than present and disabled.
    this.#create.hidden = !useSession().user();

    this.#alert.set(this.#error);
    this.#nameField.setError(errorField(this.#error) === "name" ? t("error.invalid") : undefined);
    setDisabled(this.#submit, this.#busy || !this.#name.trim());
    setButtonText(this.#submit, this.#busy ? t("common.creating") : t("common.create"));

    const list = this.#gatherings.value;
    const shows = list ?? this.#gatherings.error ?? "loading";
    if (shows === this.#listShows) return;
    this.#listShows = shows;
    if (list) fill(this.#list, cardList(list.gatherings, gatheringCard, t("gathering.emptyList")));
    else if (this.#gatherings.error) fill(this.#list, errorAlertFor(this.#gatherings.error));
    else fill(this.#list, h("p", null, t("common.loading")));
  }
}

define("tinku-gatherings-page", GatheringsPage);
