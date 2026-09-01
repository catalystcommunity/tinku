import { For, Show, createResource, createSignal, type JSX } from "solid-js";
import { useI18n } from "~/i18n";
import { api } from "~/lib/api";
import { useSession } from "~/lib/session";
import { ErrorAlert } from "~/components/Alert";
import { Field } from "~/components/Field";
import { OrganizationCard } from "~/components/Cards";

/** Every organization, plus the form to start one. */
export default function Organizations(): JSX.Element {
  const { t, plural } = useI18n();
  const { user } = useSession();

  const [organizations, { refetch }] = createResource(() => api.organization.listOrganizations({}));
  const [name, setName] = createSignal("");
  const [blurb, setBlurb] = createSignal("");
  const [description, setDescription] = createSignal("");
  const [error, setError] = createSignal<unknown>();
  const [busy, setBusy] = createSignal(false);

  // The 300-word limit is a server rule. Counting here as well is not
  // duplication of the rule — it is telling somebody where they are before
  // they submit, which the server cannot do.
  const words = () => blurb().trim().split(/\s+/).filter(Boolean).length;

  const create = async (e: SubmitEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      await api.organization.createOrganization({ name: name(), blurb: blurb(), description: description() });
      setName("");
      setBlurb("");
      setDescription("");
      await refetch();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <h1>{t("organization.plural")}</h1>

      <Show when={user()}>
        <section>
          <h2>{t("organization.new")}</h2>
          <form onSubmit={create}>
            <Field label={t("organization.nameLabel")} required requiredText={t("common.required")}>
              {(control) => (
                <input
                  {...control}
                  type="text"
                  value={name()}
                  maxlength={120}
                  onInput={(e) => setName(e.currentTarget.value)}
                />
              )}
            </Field>
            <Field
              label={t("organization.blurbLabel")}
              hint={t("organization.blurbHint")}
              optionalText={t("common.optional")}
              error={words() > 300 ? plural("organization.blurbCount", words()) : undefined}
            >
              {(control) => (
                <textarea
                  {...control}
                  rows={3}
                  value={blurb()}
                  onInput={(e) => setBlurb(e.currentTarget.value)}
                />
              )}
            </Field>
            {/*
              The running count is polite, not assertive: it changes on
              every keystroke, and an assertive region would interrupt the
              typing it is describing.
            */}
            <p class="field-hint" role="status" aria-live="polite">
              {plural("organization.blurbCount", words())}
            </p>
            <Field
              label={t("organization.descriptionLabel")}
              hint={t("organization.descriptionHint")}
              optionalText={t("common.optional")}
            >
              {(control) => (
                <textarea
                  {...control}
                  rows={6}
                  value={description()}
                  onInput={(e) => setDescription(e.currentTarget.value)}
                />
              )}
            </Field>
            <button type="submit" disabled={busy() || !name().trim() || words() > 300}>
              {busy() ? t("common.creating") : t("common.create")}
            </button>
            <ErrorAlert error={error()} />
          </form>
        </section>
      </Show>

      <section>
        <Show when={organizations()} fallback={<p>{t("common.loading")}</p>}>
          {(list) => (
            <Show when={list().organizations.length > 0} fallback={<p>{t("organization.emptyList")}</p>}>
              <ul class="cards">
                <For each={list().organizations}>{(g) => <OrganizationCard organization={g} />}</For>
              </ul>
            </Show>
          )}
        </Show>
      </section>
    </>
  );
}
