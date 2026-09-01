import { For, createEffect, type JSX } from "solid-js";

/**
 * A `<select>` that actually shows its bound value.
 *
 * A plain `<select value={...}>` in Solid does not. The value property is
 * applied while the element is being created, BEFORE the `<For>` that
 * renders the options has produced any; a select with no matching option
 * discards the assignment and falls back to the first one. The control then
 * displays something other than the state it is bound to — the screen says
 * "Africa/Abidjan" while the form is about to submit "America/Denver".
 *
 * Setting the value from an effect fixes it, because an effect runs after
 * the children are in place. The effect reads both the value and the option
 * list, so it re-runs when either changes.
 */
export function Select<T extends string>(props: {
  value: T;
  options: readonly T[];
  label: (option: T) => string;
  onChange: (value: T) => void;
  control?: Record<string, unknown>;
}): JSX.Element {
  let element!: HTMLSelectElement;

  createEffect(() => {
    // Both reads are deliberate: the effect must re-run when the options
    // arrive, not only when the value changes.
    const desired = props.value;
    void props.options.length;
    if (element.value !== desired) {
      element.value = desired;
    }
  });

  return (
    <select
      {...props.control}
      ref={element}
      onChange={(e) => props.onChange(e.currentTarget.value as T)}
    >
      <For each={props.options}>
        {(option) => <option value={option}>{props.label(option)}</option>}
      </For>
    </select>
  );
}
