import type { Webhook, WebhookList, WebhookOwnerKind, WebhookScope } from "~/gen/types.gen";
import { dateTime, t } from "~/i18n";
import { api } from "~/lib/api";
import { errorAlert, errorAlertFor, statusMessage } from "~/components/Alert";
import {
  button,
  checkField,
  selectField,
  setButtonText,
  setDisabled,
  textField,
  type PushButton,
} from "~/components/Field";
import { fill, h } from "~/ui/dom";
import { Resource, TinkuElement, define } from "~/ui/element";

const SCOPES: readonly WebhookScope[] = ["all", "structure_only"];

/**
 * The webhooks on one organization or one gathering.
 *
 * Rendered only for somebody who may manage them — the server refuses
 * anybody else, and a panel that always failed would be furniture.
 *
 * Two things here are not styling:
 *
 *   * The signing secret is shown ONCE, immediately after the webhook is
 *     made, and the panel says so. The server cannot show it again.
 *
 *   * Sending the whole record is opt-in behind an acknowledgement. The
 *     warning is not a scold: it is the owner's own information, and they
 *     are the right person to decide. What they need is to know what will
 *     leave, before it leaves.
 */
export class WebhooksPanel extends TinkuElement {
  ownerKind: WebhookOwnerKind = "organization";
  ownerId = "";

