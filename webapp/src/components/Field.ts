/**
 * Labelled form controls, made from the Zest controls.
 *
 * The point of these builders is the wiring, which is easy to get subtly
 * wrong by hand and invisible when you do:
 *
 *   * the label is the control's own `label`, so the label and the control
 *     cannot drift apart;
 *   * a hint and an error are read as part of the field (Zest `helper` and
 *     `error`, or `aria-describedby` on the host for the controls that have
 *     no help text of their own), not as loose text near it;
 *   * an error marks the field itself (`aria-invalid`), so it can be found
 *     from the control and not only by reading the whole form;
 *   * "required" is in the label text as well as the attribute, because an
 *     asterisk alone is a convention, not information.
 *
 * Every builder returns the same shape, `Field`: put `el` in the form, read
 * and write `control`, and call `setError` and `setHint`.
 *
 * Make a field in `build()`, never in `update()`: a field made again loses
 * what the person typed and where the focus was.
 */
import type {
  CheckBox,
  ComboBox,
  ControlOptionInput,
  PushButton,
  SpinBox,
  TextArea,
  TextField,
} from "@catalystcommunity/controls";
import type { DateTimePicker } from "@catalystcommunity/date-time-picker";
import { t } from "~/i18n";
import { h, uid, type Child } from "~/ui/dom";

export type { CheckBox, ComboBox, PushButton, SpinBox, TextArea, TextField, DateTimePicker };

export interface Field<T extends HTMLElement> {
  /** What goes in the form. */
  el: HTMLElement;
  control: T;
  /** Show a problem, or clear it with undefined. */
  setError(message?: string): void;
  /** Change the hint, or clear it with undefined. */
  setHint(message?: string): void;
}

export interface LabelOptions {
  label: string;
  hint?: string;
  error?: string;
  /** Says "(required)" in the label, and sets `required`. */
  required?: boolean;
  /** Says "(optional)" in the label. */
  optional?: boolean;
}

/** The label with its flag, as one message. */
export function labelText(opts: Pick<LabelOptions, "label" | "required" | "optional">): string {
  if (opts.required) return t("field.required", { label: opts.label });
  if (opts.optional) return t("field.optional", { label: opts.label });
  return opts.label;
}

type TextBase = LabelOptions & {
  value?: string;
  maxlength?: number;
  placeholder?: string;
  autocomplete?: string;
  name?: string;
  disabled?: boolean;
  /** Each keystroke. */
  onInput?: (value: string) => void;
  /** The value is committed (the person leaves the field). */
  onChange?: (value: string) => void;
};

function textControl<T extends TextField | TextArea>(tag: "text-field" | "text-area", opts: TextBase, extra: Record<string, unknown>): Field<T> {
  const control = h(tag, {
    label: labelText(opts),
    required: opts.required,
    maxlength: opts.maxlength,
    placeholder: opts.placeholder,
    autocomplete: opts.autocomplete,
    name: opts.name,
    disabled: opts.disabled,
    ...extra,
  }) as unknown as T;
  control.value = opts.value ?? "";
  if (opts.hint) control.helper = opts.hint;
  if (opts.error) control.error = opts.error;
  control.addEventListener("input", () => opts.onInput?.(control.value));
  control.addEventListener("change", () => opts.onChange?.(control.value));
  return {
    el: control,
    control,
    setError: (message) => {
      if (control.error !== (message ?? "")) control.error = message ?? "";
    },
    setHint: (message) => {
      if (control.helper !== (message ?? "")) control.helper = message ?? "";
    },
  };
}

/** A one-line text field. `type` is text, url, search, email or tel. */
export function textField(opts: TextBase & { type?: "text" | "url" | "search" | "email" | "tel" }): Field<TextField> {
  return textControl<TextField>("text-field", opts, { type: opts.type ?? "text" });
}

/** A text area. */
export function textArea(opts: TextBase & { rows?: number }): Field<TextArea> {
  return textControl<TextArea>("text-area", opts, { rows: opts.rows ?? 3 });
}

/**
 * A wrapper for a control that has no help text of its own: the hint and
 * the error are paragraphs under it, which the host names in
 * `aria-describedby` (Zest gives the ids to the inner control).
 */
function described<T extends HTMLElement>(control: T, opts: LabelOptions, before?: Child): Field<T> {
  const hint = h("p", { class: "field-hint", id: uid("hint"), hidden: !opts.hint }, opts.hint ?? "");
  const error = h("p", { class: "field-error", id: uid("error"), hidden: !opts.error }, opts.error ?? "");
  const el = h("div", { class: "field" }, before, control, hint, error);
  const sync = () => {
    const ids = [!hint.hidden && hint.id, !error.hidden && error.id].filter(Boolean).join(" ");
    if (ids) control.setAttribute("aria-describedby", ids);
    else control.removeAttribute("aria-describedby");
    if (error.hidden) control.removeAttribute("aria-invalid");
    else control.setAttribute("aria-invalid", "true");
  };
  sync();
  const set = (p: HTMLElement, message?: string) => {
    if (p.textContent !== (message ?? "")) p.textContent = message ?? "";
    p.hidden = !message;
    sync();
  };
  return { el, control, setError: (m) => set(error, m), setHint: (m) => set(hint, m) };
}

export interface SelectOption<T extends string> {
  value: T;
  label: string;
  description?: string;
}

/**
 * A select. The value is applied after the options, so the control always
 * shows the value it holds: a select whose value is set before its options
 * exist falls back to the first option, and then says "Africa/Abidjan"
 * while the form is about to send "America/Denver".
 */
