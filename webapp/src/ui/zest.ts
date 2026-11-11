/**
 * Define the Zest tags that tinku uses. Importing a Zest package defines
 * nothing, so index.ts (and the test setup) call this before the first
 * element.
 */
import { defineControls } from "@catalystcommunity/controls";
import { DateTimePicker } from "@catalystcommunity/date-time-picker";
import { define } from "~/ui/element";

/**
 * Workaround for date-time-picker 0.2.8: its connectedCallback validates
 * BEFORE render() puts the inner input in the element, and a required,
 * empty picker then calls `setValidity(..., this.input)` with an anchor
 * that is not a descendant. Chromium throws NotFoundError and the picker
 * never draws. Set `required` after the first render instead. Remove this
 * when Zest fixes the order.
 */
class TinkuDateTimePicker extends DateTimePicker {
  connectedCallback(): void {
    const required = this.hasAttribute("required");
    if (required) this.removeAttribute("required");
    super.connectedCallback();
    if (required) this.setAttribute("required", "");
  }
}

export function defineZest(): void {
  defineControls();
  define("date-time-picker", TinkuDateTimePicker);
}
