import { Show, createUniqueId, splitProps, type JSX } from "solid-js";

/**
 * A labelled form control.
 *
 * The point of this component is the wiring, which is easy to get subtly
 * wrong by hand and invisible when you do:
 *
 *   * the label's `for` and the control's `id` are generated together, so
 *     they cannot drift;
 *   * a hint and an error are both referenced by `aria-describedby`, so a
 *     screen reader reads them as part of the field rather than as loose
 *     text somewhere near it;
 *   * `aria-invalid` marks the field itself, so the error is discoverable
 *     from the control and not only by reading the whole form;
 *   * "required" is conveyed in the label text as well as the attribute,
 *     because an asterisk alone is a convention, not information.
 */
export function Field(
  props: {
    label: string;
    hint?: string;
    error?: string;
    required?: boolean;
    requiredText?: string;
    optionalText?: string;
    children: (controlProps: {
      id: string;
      "aria-describedby": string | undefined;
      "aria-invalid": boolean | undefined;
      required: boolean | undefined;
    }) => JSX.Element;
  },
): JSX.Element {
  const id = createUniqueId();
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;

  const describedBy = () => {
    const ids = [props.hint ? hintId : "", props.error ? errorId : ""].filter(Boolean);
    return ids.length ? ids.join(" ") : undefined;
  };

  return (
    <div class="field">
      <label for={id}>
        {props.label}
        <Show when={props.required && props.requiredText}>
          <span class="field-flag"> ({props.requiredText})</span>
        </Show>
        <Show when={!props.required && props.optionalText}>
          <span class="field-flag"> ({props.optionalText})</span>
        </Show>
      </label>
      {props.children({
        id,
        "aria-describedby": describedBy(),
        "aria-invalid": props.error ? true : undefined,
        required: props.required || undefined,
      })}
      <Show when={props.hint}>
        <p class="field-hint" id={hintId}>
          {props.hint}
        </p>
      </Show>
      <Show when={props.error}>
        <p class="field-error" id={errorId}>
          {props.error}
        </p>
      </Show>
    </div>
  );
}

/** A checkbox with its label, which does not take the Field shape because
 *  the label follows the control rather than preceding it. */
export function CheckField(props: {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
}): JSX.Element {
  const [local] = splitProps(props, ["label", "checked", "onChange", "disabled"]);
  const id = createUniqueId();
  return (
    <div class="check-field">
      <input
        type="checkbox"
        id={id}
        checked={local.checked}
        disabled={local.disabled}
        onChange={(e) => local.onChange(e.currentTarget.checked)}
      />
      <label for={id}>{local.label}</label>
    </div>
  );
}
