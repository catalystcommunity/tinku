import { For, Show, createSignal, type JSX } from "solid-js";
import { createStore } from "solid-js/store";
import type { SearchRequest, SearchResults } from "~/gen/types.gen";
import { useI18n } from "~/i18n";
import { api } from "~/lib/api";
import { ErrorAlert } from "~/components/Alert";
import { CheckField, Field } from "~/components/Field";
import {
  EventCard,
  GatheringCard,
  OrganizationCard,
  RemoteEventCard,
  SeriesCard,
} from "~/components/Cards";

/**
 * One search across every kind. The form is a single <form> with three
 * fieldsets — text, place, time — so a keyboard user can tab through it in
 * the order the questions are asked, and a screen reader announces which
 * organization each control belongs to.
 */
export default function Discover(): JSX.Element {
  const { t, plural } = useI18n();

  const [form, setForm] = createStore({
    query: "",
    locality: "",
    region: "",
    from: "",
    to: "",
    onlineOnly: false,
    inPersonOnly: false,
    includeStarted: false,
  });
  const [results, setResults] = createSignal<SearchResults>();
  const [error, setError] = createSignal<unknown>();
  const [busy, setBusy] = createSignal(false);

  const totalFound = () => {
    const r = results();
    if (!r) return 0;
    return (
      r.organizations.length +
      r.gatherings.length +
      r.events.length +
      r.series.length +
      r.remoteEvents.length
    );
  };

  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      const req: SearchRequest = { includeStarted: form.includeStarted };
      if (form.query.trim()) req.query = form.query.trim();
      if (form.locality.trim() || form.region.trim()) {
        req.location = {};
        if (form.locality.trim()) req.location.locality = form.locality.trim();
        if (form.region.trim()) req.location.region = form.region.trim();
      }
      // A date input gives a local calendar date. Reading it as the start
      // of that day, and the "to" bound as the end of it, is what a person
      // means by "between the 1st and the 3rd".
      if (form.from) req.startsAfter = new Date(`${form.from}T00:00`);
      if (form.to) req.startsBefore = new Date(`${form.to}T23:59`);
      if (form.onlineOnly) req.onlineOnly = true;
      if (form.inPersonOnly) req.inPersonOnly = true;

      setResults(await api.search.search(req));
    } catch (err) {
      setError(err);
      setResults(undefined);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <h1>{t("discover.title")}</h1>

      <form onSubmit={submit}>
        <Field label={t("discover.searchLabel")}>
          {(control) => (
            <input
              {...control}
              type="search"
              value={form.query}
              placeholder={t("discover.searchPlaceholder")}
              onInput={(e) => setForm("query", e.currentTarget.value)}
            />
          )}
        </Field>

        <fieldset>
          <legend>{t("discover.placeLegend")}</legend>
          <Field label={t("discover.localityLabel")}>
            {(control) => (
              <input
                {...control}
                type="text"
                autocomplete="address-level2"
                value={form.locality}
                onInput={(e) => setForm("locality", e.currentTarget.value)}
              />
            )}
          </Field>
          <Field label={t("discover.regionLabel")}>
            {(control) => (
              <input
                {...control}
                type="text"
                autocomplete="address-level1"
                value={form.region}
                onInput={(e) => setForm("region", e.currentTarget.value)}
              />
            )}
          </Field>
        </fieldset>

        <fieldset>
          <legend>{t("discover.timeLegend")}</legend>
          <Field label={t("discover.fromLabel")}>
            {(control) => (
              <input
                {...control}
                type="date"
                value={form.from}
                onInput={(e) => setForm("from", e.currentTarget.value)}
              />
            )}
          </Field>
          <Field label={t("discover.toLabel")}>
            {(control) => (
              <input
                {...control}
                type="date"
                value={form.to}
                onInput={(e) => setForm("to", e.currentTarget.value)}
              />
            )}
          </Field>
          <CheckField
            label={t("discover.onlineOnly")}
            checked={form.onlineOnly}
            onChange={(v) => setForm("onlineOnly", v)}
          />
          <CheckField
            label={t("discover.inPersonOnly")}
            checked={form.inPersonOnly}
            onChange={(v) => setForm("inPersonOnly", v)}
          />
          <CheckField
            label={t("discover.includeStarted")}
            checked={form.includeStarted}
            onChange={(v) => setForm("includeStarted", v)}
          />
        </fieldset>

        <button type="submit" disabled={busy()}>
          {busy() ? t("common.loading") : t("discover.searchAction")}
        </button>
      </form>

      <ErrorAlert error={error()} />

      {/*
        The result count is announced rather than only shown: a search that
        replaces the page below the form is a change a sighted user sees at
        a glance and a screen-reader user is told nothing about.
      */}
      <section aria-label={t("discover.resultsLabel")}>
        <p class="visually-hidden" role="status" aria-live="polite">
          <Show when={results()}>{plural("discover.resultsAnnounced", totalFound())}</Show>
        </p>

        <Show when={results()}>
          {(found) => (
            <Show when={totalFound() > 0} fallback={<p>{t("discover.noResults")}</p>}>
              <Show when={found().gatherings.length > 0}>
                <h2>{t("gathering.plural")}</h2>
                <ul class="cards">
                  <For each={found().gatherings}>{(g) => <GatheringCard gathering={g} />}</For>
                </ul>
              </Show>
              <Show when={found().events.length > 0}>
                <h2>{t("event.plural")}</h2>
                <ul class="cards">
                  <For each={found().events}>{(e) => <EventCard event={e} />}</For>
                </ul>
              </Show>
              <Show when={found().remoteEvents.length > 0}>
                <h2>{t("federation.remoteEvents")}</h2>
                <ul class="cards">
                  <For each={found().remoteEvents}>{(e) => <RemoteEventCard event={e} />}</For>
                </ul>
              </Show>
              <Show when={found().series.length > 0}>
                <h2>{t("event.newSeries")}</h2>
                <ul class="cards">
                  <For each={found().series}>{(s) => <SeriesCard series={s} />}</For>
                </ul>
              </Show>
              <Show when={found().organizations.length > 0}>
                <h2>{t("organization.plural")}</h2>
                <ul class="cards">
                  <For each={found().organizations}>{(g) => <OrganizationCard organization={g} />}</For>
                </ul>
              </Show>
            </Show>
          )}
        </Show>
      </section>
    </>
  );
}
