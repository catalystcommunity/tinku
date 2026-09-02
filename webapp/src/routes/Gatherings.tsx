import { For, Show, createResource, createSignal, type JSX } from "solid-js";
import type { Organization } from "~/gen/types.gen";
import { useI18n } from "~/i18n";
import { api } from "~/lib/api";
import { useSession } from "~/lib/session";
import { ErrorAlert } from "~/components/Alert";
import { Field } from "~/components/Field";
import { GatheringCard } from "~/components/Cards";
import { OrganizationPicker } from "~/components/OrganizationPicker";
import { errorField } from "~/lib/messages";

/** Every gathering, plus the form to start one. */
export default function Gatherings(): JSX.Element {
  const { t } = useI18n();
  const { user } = useSession();

  const [gatherings, { refetch }] = createResource(() => api.gathering.listGatherings({}));
  const [name, setName] = createSignal("");
  const [blurb, setBlurb] = createSignal("");
  const [description, setDescription] = createSignal("");
  // Optional, and offered only to somebody who belongs to an organization:
  // a picker that is always empty is a question with no answers.
  const [owner, setOwner] = createSignal<Organization>();
  const [error, setError] = createSignal<unknown>();
  const [busy, setBusy] = createSignal(false);

  const create = async (e: SubmitEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      const organization = owner();
      await api.gathering.createGathering({
        name: name(),
        blurb: blurb(),
        description: description(),
        // Absent means the caller owns it themselves. Naming an
        // organization they do not own is refused by the server, which is
        // the only place that decision belongs.
        owner: organization ? { kind: "organization", id: organization.id } : undefined,
      });
      setName("");
      setBlurb("");
      setDescription("");
      setOwner(undefined);
      await refetch();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <h1>{t("gathering.plural")}</h1>

      <Show when={user()}>
        <section>
          <h2>{t("gathering.new")}</h2>
          <form onSubmit={create}>
            <Field
              label={t("gathering.nameLabel")}
              required
              requiredText={t("common.required")}
              error={errorField(error()) === "name" ? t("error.invalid") : undefined}
            >
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
            <Field label={t("gathering.blurbLabel")} optionalText={t("common.optional")}>
              {(control) => (
                <textarea
                  {...control}
                  rows={3}
                  value={blurb()}
                  onInput={(e) => setBlurb(e.currentTarget.value)}
                />
              )}
            </Field>
            <Field label={t("gathering.descriptionLabel")} optionalText={t("common.optional")}>
              {(control) => (
                <textarea
                  {...control}
                  rows={6}
                  value={description()}
                  onInput={(e) => setDescription(e.currentTarget.value)}
                />
              )}
            </Field>
            {/* `mine` keeps this to organizations the caller belongs to.
                Anybody can be shown every organization on the instance; only
                an owner of one can put a gathering under it, so offering the
                rest would be offering a refusal. */}
            <OrganizationPicker
              label={t("gathering.ownerLabel")}
              hint={t("gathering.ownerHint")}
              mine
              value={owner()}
              onChange={setOwner}
            />
            <div class="form-actions">
              <button type="submit" disabled={busy() || !name().trim()}>
                {busy() ? t("common.creating") : t("common.create")}
              </button>
            </div>
            <ErrorAlert error={error()} />
          </form>
        </section>
      </Show>

      <section>
        <Show when={gatherings()} fallback={<p>{t("common.loading")}</p>}>
          {(list) => (
            <Show when={list().gatherings.length > 0} fallback={<p>{t("gathering.emptyList")}</p>}>
              <ul class="cards">
                <For each={list().gatherings}>{(g) => <GatheringCard gathering={g} />}</For>
              </ul>
            </Show>
          )}
        </Show>
      </section>
    </>
  );
}
