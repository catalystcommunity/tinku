import { For, Show, createResource, createSignal, type JSX } from "solid-js";
import { useI18n } from "~/i18n";
import { api } from "~/lib/api";
import { useSession } from "~/lib/session";
import { ErrorAlert } from "~/components/Alert";
import { Field } from "~/components/Field";
import { GatheringCard } from "~/components/Cards";
import { errorField } from "~/lib/messages";

/** Every gathering, plus the form to start one. */
export default function Gatherings(): JSX.Element {
  const { t } = useI18n();
  const { user } = useSession();

  const [gatherings, { refetch }] = createResource(() => api.gathering.listGatherings({}));
  const [name, setName] = createSignal("");
  const [blurb, setBlurb] = createSignal("");
  const [description, setDescription] = createSignal("");
  const [error, setError] = createSignal<unknown>();
  const [busy, setBusy] = createSignal(false);

  const create = async (e: SubmitEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      await api.gathering.createGathering({
        name: name(),
        blurb: blurb(),
        description: description(),
      });
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
            <button type="submit" disabled={busy() || !name().trim()}>
              {busy() ? t("common.creating") : t("common.create")}
            </button>
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
