import { Show, type JSX } from "solid-js";
import type { PublishDecision, PublishSetting } from "~/gen/types.gen";
import { useI18n } from "~/i18n";
import { Field } from "./Field";
import { Select } from "./Select";

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
 */
export function PublishControl(props: {
  value: PublishSetting;
  decision?: PublishDecision;
  canEdit: boolean;
  onChange: (setting: PublishSetting) => void;
}): JSX.Element {
  const { t } = useI18n();

  const decidedBy = () => {
    switch (props.decision?.source) {
      case "gathering":
        return t("publish.decidedByGathering");
      case "organization":
        return t("publish.decidedByOrganization");
      default:
        return t("publish.decidedByInstance");
    }
  };

  return (
    <fieldset>
      <legend>{t("publish.legend")}</legend>

      {/* What it currently resolves to, in words, before any control. The
          resolved answer is the thing a person actually wants to know. */}
      <Show when={props.decision}>
        {(decision) => (
          <p class="card-meta">
            {decision().publishing ? t("publish.resolvedIn") : t("publish.resolvedOut")}{" "}
            {decidedBy()}
          </p>
        )}
      </Show>

      <Show
        when={props.canEdit && (props.decision?.canOverride ?? true)}
        fallback={
          <Show when={props.canEdit}>
            <p class="field-hint">{t("publish.cannotOverride")}</p>
          </Show>
        }
      >
        <Field label={t("publish.label")}>
          {(control) => (
            <Select
              control={control}
              value={props.value}
              options={PUBLISH_SETTINGS}
              label={(setting) => t(`publish.${setting}`)}
              onChange={props.onChange}
            />
          )}
        </Field>
      </Show>
    </fieldset>
  );
}
