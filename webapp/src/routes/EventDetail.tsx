import { A, useNavigate, useParams } from "@solidjs/router";
import { For, Show, createResource, createSignal, type JSX } from "solid-js";
import { useI18n } from "~/i18n";
import { api } from "~/lib/api";
import { useSession } from "~/lib/session";
import { ErrorAlert } from "~/components/Alert";
import { Field } from "~/components/Field";
import { safeUrl } from "~/lib/safeUrl";

/**
 * One event.
 *
 * The screen's shape changes once the event has started: the description is
 * gone (the server did not send it), attendance is closed, and the edit and
 * delete controls disappear. The reason is stated rather than left for the
 * reader to infer from missing controls — a screen that quietly loses half
 * its buttons reads as broken.
 */
export default function EventDetail(): JSX.Element {
  const { t, plural, dateTime } = useI18n();
  const params = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { user } = useSession();

  const [event, { mutate, refetch }] = createResource(
    () => params.id,
    (id) => api.event.getEvent({ id }),
  );
  const [attendees, { refetch: refetchAttendees }] = createResource(
    () => params.id,
    (eventId) => api.event.listAttendees({ eventId }),
  );
  const [roles, { refetch: refetchRoles }] = createResource(
    () => params.id,
    (eventId) => api.event.listEventRoles({ eventId }),
  );

  const [error, setError] = createSignal<unknown>();
  const [busy, setBusy] = createSignal(false);
  const [address, setAddress] = createSignal("");
  const [role, setRole] = createSignal<"organizer" | "presenter">("presenter");

  const attendance = async (attending: boolean) => {
    setBusy(true);
    setError(undefined);
    try {
      const updated = attending
        ? await api.event.attendEvent({ eventId: params.id })
        : await api.event.unattendEvent({ eventId: params.id });
      mutate(updated);
      await refetchAttendees();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  const assignRole = async (e: SubmitEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      const [handle, domain] = address().split("@");
      const found = await api.admin.findUser({ handle: handle ?? "", domain: domain ?? "" });
      await api.event.setEventRole({ eventId: params.id, userId: found.userId, role: role() });
      setAddress("");
      await refetchRoles();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    const current = event();
    if (!current) return;
    if (!window.confirm(t("common.confirmDelete", { name: current.title }))) return;
    setError(undefined);
    try {
      await api.event.deleteEvent({ id: params.id });
      // Navigate AWAY rather than refetch. Refetching an id that no longer
      // exists puts the resource into its errored state, and reading an
      // errored resource rethrows during render — with no ErrorBoundary
      // above this route, that is a blank page instead of a deletion.
      navigate(`/gatherings/${current.gatheringId}`);
    } catch (err) {
      setError(err);
    }
  };

  return (
    <Show when={event()} fallback={<p>{t("common.loading")}</p>}>
      {(e) => (
        <>
          <h1>{e().title}</h1>
          <p class="page-when">
            <time datetime={new Date(e().startsAt).toISOString()}>
              {dateTime(e().startsAt, e().timezone)}
            </time>
            {" – "}
            <time datetime={new Date(e().endsAt).toISOString()}>
              {dateTime(e().endsAt, e().timezone)}
            </time>{" "}
            <span class="card-tz">({e().timezone})</span>
          </p>

          {/* The lock is announced, not merely implied by what is missing. */}
          <Show when={e().locked}>
            <p class="alert alert-status" role="status">
              <strong>{t("event.started")}</strong> {t("event.startedExplanation")}
            </p>
          </Show>

          <Show
            when={e().description}
            fallback={
              <Show when={e().locked}>
                <p class="card-meta">{t("event.descriptionWithheld")}</p>
              </Show>
            }
          >
            {(description) => <p class="long-form">{description()}</p>}
          </Show>

          <p class="page-meta">
            <Show when={e().isOnline}>
              <span class="badge">{t("event.online")}</span>
            </Show>
            <Show when={e().isInPerson}>
              <span class="badge">{t("event.inPerson")}</span>
            </Show>
            <Show when={e().seriesId}>
              <span class="badge">{t("event.partOfSeries")}</span>
            </Show>
          </p>

          {/* Whoever runs the gathering typed this URL. It is rendered as a
              link only if it is one this app will vouch for — a browser runs
              `javascript:` out of an href on THIS origin, session cookie and
              all. */}
          <Show when={safeUrl(e().onlineUrl)}>
            {(url) => (
              <p>
                <a href={url()} rel="noreferrer noopener">
                  {t("event.onlineUrlLabel")}
                </a>
              </p>
            )}
          </Show>

          <Show when={e().location}>
            {(location) => (
              <address>
                <Show when={location().name}>
                  <span>{location().name}</span>
                  <br />
                </Show>
                <Show when={location().address}>
                  <span>{location().address}</span>
                  <br />
                </Show>
                <span>
                  {[location().locality, location().region, location().postalCode]
                    .filter(Boolean)
                    .join(", ")}
                </span>
                <Show when={location().country}>
                  <br />
                  <span>{location().country}</span>
                </Show>
              </address>
            )}
          </Show>

          <p class="page-actions">
            <A href={`/gatherings/${e().gatheringId}`}>{t("event.backToGathering")}</A>
          </p>

          <ErrorAlert error={error()} />

          <section>
            <h2>{t("event.attendeeList")}</h2>
            <p class="card-meta" role="status" aria-live="polite">
              {plural("event.attendees", e().attendeeCount)}
            </p>

            <Show when={user() && !e().locked}>
              <Show
                when={e().viewer.isMember}
                fallback={<p class="card-meta">{t("event.joinToAttend")}</p>}
              >
                <Show
                  when={e().viewerAttending}
                  fallback={
                    <button type="button" onClick={() => attendance(true)} disabled={busy()}>
                      {t("event.attend")}
                    </button>
                  }
                >
                  <button type="button" onClick={() => attendance(false)} disabled={busy()}>
                    {t("event.unattend")}
                  </button>
                </Show>
              </Show>
            </Show>

            <Show when={attendees()}>
              {(list) => (
                <ul class="roster">
                  <For each={list().attendees}>
                    {(a) => (
                      <li>
                        <span class="roster-name">{a.displayName || a.handle}</span>{" "}
                        <span class="card-address">
                          {a.handle}@{a.linkkeysDomain}
                        </span>
                      </li>
                    )}
                  </For>
                </ul>
              )}
            </Show>
          </section>

          <section>
            <h2>{t("roles.legend")}</h2>
            <p class="card-meta">{t("roles.presenterNote")}</p>
            <Show when={roles()}>
              {(list) => (
                <ul class="roster">
                  <For each={list().roles}>
                    {(r) => (
                      <li>
                        <span class="roster-name">{r.displayName || r.handle}</span>{" "}
                        <span class="badge">
                          {r.role === "organizer" ? t("roles.organizer") : t("roles.presenter")}
                        </span>
                      </li>
                    )}
                  </For>
                </ul>
              )}
            </Show>

            <Show when={e().viewer.canManageMembers}>
              <form onSubmit={assignRole}>
                <h3>{t("roles.assign")}</h3>
                <div class="field-row">
                  <Field
                    label={t("organization.addMemberAddress")}
                    hint={t("organization.addMemberAddressHint")}
                    required
                    requiredText={t("common.required")}
                  >
                    {(control) => (
                      <input
                        {...control}
                        type="text"
                        value={address()}
                        onInput={(ev) => setAddress(ev.currentTarget.value)}
                      />
                    )}
                  </Field>
                  <Field label={t("roles.legend")}>
                    {(control) => (
                      <select
                        {...control}
                        value={role()}
                        onChange={(ev) =>
                          setRole(ev.currentTarget.value as "organizer" | "presenter")
                        }
                      >
                        <option value="presenter">{t("roles.presenter")}</option>
                        <option value="organizer">{t("roles.organizer")}</option>
                      </select>
                    )}
                  </Field>
                </div>
                <div class="form-actions">
                  <button type="submit" disabled={busy() || !address().includes("@")}>
                    {t("organization.addMemberAction")}
                  </button>
                </div>
              </form>
            </Show>
          </section>

          <Show when={e().viewer.canDelete}>
            <section class="danger-zone">
              <p>{t("event.dangerNote")}</p>
              <button type="button" class="danger" onClick={remove}>
                {t("common.delete")}
              </button>
            </section>
          </Show>
        </>
      )}
    </Show>
  );
}
