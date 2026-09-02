import { For, Show, createEffect, createResource, createSignal, createUniqueId, type JSX } from "solid-js";
import type { Organization } from "~/gen/types.gen";
import { useI18n } from "~/i18n";
import { api } from "~/lib/api";

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
 * Built from a listbox rather than a <datalist>: a datalist gives no control
 * over what is announced, and the value a person picks has to be an id while
 * the thing they read is a name.
 */
export function OrganizationPicker(props: {
  label: string;
  hint?: string;
  /** The chosen organization, or undefined for none. */
  value?: Organization;
  onChange: (organization: Organization | undefined) => void;
  /** Only organizations the caller belongs to. For "start a gathering under". */
  mine?: boolean;
  placeholder?: string;
}): JSX.Element {
  const { t } = useI18n();
  const id = createUniqueId();
  const listId = `${id}-list`;

  const [query, setQuery] = createSignal("");
  const [open, setOpen] = createSignal(false);

  // The query is what the resource keys on, so typing refetches and clearing
  // the box goes back to the unfiltered list — which is the useful answer
  // for somebody who owns two organizations and remembers neither name.
  const [matches] = createResource(
    () => ({ query: query(), mine: props.mine ?? false }),
    ({ query: text, mine }) =>
      api.organization.listOrganizations({
        query: text,
        mine,
        page: { limit: 8, offset: 0 },
      }),
  );

  // When the value is set from outside — cleared after a submit, say — the
  // box follows it.
  createEffect(() => {
    if (!props.value) {
      return;
    }
    setQuery(props.value.name);
  });

  const choose = (organization: Organization) => {
    props.onChange(organization);
    setQuery(organization.name);
    setOpen(false);
  };

  const clear = () => {
    props.onChange(undefined);
    setQuery("");
    setOpen(true);
  };

  return (
    <div class="field picker">
      <label for={id}>{props.label}</label>
      <div class="picker__row">
        <input
          id={id}
          type="text"
          role="combobox"
          autocomplete="off"
          aria-expanded={open()}
          aria-controls={listId}
          aria-describedby={props.hint ? `${id}-hint` : undefined}
          placeholder={props.placeholder}
          value={query()}
          onInput={(e) => {
            setQuery(e.currentTarget.value);
            setOpen(true);
            // Typing after a choice means the choice is being changed, so
            // the old one must not survive as a hidden selection.
            if (props.value) {
              props.onChange(undefined);
            }
          }}
          onFocus={() => setOpen(true)}
          // A blur that lands on an option must not close the list before
          // the click registers.
          onBlur={() => window.setTimeout(() => setOpen(false), 150)}
        />
        <Show when={props.value}>
          <button type="button" class="secondary compact" onClick={clear}>
            {t("picker.clear")}
          </button>
        </Show>
      </div>
      <Show when={props.hint}>
        <p class="field-hint" id={`${id}-hint`}>
          {props.hint}
        </p>
      </Show>

      <Show when={open() && !props.value}>
        <ul class="picker__list" id={listId} role="listbox">
          <Show
            when={(matches()?.organizations.length ?? 0) > 0}
            fallback={
              <li class="picker__empty" role="presentation">
                {matches.loading ? t("common.loading") : t("picker.noMatches")}
              </li>
            }
          >
            <For each={matches()?.organizations}>
              {(organization) => (
                <li role="option" aria-selected={false}>
                  <button type="button" class="picker__option" onClick={() => choose(organization)}>
                    <span class="picker__name">{organization.name}</span>
                    {/* The domain, always: two instances can hold the same
                        name and a directory shows both at once. */}
                    <span class="picker__domain">{organization.origin.domain}</span>
                  </button>
                </li>
              )}
            </For>
          </Show>
        </ul>
      </Show>
    </div>
  );
}
