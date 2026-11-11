import type { ComboBox } from "@catalystcommunity/controls";
import type { Organization } from "~/gen/types.gen";
import { t } from "~/i18n";
import { api } from "~/lib/api";
import { h, uid } from "~/ui/dom";

/**
 * Find an organization by typing.
 *
 * The server matches the name, the blurb AND the description, because a
 * person may remember what an organization does rather than what it is
 * called — but it ranks a name match first, and only the name is shown
 * here. Showing a match whose reason is invisible reads as a wrong answer,
 * which is why the ordering is the server's job and not a detail of this
 * component.
 *
 * An editable Zest `combo-box`: the value a person picks is an id while the
 * thing they read is a name, and each option shows its domain as its
 * description — two instances can hold the same name, and a directory
 * shows both at once.
 *
 * Make it one time. `value()` is the chosen organization; `set(undefined)`
 * clears it, for example after a submit.
 */
export function organizationPicker(opts: {
  label: string;
  hint?: string;
  /** Only organizations the caller belongs to. For "start a gathering under". */
  mine?: boolean;
  placeholder?: string;
  onChange: (organization: Organization | undefined) => void;
}): { el: HTMLElement; value(): Organization | undefined; set(organization: Organization | undefined): void } {
  const control = h("combo-box", {
    label: opts.label,
    editable: true,
    placeholder: opts.placeholder,
  }) as unknown as ComboBox;
  const hint = opts.hint ? h("p", { class: "field-hint", id: uid("hint") }, opts.hint) : null;
  if (hint) control.setAttribute("aria-describedby", hint.id);
  // The live count is polite: it changes as the person types.
  const status = h("p", { class: "visually-hidden", role: "status", "aria-live": "polite" });
  const el = h("div", { class: "field picker" }, control, hint, status);

  let chosen: Organization | undefined;
  let found: Organization[] = [];
  let seq = 0;

  // The query is what the search keys on, so typing searches again and
  // clearing the box goes back to the unfiltered list — which is the useful
  // answer for somebody who owns two organizations and remembers neither
  // name.
  const search = async (query: string) => {
    const mine = ++seq;
    status.textContent = t("common.loading");
    try {
      const result = await api.organization.listOrganizations({
        query,
        mine: opts.mine ?? false,
        page: { limit: 8, offset: 0 },
      });
      if (mine !== seq) return;
      found = result.organizations;
      const current = control.value;
      control.options = found.map((o) => ({ value: o.id, label: o.name, description: o.origin.domain }));
      if (control.value !== current) control.value = current;
      status.textContent = found.length ? "" : t("picker.noMatches");
    } catch {
      if (mine === seq) status.textContent = t("picker.noMatches");
    }
  };

  const choose = (organization: Organization | undefined) => {
    if (organization?.id === chosen?.id) return;
    chosen = organization;
    opts.onChange(organization);
  };

  control.addEventListener("input", (e) => {
    if (e.target !== control) return;
    // Typing after a choice means the choice is being changed, so the old
    // one must not survive as a hidden selection.
    if (chosen && control.value !== chosen.id) choose(undefined);
    const text = (control.control as HTMLInputElement).value ?? "";
    void search(text);
  });
  control.addEventListener("change", (e) => {
    if (e.target !== control) return;
    choose(found.find((o) => o.id === control.value));
  });
  control.addEventListener("focusin", () => {
    if (found.length === 0) void search("");
  });

  return {
    el,
    value: () => chosen,
    set(organization) {
      chosen = organization;
      if (organization) {
        if (!found.some((o) => o.id === organization.id)) {
          found = [organization, ...found];
          control.options = found.map((o) => ({ value: o.id, label: o.name, description: o.origin.domain }));
        }
        control.value = organization.id;
      } else {
        control.value = "";
      }
    },
  };
}
