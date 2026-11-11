import { t } from "~/i18n";
import { errorDetail, errorKey } from "~/lib/messages";
import { h } from "~/ui/dom";

/**
 * An error, announced.
 *
 * `role="alert"` is what makes an assistive technology read this the moment
 * it appears — without it a failure is silent for anybody not watching the
 * region it rendered into, which is the whole population this matters most
 * for.
 *
 * Make it one time, put `el` where the error belongs, and call `set()`.
 * Nothing shows while the error is undefined.
 */
export function errorAlert(): { el: HTMLElement; set(error: unknown): void } {
  const el = h("p", { class: "alert alert-error", role: "alert", hidden: true });
  let last: unknown = undefined;
  return {
    el,
    set(error: unknown) {
      if (error === last) return;
      last = error;
      if (!error) {
        el.hidden = true;
        el.replaceChildren();
        return;
      }
      const detail = errorDetail(error);
      el.replaceChildren(t(errorKey(error)), detail ? h("span", { class: "alert-detail" }, ` ${detail}`) : "");
      el.hidden = false;
    },
  };
}

/** An error alert with its error set, for a part that is made again. */
export function errorAlertFor(error: unknown): HTMLElement {
  const alert = errorAlert();
  alert.set(error);
  return alert.el;
}

/**
 * A status message.
 *
 * `aria-live="polite"` announces it at the next pause rather than
 * interrupting, which is right for a confirmation and wrong for an error —
 * hence two helpers rather than one with a flag. The region stays in the
 * page while it is empty: a live region that appears with its text is
 * often not read.
 */
export function statusMessage(): { el: HTMLElement; set(text: string): void } {
  // Not `hidden` while empty: the stylesheet collapses an empty region
  // (`.alert-status:empty`) and keeps it in the accessibility tree.
  const el = h("p", { class: "alert alert-status", role: "status", "aria-live": "polite" });
  return {
    el,
    set(text: string) {
      if (el.textContent !== text) el.textContent = text;
    },
  };
}
