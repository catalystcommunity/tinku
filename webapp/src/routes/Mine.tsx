import { For, Show, createResource, createSignal, type JSX } from "solid-js";
import { useI18n } from "~/i18n";
import { api } from "~/lib/api";
import { useSession } from "~/lib/session";
import { Calendar } from "~/components/Calendar";
import { EventCard, GatheringCard, OrganizationCard } from "~/components/Cards";
import { monthWindow } from "~/lib/month";

/** Everything that is the caller's: what they own or joined, and what they
 *  said they are attending. */
export default function Mine(): JSX.Element {
  const { t } = useI18n();
  const { user } = useSession();

  // Each resource keys on the signed-in user, so signing in or out refetches
  // rather than leaving somebody else's page on screen.
  const [gatherings] = createResource(
    () => user()?.id,
    () => api.gathering.listGatherings({ mine: true }),
  );
  const [organizations] = createResource(
    () => user()?.id,
    () => api.organization.listOrganizations({ mine: true }),
  );
  const [attending] = createResource(
    () => user()?.id,
    () => api.event.listEvents({ attendingOnly: true }),
  );

  // The calendar here has no toggles at all. This page IS the filter — it is
  // what is yours — so a control offering to widen it would answer a question
  // the reader did not come here to ask. Discover is where the whole
  // directory is.
  const today = new Date();
  const [month, setMonth] = createSignal({ year: today.getFullYear(), month: today.getMonth() });
  const [calendar] = createResource(
    () => ({ user: user()?.id, year: month().year, month: month().month }),
    async (key) => {
      if (!key.user) {
        return { events: [], total: 0 };
      }
      const { from, to } = monthWindow(key.year, key.month);
      return api.event.listEvents({
        attendingOnly: true,
        startsAfter: from,
        startsBefore: to,
        // A month that has been and gone is still a month somebody wants to
        // look at.
        includeStarted: true,
        page: { limit: 250, offset: 0 },
      });
    },
  );

  return (
    <>
      <h1>{t("mine.title")}</h1>
      <Show when={user()} fallback={<p>{t("mine.signInFirst")}</p>}>
        <section>
          <h2>{t("mine.calendar")}</h2>
          <p class="card-meta">{t("mine.calendarNote")}</p>
          <Calendar
            events={calendar()?.events ?? []}
            year={month().year}
            month={month().month}
            loading={calendar.loading}
            onMove={(year, m) => setMonth({ year, month: m })}
          />
        </section>

        <section>
          <h2>{t("gathering.mine")}</h2>
          <Show
            when={(gatherings()?.gatherings.length ?? 0) > 0}
            fallback={<p>{t("common.nothingHere")}</p>}
          >
            <ul class="cards">
              <For each={gatherings()?.gatherings}>{(g) => <GatheringCard gathering={g} />}</For>
            </ul>
          </Show>
        </section>

        <section>
          <h2>{t("event.mine")}</h2>
          <Show
            when={(attending()?.events.length ?? 0) > 0}
            fallback={<p>{t("common.nothingHere")}</p>}
          >
            <ul class="cards">
              <For each={attending()?.events}>{(e) => <EventCard event={e} />}</For>
            </ul>
          </Show>
        </section>

        <section>
          <h2>{t("organization.mine")}</h2>
          <Show when={(organizations()?.organizations.length ?? 0) > 0} fallback={<p>{t("common.nothingHere")}</p>}>
            <ul class="cards">
              <For each={organizations()?.organizations}>{(g) => <OrganizationCard organization={g} />}</For>
            </ul>
          </Show>
        </section>
      </Show>
    </>
  );
}