  #webhooks = new Resource<WebhookList>(this, () =>
    api.webhook.listWebhooks({ ownerKind: this.ownerKind, ownerId: this.ownerId }),
  );
  #url = "";
  #note = "";
  #scope: WebhookScope = "all";
  #includeDetails = false;
  #accepted = false;
  #secret = "";
  #busy = false;

  #alert = errorAlert();
  #status = statusMessage();
  #count!: HTMLElement;
  #secretBox!: HTMLElement;
  #secretCode!: HTMLElement;
  #list!: HTMLElement;
  #listShows: unknown = null;
  #rowButtons: PushButton[] = [];
  #full!: HTMLElement;
  #form!: HTMLFormElement;
  #urlField!: ReturnType<typeof textField>;
  #noteField!: ReturnType<typeof textField>;
  #scopeField!: ReturnType<typeof selectField<WebhookScope>>;
  #detailsField!: ReturnType<typeof checkField>;
  #acceptField!: ReturnType<typeof checkField>;
  #warning!: HTMLElement;
  #submit!: PushButton;

  #limit(): number {
    return Number(this.#webhooks.value?.limit ?? 5);
  }

  #countHooks(): number {
    return this.#webhooks.value?.webhooks.length ?? 0;
  }

  // The acknowledgement gates the SWITCH, not the form: somebody who never
  // asks for details never sees a checkbox they have to tick to proceed.
  #blocked(): boolean {
    return this.#includeDetails && !this.#accepted;
  }

  protected build(): void {
    this.#count = h("p", { class: "card-meta" });

    // The secret, once. It is in a live region because it appears without
    // the reader moving anywhere, and it stays until they dismiss it — a
    // panel that cleared it on the next render would lose the one copy
    // that exists.
    this.#secretCode = h("code", { class: "secret" });
    this.#secretBox = h(
      "div",
      { class: "alert alert-status", role: "status", hidden: true },
      h("p", null, h("strong", null, t("webhook.secretLabel"))),
      h("p", null, this.#secretCode),
      h("p", null, t("webhook.secretNote")),
      button(t("webhook.secretDone"), {
        variant: "default",
        compact: true,
        onClick: () => {
          this.#secret = "";
          this.invalidate();
        },
      }),
    );

    this.#list = h("div");
    this.#full = h("p", { class: "card-meta" }, t("webhook.full"));

    this.#urlField = textField({
      label: t("webhook.urlLabel"),
      hint: t("webhook.urlHint"),
      required: true,
      type: "url",
      onInput: (v) => {
        this.#url = v;
        this.invalidate();
      },
    });
    this.#noteField = textField({
      label: t("webhook.noteLabel"),
      optional: true,
      onInput: (v) => {
        this.#note = v;
      },
    });
    this.#scopeField = selectField<WebhookScope>({
      label: t("webhook.scopeLabel"),
      value: "all",
      options: SCOPES.map((scope) => ({ value: scope, label: t(`webhook.scope.${scope}`) })),
      onChange: (scope) => {
        this.#scope = scope;
      },
    });
    this.#detailsField = checkField({
      label: t("webhook.detailsLabel"),
      checked: false,
      onChange: (v) => {
        this.#includeDetails = v;
        this.#accepted = false;
        this.#acceptField.control.checked = false;
        this.invalidate();
      },
    });
    this.#acceptField = checkField({
      label: t("webhook.detailsAccept"),
      checked: false,
      onChange: (v) => {
        this.#accepted = v;
        this.invalidate();
      },
    });
    // The warning appears with the switch and not before it: a panel that
    // shouted about leaking data at somebody setting up a plain
    // notification would be noise, and noise is what gets clicked through.
    this.#warning = h(
      "div",
      { class: "alert alert-error", role: "alert", hidden: true },
      h("p", null, t("webhook.detailsWarning")),
      this.#acceptField.el,
    );
    this.#submit = button(t("webhook.add"), { type: "submit" });
    this.#form = h(
      "form",
      null,
      h("h3", null, t("webhook.add")),
      h("div", { class: "field-row" }, this.#urlField.el, this.#noteField.el),
      this.#scopeField.el,
      this.#detailsField.el,
      this.#warning,
      h("div", { class: "form-actions" }, this.#submit),
    );
    this.#form.addEventListener("submit", (e) => {
      e.preventDefault();
      if (this.#busy || !this.#url.trim() || this.#blocked()) return;
      void this.#act(async () => {
        const created = await api.webhook.createWebhook({
          ownerKind: this.ownerKind,
          ownerId: this.ownerId,
          url: this.#url.trim(),
          note: this.#note.trim(),
          scope: this.#scope,
          includeDetails: this.#includeDetails,
        });
        this.#secret = created.secret;
        this.#url = this.#note = "";
        this.#scope = "all";
        this.#includeDetails = this.#accepted = false;
        this.#urlField.control.value = "";
        this.#noteField.control.value = "";
        this.#scopeField.control.value = "all";
        this.#detailsField.control.checked = false;
        this.#acceptField.control.checked = false;
      }, t("webhook.added"));
    });

    this.replaceChildren(
      h(
        "section",
        null,
        h("h2", null, t("webhook.heading")),
        h("p", { class: "card-meta" }, t("webhook.note")),
        this.#count,
        this.#alert.el,
        this.#status.el,
        this.#secretBox,
        this.#list,
        this.#full,
        this.#form,
      ),
    );
    void this.#webhooks.load();
  }

  async #act(run: () => Promise<unknown>, done: string): Promise<void> {
    this.#busy = true;
    this.#alert.set(undefined);
    this.#status.set("");
    this.invalidate();
    try {
      await run();
      await this.#webhooks.load();
      this.#status.set(done);
    } catch (err) {
      this.#alert.set(err);
    } finally {
      this.#busy = false;
      if (this.isActive()) this.invalidate();
    }
  }

  #row(hook: Webhook): HTMLLIElement {
    const toggle = button(hook.active ? t("webhook.pause") : t("webhook.resume"), {
      variant: "default",
      compact: true,
      ariaLabel: hook.active
        ? t("webhook.pauseLabel", { url: hook.url })
        : t("webhook.resumeLabel", { url: hook.url }),
      onClick: () =>
        void this.#act(() => api.webhook.updateWebhook({ id: hook.id, active: !hook.active }), t("common.save")),
    });
    const remove = button(t("webhook.remove"), {
      variant: "danger",
      compact: true,
      ariaLabel: t("webhook.removeLabel", { url: hook.url }),
      onClick: () => void this.#act(() => api.webhook.deleteWebhook({ id: hook.id }), t("webhook.removed")),
    });
    this.#rowButtons.push(toggle, remove);
    return h(
      "li",
      null,
      h("span", { class: "roster-name" }, hook.url),
      hook.note ? h("span", { class: "card-meta" }, hook.note) : null,
      h("span", { class: "badge" }, t(`webhook.scope.${hook.scope}`)),
      hook.includeDetails ? h("span", { class: "badge badge-strong" }, t("webhook.sendsDetails")) : null,
      !hook.active ? h("span", { class: "badge" }, t("webhook.paused")) : null,
      hook.failureCount > 0
        ? h("span", { class: "badge" }, t("webhook.failing", { count: Number(hook.failureCount) }))
        : null,
      h("span", { class: "card-meta" }, hook.lastAttemptAt ? dateTime(hook.lastAttemptAt) : t("webhook.neverSent")),
      toggle,
      remove,
    );
  }

  protected update(): void {
    const count = this.#countHooks();
    const countText = t("webhook.count", { count, limit: this.#limit() });
    if (this.#count.textContent !== countText) this.#count.textContent = countText;

    this.#secretBox.hidden = !this.#secret;
    if (this.#secretCode.textContent !== this.#secret) this.#secretCode.textContent = this.#secret;

    const data = this.#webhooks.value;
    const shows = data ?? this.#webhooks.error ?? "loading";
    if (shows !== this.#listShows) {
      this.#listShows = shows;
      this.#rowButtons = [];
      if (data && data.webhooks.length > 0) {
        fill(this.#list, h("ul", { class: "roster" }, data.webhooks.map((hook) => this.#row(hook))));
      } else if (data) {
        fill(this.#list, h("p", { class: "empty" }, t("webhook.empty")));
      } else if (this.#webhooks.error) {
        fill(this.#list, errorAlertFor(this.#webhooks.error));
      } else {
        fill(this.#list, h("p", null, t("common.loading")));
      }
    }
    for (const b of this.#rowButtons) setDisabled(b, this.#busy);

    const full = count >= this.#limit();
    this.#full.hidden = !full;
    this.#form.hidden = full;
    this.#warning.hidden = !this.#includeDetails;
    setDisabled(this.#submit, this.#busy || !this.#url.trim() || this.#blocked());
    setButtonText(this.#submit, this.#busy ? t("common.saving") : t("webhook.add"));
  }
}
define("tinku-webhooks", WebhooksPanel);

/** The webhooks of one organization or gathering. Make it one time. */
export function webhooksPanel(props: { ownerKind: WebhookOwnerKind; ownerId: string }): HTMLElement {
  const el = document.createElement("tinku-webhooks") as WebhooksPanel;
  el.ownerKind = props.ownerKind;
  el.ownerId = props.ownerId;
  return el;
}
