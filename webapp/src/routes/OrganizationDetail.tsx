import { useNavigate, useParams } from "@solidjs/router";
import { For, Show, createResource, createSignal, type JSX } from "solid-js";
import { useI18n } from "~/i18n";
import { api } from "~/lib/api";
import { ErrorAlert, StatusMessage } from "~/components/Alert";
import { Field } from "~/components/Field";
import { OriginBadge } from "~/components/OriginBadge";
import { PublishControl } from "~/components/PublishControl";

/** One organization: its blurb, its roster, and the roster controls its owners get. */
export default function OrganizationDetail(): JSX.Element {
  const { t, plural, date } = useI18n();
  const params = useParams<{ id: string }>();
  const navigate = useNavigate();

  const [organization, { refetch: refetchOrganization }] = createResource(
    () => params.id,
    (id) => api.organization.getOrganization({ id }),
  );
  const [members, { refetch: refetchMembers }] = createResource(
    () => params.id,
    (organizationId) => api.organization.listOrganizationMembers({ organizationId }),
  );

  const [address, setAddress] = createSignal("");
  const [role, setRole] = createSignal<"owner" | "member">("member");
  const [error, setError] = createSignal<unknown>();
  const [status, setStatus] = createSignal("");
  const [busy, setBusy] = createSignal(false);

  const addMember = async (e: SubmitEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      // Somebody is addressed as handle@domain, so that is what the form
      // asks for; resolving it to an id is the server's job, not a thing a
      // person should have to do first.
      const [handle, domain] = address().split("@");
      const found = await api.admin.findUser({ handle: handle ?? "", domain: domain ?? "" });
      await api.organization.setOrganizationMember({
        organizationId: params.id,
        userId: found.userId,
        role: role(),
      });
      setAddress("");
      await Promise.all([refetchOrganization(), refetchMembers()]);
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  const removeMember = async (userId: string) => {
    setError(undefined);
    try {
      await api.organization.removeOrganizationMember({ organizationId: params.id, userId });
      await Promise.all([refetchOrganization(), refetchMembers()]);
    } catch (err) {
      setError(err);
    }
  };

  const remove = async () => {
    const current = organization();
    if (!current) return;
    if (!window.confirm(t("common.confirmDelete", { name: current.name }))) return;
    setError(undefined);
    try {
      await api.organization.deleteOrganization({ id: params.id });
      setStatus(t("common.loading"));
      navigate("/organizations");
    } catch (err) {
      setError(err);
    }
  };

  return (
    <Show when={organization()} fallback={<p>{t("common.loading")}</p>}>
      {(g) => (
        <>
          <h1>{g().name}</h1>
          <p>
            <OriginBadge origin={g().origin} local={g().slug} />
          </p>
          {/* An external record that shares a local name is the case worth
              spelling out, not merely badging. */}
          <Show when={g().origin.isExternal}>
            <p class="alert alert-status" role="status">
              {t("origin.externalWarning", { domain: g().origin.domain })}
            </p>
          </Show>
          <p class="card-meta">
            {plural("organization.members", g().memberCount)} · {plural("organization.owners", g().ownerCount)}
          </p>

          <Show when={g().blurb}>
            <p>{g().blurb}</p>
          </Show>
          <Show when={g().description}>
            <p class="long-form">{g().description}</p>
          </Show>

          <Show when={g().viewer.canEdit}>
            <PublishControl
              value={g().publishEvents}
              canEdit={g().viewer.canEdit}
              onChange={(setting) =>
                void (async () => {
                  setError(undefined);
                  try {
                    await api.organization.updateOrganization({
                      id: params.id,
                      publishEvents: setting,
                    });
                    await refetchOrganization();
                  } catch (err) {
                    setError(err);
                  }
                })()
              }
            />
          </Show>

          <ErrorAlert error={error()} />
          <Show when={status()}>{(s) => <StatusMessage>{s()}</StatusMessage>}</Show>

          <section>
            <h2>{t("organization.roster")}</h2>
            <Show when={members()} fallback={<p>{t("common.loading")}</p>}>
              {(roster) => (
                <ul class="roster">
                  <For each={roster().members}>
                    {(m) => (
                      <li>
                        <span class="roster-name">{m.displayName || m.handle}</span>{" "}
                        <span class="card-address">
                          {m.handle}@{m.linkkeysDomain}
                        </span>{" "}
                        <span class="badge">
                          {m.role === "owner" ? t("roles.owner") : t("roles.member")}
                        </span>{" "}
                        <span class="card-meta">{date(m.joinedAt)}</span>
                        <Show when={g().viewer.canManageMembers}>
                          {/*
                            The accessible name says who is being removed.
                            A row of buttons all named "Remove" is
                            unusable when the rows are read out of context.
                          */}
                          <button
                            type="button"
                            class="link-button"
                            aria-label={t("organization.removeMember", {
                              name: m.displayName || m.handle,
                            })}
                            onClick={() => removeMember(m.userId)}
                          >
                            {t("common.delete")}
                          </button>
                        </Show>
                      </li>
                    )}
                  </For>
                </ul>
              )}
            </Show>

            <Show when={g().viewer.canManageMembers}>
              <form onSubmit={addMember}>
                <fieldset>
                  <legend>{t("organization.addMemberLegend")}</legend>
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
                        onInput={(e) => setAddress(e.currentTarget.value)}
                      />
                    )}
                  </Field>
                  <Field label={t("roles.legend")}>
                    {(control) => (
                      <select
                        {...control}
                        value={role()}
                        onChange={(e) => setRole(e.currentTarget.value as "owner" | "member")}
                      >
                        <option value="member">{t("roles.member")}</option>
                        <option value="owner">{t("roles.owner")}</option>
                      </select>
                    )}
                  </Field>
                  <button type="submit" disabled={busy() || !address().includes("@")}>
                    {t("organization.addMemberAction")}
                  </button>
                </fieldset>
              </form>
            </Show>
          </section>

          {/*
            Deleting an organization is an administrator's power alone. The button
            is shown to an admin and the reason is shown to everybody else,
            rather than the control simply not existing — an owner who
            wonders why they cannot delete their own organization deserves the
            answer.
          */}
          <section>
            <Show
              when={g().viewer.canDelete}
              fallback={<p class="card-meta">{t("organization.deleteNeedsAdmin")}</p>}
            >
              <button type="button" class="danger" onClick={remove}>
                {t("common.delete")}
              </button>
            </Show>
          </section>
        </>
      )}
    </Show>
  );
}
