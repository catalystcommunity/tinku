import { For, Show, createResource, createSignal, type JSX } from "solid-js";
import { A } from "@solidjs/router";
import type { GatheringOffer, Organization } from "~/gen/types.gen";
import { useI18n } from "~/i18n";
import { api } from "~/lib/api";
import { ErrorAlert, StatusMessage } from "~/components/Alert";
import { Field } from "~/components/Field";
import { OrganizationPicker } from "~/components/OrganizationPicker";

/**
 * The offering side: a gathering's owner hands it to an organization.
 *
 * The note under the heading says what accepting will do, because an owner
 * about to give somebody else power over their gathering should not have to
 * find that out afterwards: the organization becomes an owner ALONGSIDE
 * them, the gathering's owners join its roster, and the gathering's members
 * are not touched.
 */
export function OfferGathering(props: { gatheringId: string }): JSX.Element {
  const { t } = useI18n();

  const [offers, { refetch }] = createResource(
    () => props.gatheringId,
    (gatheringId) => api.gathering.listGatheringOffers({ gatheringId }),
  );

  const [organization, setOrganization] = createSignal<Organization>();
  const [note, setNote] = createSignal("");
  const [error, setError] = createSignal<unknown>();
  const [status, setStatus] = createSignal("");
  const [busy, setBusy] = createSignal(false);

  const act = async (run: () => Promise<unknown>, done: string) => {
    setBusy(true);
    setError(undefined);
    setStatus("");
    try {
      await run();
      await refetch();
      setStatus(done);
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  const send = (e: SubmitEvent) => {
    e.preventDefault();
    const target = organization();
    if (!target) return;
    void act(async () => {
      await api.gathering.offerGathering({
        gatheringId: props.gatheringId,
        organizationId: target.id,
        note: note().trim(),
      });
      setOrganization(undefined);
      setNote("");
    }, t("offer.sent"));
  };

  return (
    <section>
      <h2>{t("offer.heading")}</h2>
      <p class="card-meta">{t("offer.note")}</p>

      <ErrorAlert error={error()} />
      <Show when={status()}>{(s) => <StatusMessage>{s()}</StatusMessage>}</Show>

      <Show when={(offers()?.offers.length ?? 0) > 0}>
        <ul class="roster">
          <For each={offers()?.offers}>
            {(offer) => (
              <li>
                <span class="roster-name">
                  {t("offer.pending", { organization: offer.organizationName })}
                </span>
                <Show when={offer.viewer.canDelete}>
                  <button
                    type="button"
                    class="secondary compact"
                    disabled={busy()}
                    onClick={() =>
                      void act(
                        () => api.gathering.withdrawGatheringOffer({ offerId: offer.id }),
                        t("offer.withdrawn"),
                      )
                    }
                  >
                    {t("offer.withdraw")}
                  </button>
                </Show>
              </li>
            )}
          </For>
        </ul>
      </Show>

      <form onSubmit={send}>
        <OrganizationPicker
          label={t("offer.organizationLabel")}
          value={organization()}
          onChange={setOrganization}
        />
        <Field label={t("offer.messageLabel")} optionalText={t("common.optional")}>
          {(control) => (
            <textarea
              {...control}
              rows={2}
              value={note()}
              onInput={(e) => setNote(e.currentTarget.value)}
            />
          )}
        </Field>
        <div class="form-actions">
          <button type="submit" disabled={busy() || !organization()}>
            {busy() ? t("common.saving") : t("offer.send")}
          </button>
        </div>
      </form>
    </section>
  );
}

/**
 * The receiving side: what has been offered to this organization.
 *
 * Accept and decline are both here, and neither is the default: an
 * organization taking on a gathering is taking on something it will be
 * answerable for, so the choice is deliberate on both sides.
 */
export function OfferInbox(props: { organizationId: string; onAccepted?: () => void }): JSX.Element {
  const { t, dateTime } = useI18n();

  const [offers, { refetch }] = createResource(
    () => props.organizationId,
    (organizationId) => api.gathering.listGatheringOffers({ organizationId }),
  );

  const [error, setError] = createSignal<unknown>();
  const [status, setStatus] = createSignal("");
  const [busy, setBusy] = createSignal(false);

  const answer = (offer: GatheringOffer, accept: boolean) => {
    setBusy(true);
    setError(undefined);
    setStatus("");
    void (async () => {
      try {
        await api.gathering.respondToGatheringOffer({ offerId: offer.id, accept });
        await refetch();
        setStatus(accept ? t("offer.accepted") : t("offer.declined"));
        if (accept) props.onAccepted?.();
      } catch (err) {
        setError(err);
      } finally {
        setBusy(false);
      }
    })();
  };

  return (
    <section>
      <h2>{t("offer.inbox")}</h2>

      <ErrorAlert error={error()} />
      <Show when={status()}>{(s) => <StatusMessage>{s()}</StatusMessage>}</Show>

      <Show
        when={(offers()?.offers.length ?? 0) > 0}
        fallback={<p class="empty">{t("offer.inboxEmpty")}</p>}
      >
        <ul class="roster">
          <For each={offers()?.offers}>
            {(offer) => (
              <li>
                <span class="roster-name">
                  <A href={`/gatherings/${offer.gatheringId}`}>{offer.gatheringName}</A>
                </span>
                {/* Who offered it, with their domain: a name from another
                    instance is not the same person as the same name here. */}
                <span class="card-address">
                  {t("offer.from", {
                    who: `${offer.offeredBy.handle}@${offer.offeredBy.linkkeysDomain}`,
                  })}
                </span>
                <span class="card-meta">{dateTime(offer.createdAt)}</span>
                <Show when={offer.note}>
                  <span class="card-meta">{offer.note}</span>
                </Show>
                <Show when={offer.viewer.canEdit}>
                  <button
                    type="button"
                    class="compact"
                    disabled={busy()}
                    aria-label={t("offer.accept") + ": " + offer.gatheringName}
                    onClick={() => answer(offer, true)}
                  >
                    {t("offer.accept")}
                  </button>
                  <button
                    type="button"
                    class="secondary compact"
                    disabled={busy()}
                    aria-label={t("offer.decline") + ": " + offer.gatheringName}
                    onClick={() => answer(offer, false)}
                  >
                    {t("offer.decline")}
                  </button>
                </Show>
              </li>
            )}
          </For>
        </ul>
      </Show>
    </section>
  );
}

/**
 * The administrator's one-sided path: put a loose gathering under an
 * organization with no offer and no acceptance.
 *
 * Separate from the offer panel on purpose. It is a different power with a
 * different justification, and a screen that ran the two together would
 * invite an administrator to use the blunt one by habit.
 */
export function AdoptGathering(props: { gatheringId: string; onAdopted?: () => void }): JSX.Element {
  const { t } = useI18n();
  const [organization, setOrganization] = createSignal<Organization>();
  const [error, setError] = createSignal<unknown>();
  const [status, setStatus] = createSignal("");
  const [busy, setBusy] = createSignal(false);

  const adopt = (e: SubmitEvent) => {
    e.preventDefault();
    const target = organization();
    if (!target) return;
    setBusy(true);
    setError(undefined);
    setStatus("");
    void (async () => {
      try {
        await api.gathering.adoptGathering({
          gatheringId: props.gatheringId,
          organizationId: target.id,
        });
        setOrganization(undefined);
        setStatus(t("adopt.done"));
        props.onAdopted?.();
      } catch (err) {
        setError(err);
      } finally {
        setBusy(false);
      }
    })();
  };

  return (
    <section>
      <h2>{t("adopt.heading")}</h2>
      <p class="card-meta">{t("adopt.note")}</p>

      <ErrorAlert error={error()} />
      <Show when={status()}>{(s) => <StatusMessage>{s()}</StatusMessage>}</Show>

      <form onSubmit={adopt}>
        <OrganizationPicker
          label={t("offer.organizationLabel")}
          value={organization()}
          onChange={setOrganization}
        />
        <div class="form-actions">
          <button type="submit" disabled={busy() || !organization()}>
            {busy() ? t("common.saving") : t("adopt.action")}
          </button>
        </div>
      </form>
    </section>
  );
}
