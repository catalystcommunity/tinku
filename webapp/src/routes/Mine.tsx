import { For, Show, createResource, type JSX } from "solid-js";
import { useI18n } from "~/i18n";
import { api } from "~/lib/api";
import { useSession } from "~/lib/session";
import { EventCard, GatheringCard, OrganizationCard } from "~/components/Cards";

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

  return (
    <>
      <h1>{t("mine.title")}</h1>
      <Show when={user()} fallback={<p>{t("mine.signInFirst")}</p>}>
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
