import type { PublishDecision, PublishSetting } from "~/gen/types.gen";
import { t } from "~/i18n";
import { fill, h } from "~/ui/dom";
import { selectField } from "./Field";

/** The three states, in the order a reader meets them. */
const PUBLISH_SETTINGS: readonly PublishSetting[] = ["unset", "in", "out"];

/**
 * The publish choice for one organization or gathering.
 *
 * Three states, not two. "Follow the level above" is a real answer, and a
 * checkbox could not say it — which is the whole reason this is a select.
 *
 * When the instance has withdrawn the right to override, the control is not
 * rendered at all and the reason is shown instead. A disabled control that
 * looks editable is worse than no control: it invites somebody to try, and
 * says nothing about why it did not work.
 *
 * Make it one time and call `update()` with each new record: the select is
 * kept, so a render does not take the focus from it.
 */
export function publishControl(onChange: (setting: PublishSetting) => void): {
  el: HTMLElement;
  update(props: { value: PublishSetting; decision?: PublishDecision; canEdit: boolean }): void;
} {
  const resolved = h("p", { class: "card-meta" });
  const cannot = h("p", { class: "field-hint" }, t("publish.cannotOverride"));
  const select = selectField<PublishSetting>({
    label: t("publish.label"),
    value: "unset",
    options: PUBLISH_SETTINGS.map((setting) => ({ value: setting, label: t(`publish.${setting}`) })),
    onChange,
  });
  const el = h("fieldset", null, h("legend", null, t("publish.legend")), resolved, cannot, select.el);

  const decidedBy = (decision?: PublishDecision) => {
    switch (decision?.source) {
      case "gathering":
        return t("publish.decidedByGathering");
      case "organization":
        return t("publish.decidedByOrganization");
      default:
        return t("publish.decidedByInstance");
    }
  };

  return {
    el,
    update({ value, decision, canEdit }) {
      // What it currently resolves to, in words, before any control. The
      // resolved answer is the thing a person actually wants to know.
      resolved.hidden = !decision;
      if (decision) {
        fill(resolved, decision.publishing ? t("publish.resolvedIn") : t("publish.resolvedOut"), " ", decidedBy(decision));
      }
      const editable = canEdit && (decision?.canOverride ?? true);
      select.el.hidden = !editable;
      cannot.hidden = editable || !canEdit;
      if (select.control.value !== value) select.control.value = value;
    },
  };
}
