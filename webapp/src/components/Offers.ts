import type { GatheringOffer, GatheringOfferList, Organization } from "~/gen/types.gen";
import { dateTime, t } from "~/i18n";
import { api } from "~/lib/api";
import { errorAlert, statusMessage } from "~/components/Alert";
import { button, setButtonText, setDisabled, textArea } from "~/components/Field";
import { organizationPicker } from "~/components/OrganizationPicker";
import { fill, h } from "~/ui/dom";
import { Resource, TinkuElement, define } from "~/ui/element";

/**
 * The offering side: a gathering's owner hands it to an organization.
 *
 * The note under the heading says what accepting will do, because an owner
 * about to give somebody else power over their gathering should not have to
 * find that out afterwards: the organization becomes an owner ALONGSIDE
 * them, the gathering's owners join its roster, and the gathering's members
 * are not touched.
 */
class OfferGatheringPanel extends TinkuElement {
  gatheringId = "";

  #offers = new Resource<GatheringOfferList>(this, () =>
    api.gathering.listGatheringOffers({ gatheringId: this.gatheringId }),
  );
  #organization: Organization | undefined;
  #note = "";
  #busy = false;
  #alert = errorAlert();
  #status = statusMessage();
  #picker!: ReturnType<typeof organizationPicker>;
  #noteField!: ReturnType<typeof textArea>;
  #submit!: ReturnType<typeof button>;
  #list!: HTMLUListElement;
  #listShows: unknown = null;

  protected build(): void {
    this.#picker = organizationPicker({
      label: t("offer.organizationLabel"),
      onChange: (organization) => {
        this.#organization = organization;
        this.invalidate();
      },
    });
    this.#noteField = textArea({
      label: t("offer.messageLabel"),
      optional: true,
      rows: 2,
      onInput: (v) => {
        this.#note = v;
      },
    });
    this.#submit = button(t("offer.send"), { type: "submit" });
    const form = h("form", null, this.#picker.el, this.#noteField.el, h("div", { class: "form-actions" }, this.#submit));
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      const target = this.#organization;
      if (!target || this.#busy) return;
      void this.#act(async () => {
        await api.gathering.offerGathering({
          gatheringId: this.gatheringId,
          organizationId: target.id,
          note: this.#note.trim(),
        });
        this.#organization = undefined;
        this.#picker.set(undefined);
        this.#note = "";
        this.#noteField.control.value = "";
      }, t("offer.sent"));
    });
    this.#list = h("ul", { class: "roster" });
    this.replaceChildren(
      h("h2", null, t("offer.heading")),
      h("p", { class: "card-meta" }, t("offer.note")),
      this.#alert.el,
      this.#status.el,
      this.#list,
      form,
    );
    void this.#offers.load();
  }

  async #act(run: () => Promise<unknown>, done: string): Promise<void> {
    this.#busy = true;
    // The row buttons show the busy state too, so the list is made again.
    this.#listShows = null;
    this.#alert.set(undefined);
    this.#status.set("");
    this.invalidate();
    try {
      await run();
      await this.#offers.load();
      this.#status.set(done);
    } catch (err) {
      this.#alert.set(err);
    } finally {
      this.#busy = false;
      this.#listShows = null;
      this.invalidate();
    }
  }

  protected update(): void {
    setDisabled(this.#submit, this.#busy || !this.#organization);
    setButtonText(this.#submit, this.#busy ? t("common.saving") : t("offer.send"));

    const offers = this.#offers.value?.offers ?? [];
    const shows = this.#offers.value ?? null;
    if (shows === this.#listShows) return;
    this.#listShows = shows;
    this.#list.hidden = offers.length === 0;
    fill(
      this.#list,
      offers.map((offer) =>
        h(
          "li",
          null,
          h("span", { class: "roster-name" }, t("offer.pending", { organization: offer.organizationName })),
          offer.viewer.canDelete
            ? button(t("offer.withdraw"), {
                variant: "default",
                compact: true,
                disabled: this.#busy,
                ariaLabel: t("offer.withdrawLabel", { organization: offer.organizationName }),
                onClick: () =>
                  void this.#act(
                    () => api.gathering.withdrawGatheringOffer({ offerId: offer.id }),
                    t("offer.withdrawn"),
                  ),
              })
            : null,
        ),
      ),
    );
  }
}
define("tinku-offer-gathering", OfferGatheringPanel);

/** Offer a gathering to an organization. Make it one time. */
export function offerGathering(props: { gatheringId: string }): HTMLElement {
  const el = document.createElement("tinku-offer-gathering") as OfferGatheringPanel;
  el.gatheringId = props.gatheringId;
  return el;
}

/**
 * The receiving side: what has been offered to this organization.
 *
 * Accept and decline are both here, and neither is the default: an
 * organization taking on a gathering is taking on something it will be
 * answerable for, so the choice is deliberate on both sides.
 */
class OfferInboxPanel extends TinkuElement {
  organizationId = "";
  onAccepted?: () => void;

  #offers = new Resource<GatheringOfferList>(this, () =>
    api.gathering.listGatheringOffers({ organizationId: this.organizationId }),
  );
  #busy = false;
  #alert = errorAlert();
  #status = statusMessage();
  #body!: HTMLElement;
  #shows: unknown = null;

  protected build(): void {
    this.#body = h("div");
    this.replaceChildren(h("h2", null, t("offer.inbox")), this.#alert.el, this.#status.el, this.#body);
    void this.#offers.load();
  }

  #answer(offer: GatheringOffer, accept: boolean): void {
    this.#busy = true;
    this.#alert.set(undefined);
    this.#status.set("");
    this.#shows = null;
    this.invalidate();
    void (async () => {
      try {
        await api.gathering.respondToGatheringOffer({ offerId: offer.id, accept });
        await this.#offers.load();
        this.#status.set(accept ? t("offer.accepted") : t("offer.declined"));
        if (accept) this.onAccepted?.();
      } catch (err) {
        this.#alert.set(err);
      } finally {
        this.#busy = false;
        this.#shows = null;
        this.invalidate();
      }
    })();
  }

  protected update(): void {
    const shows = this.#offers.value ?? null;
    if (shows === this.#shows) return;
    this.#shows = shows;
    const offers = this.#offers.value?.offers ?? [];
    if (offers.length === 0) {
      fill(this.#body, h("p", { class: "empty" }, t("offer.inboxEmpty")));
      return;
    }
    fill(
      this.#body,
      h(
        "ul",
        { class: "roster" },
        offers.map((offer) =>
          h(
            "li",
            null,
            h("span", { class: "roster-name" }, h("a", { href: `/gatherings/${offer.gatheringId}` }, offer.gatheringName)),
            // Who offered it, with their domain: a name from another
            // instance is not the same person as the same name here.
            h(
              "span",
              { class: "card-address" },
              t("offer.from", { who: `${offer.offeredBy.handle}@${offer.offeredBy.linkkeysDomain}` }),
            ),
            h("span", { class: "card-meta" }, dateTime(offer.createdAt)),
            offer.note ? h("span", { class: "card-meta" }, offer.note) : null,
            offer.viewer.canEdit && [
              button(t("offer.accept"), {
                compact: true,
                disabled: this.#busy,
                ariaLabel: t("offer.acceptLabel", { gathering: offer.gatheringName }),
                onClick: () => this.#answer(offer, true),
              }),
              button(t("offer.decline"), {
                variant: "default",
                compact: true,
                disabled: this.#busy,
                ariaLabel: t("offer.declineLabel", { gathering: offer.gatheringName }),
                onClick: () => this.#answer(offer, false),
              }),
            ],
          ),
        ),
      ),
    );
  }
}
define("tinku-offer-inbox", OfferInboxPanel);

/** The offers that an organization has received. Make it one time. */
export function offerInbox(props: { organizationId: string; onAccepted?: () => void }): HTMLElement {
  const el = document.createElement("tinku-offer-inbox") as OfferInboxPanel;
  el.organizationId = props.organizationId;
  el.onAccepted = props.onAccepted;
  return el;
}

/**
 * The administrator's one-sided path: put a loose gathering under an
 * organization with no offer and no acceptance.
 *
 * Separate from the offer panel on purpose. It is a different power with a
 * different justification, and a screen that ran the two together would
 * invite an administrator to use the blunt one by habit.
 */
class AdoptGatheringPanel extends TinkuElement {
  gatheringId = "";
  onAdopted?: () => void;

  #organization: Organization | undefined;
  #busy = false;
  #alert = errorAlert();
  #status = statusMessage();
  #picker!: ReturnType<typeof organizationPicker>;
  #submit!: ReturnType<typeof button>;

  protected build(): void {
    this.#picker = organizationPicker({
      label: t("offer.organizationLabel"),
      onChange: (organization) => {
        this.#organization = organization;
        this.invalidate();
      },
    });
    this.#submit = button(t("adopt.action"), { type: "submit" });
    const form = h("form", null, this.#picker.el, h("div", { class: "form-actions" }, this.#submit));
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      void this.#adopt();
    });
    this.replaceChildren(
      h("h2", null, t("adopt.heading")),
      h("p", { class: "card-meta" }, t("adopt.note")),
      this.#alert.el,
      this.#status.el,
      form,
    );
  }

  async #adopt(): Promise<void> {
    const target = this.#organization;
    if (!target || this.#busy) return;
    this.#busy = true;
    this.#alert.set(undefined);
    this.#status.set("");
    this.invalidate();
    try {
      await api.gathering.adoptGathering({ gatheringId: this.gatheringId, organizationId: target.id });
      this.#organization = undefined;
      this.#picker.set(undefined);
      this.#status.set(t("adopt.done"));
      this.onAdopted?.();
    } catch (err) {
      this.#alert.set(err);
    } finally {
      this.#busy = false;
      this.invalidate();
    }
  }

  protected update(): void {
    setDisabled(this.#submit, this.#busy || !this.#organization);
    setButtonText(this.#submit, this.#busy ? t("common.saving") : t("adopt.action"));
  }
}
define("tinku-adopt-gathering", AdoptGatheringPanel);

/** An administrator puts a loose gathering under an organization. Make it one time. */
export function adoptGathering(props: { gatheringId: string; onAdopted?: () => void }): HTMLElement {
  const el = document.createElement("tinku-adopt-gathering") as AdoptGatheringPanel;
  el.gatheringId = props.gatheringId;
  el.onAdopted = props.onAdopted;
  return el;
}