export function selectField<T extends string>(
  opts: LabelOptions & {
    value: T;
    options: readonly SelectOption<T>[];
    onChange: (value: T) => void;
    disabled?: boolean;
  },
): Field<ComboBox> & { setOptions(options: readonly SelectOption<T>[], value?: T): void } {
  const control = h("combo-box", { label: labelText(opts), required: opts.required, disabled: opts.disabled }) as unknown as ComboBox;
  const setOptions = (options: readonly SelectOption<T>[], value?: T) => {
    control.options = options.map((o) => ({ value: o.value, label: o.label, description: o.description })) as ControlOptionInput[];
    const next = value ?? (control.value as T);
    if (control.value !== next) control.value = next;
  };
  setOptions(opts.options, opts.value);
  control.addEventListener("change", (e) => {
    if (e.target === control) opts.onChange(control.value as T);
  });
  return { ...described(control, opts), setOptions };
}

/** A check box with its label after it. */
export function checkField(opts: {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  disabled?: boolean;
}): Field<CheckBox> {
  const control = h("check-box", { class: "check-field", disabled: opts.disabled }, opts.label) as unknown as CheckBox;
  control.checked = opts.checked;
  control.addEventListener("change", (e) => {
    if (e.target === control) opts.onChange(control.checked);
  });
  return described(control, { label: opts.label });
}

/** A number. `null` is blank, which is not the same as zero. */
export function numberField(
  opts: LabelOptions & {
    value: number | null;
    min?: number;
    max?: number;
    step?: number;
    onChange: (value: number | null) => void;
  },
): Field<SpinBox> {
  const control = h("spin-box", {
    label: labelText(opts),
    required: opts.required,
    min: opts.min,
    max: opts.max,
    step: opts.step,
  }) as unknown as SpinBox;
  control.value = opts.value;
  const send = () => opts.onChange(control.value);
  control.addEventListener("input", send);
  control.addEventListener("change", send);
  return described(control, opts);
}

/**
 * A date, a time, or both, as wall clock text with no zone: `2026-02-11`,
 * `19:00`, `2026-02-11T19:00`. These are the same forms as the native
 * date, time and datetime-local inputs, so lib/zonedTime reads them.
 */
export function dateTimeField(
  opts: LabelOptions & {
    mode: "date" | "time" | "datetime";
    value: string;
    onChange: (value: string) => void;
    /** The zone of "today" and "now" in the panel. */
    timeZone?: string;
    min?: string;
    max?: string;
    minuteStep?: number;
  },
): Field<DateTimePicker> & { setTimeZone(zone?: string): void } {
  const text = labelText(opts);
  const id = uid("dtp");
  const control = h("date-time-picker", {
    id,
    mode: opts.mode,
    label: text,
    required: opts.required,
    min: opts.min,
    max: opts.max,
    "time-zone": opts.timeZone,
    "minute-step": opts.minuteStep,
  }) as unknown as DateTimePicker;
  control.value = opts.value;
  control.addEventListener("change", (e) => {
    if (e.target === control) opts.onChange(control.value);
  });
  // The picker has no visible label of its own. `label` names it for a
  // screen reader; this one is for the eye.
  const visible = h("span", { class: "field-label", "aria-hidden": "true" }, text);
  return {
    ...described(control, opts, visible),
    setTimeZone: (zone) => {
      if (zone) control.setAttribute("time-zone", zone);
      else control.removeAttribute("time-zone");
    },
  };
}

export type ButtonVariant = "primary" | "default" | "danger" | "flat";

/**
 * A button. `primary` is the one action of a form, `default` the quieter
 * of two, `danger` a deletion, `flat` a link-like action in a line of text.
 * `compact` is a small button for a row in a list.
 *
 * A button in a list needs `ariaLabel` that says which row it acts on:
 * five buttons that all read "Remove" tell a screen reader nothing.
 */
export function button(
  label: string,
  opts: {
    variant?: ButtonVariant;
    type?: "button" | "submit";
    onClick?: (e: MouseEvent) => void;
    ariaLabel?: string;
    compact?: boolean;
    disabled?: boolean;
  } = {},
): PushButton {
  const el = h(
    "push-button",
    {
      variant: opts.variant ?? "primary",
      type: opts.type ?? "button",
      "aria-label": opts.ariaLabel,
      class: opts.compact ? "compact" : undefined,
      disabled: opts.disabled,
    },
    label,
  ) as unknown as PushButton;
  if (opts.onClick) el.addEventListener("click", opts.onClick as EventListener);
  return el;
}

/**
 * Set an ARIA state on the button that takes the focus. A push-button
 * forwards only `aria-label` and `aria-describedby` from its host, so a
 * state such as `aria-expanded` on the host is never read. Call it after
 * the button is connected (in `update()`).
 */
export function setInnerAria(el: PushButton, name: "aria-expanded" | "aria-pressed" | "aria-controls", value: string): void {
  const inner = el.querySelector("button");
  if (inner && inner.getAttribute(name) !== value) inner.setAttribute(name, value);
}

/** Change the text of a button, only when it changes. */
export function setButtonText(el: PushButton, text: string): void {
  if (el.textContent !== text) el.textContent = text;
}

/** Enable or disable a control, only when it changes. */
export function setDisabled(el: { disabled: boolean }, disabled: boolean): void {
  if (el.disabled !== disabled) el.disabled = disabled;
}
