import { For, Show, createResource, createSignal, type JSX } from "solid-js";
import { createStore } from "solid-js/store";
import type { Webhook, WebhookOwnerKind, WebhookScope } from "~/gen/types.gen";
import { useI18n } from "~/i18n";
import { api } from "~/lib/api";
import { ErrorAlert, StatusMessage } from "~/components/Alert";
import { CheckField, Field } from "~/components/Field";
import { Select } from "~/components/Select";

/**
 * The webhooks on one organization or one gathering.
 *
 * Rendered only for somebody who may manage them — the server refuses
 * anybody else, and a panel that always failed would be furniture.
 *
 * Two things here are not styling:
 *
 *   * The signing secret is shown ONCE, immediately after the webhook is
 *     made, and the panel says so. The server cannot show it again.
 *
 *   * Sending the whole record is opt-in behind an acknowledgement. The
 *     warning is not a scold: it is the owner's own information, and they
 *     are the right person to decide. What they need is to know what will
 *     leave, before it leaves.
 */
export function Webhooks(props: {
  ownerKind: WebhookOwnerKind;
  ownerId: string;
}): JSX.Element {
  const { t, dateTime } = useI18n();

  const [webhooks, { refetch }] = createResource(
    () => ({ kind: props.ownerKind, id: props.ownerId }),
    ({ kind, id }) => api.webhook.listWebhooks({ ownerKind: kind, ownerId: id }),
  );

  const [form, setForm] = createStore({
    url: "",
    note: "",
    scope: "all" as WebhookScope,
    includeDetails: false,
    accepted: false,
  });
  const [secret, setSecret] = createSignal("");
  const [error, setError] = createSignal<unknown>();
  const [status, setStatus] = createSignal("");
  const [busy, setBusy] = createSignal(false);

  const limit = () => Number(webhooks()?.limit ?? 5);
  const count = () => webhooks()?.webhooks.length ?? 0;
  const full = () => count() >= limit();

  // The acknowledgement gates the SWITCH, not the form: somebody who never
  // asks for details never sees a checkbox they have to tick to proceed.
  const blocked = () => form.includeDetails && !form.accepted;

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

  const add = (e: SubmitEvent) => {
    e.preventDefault();
    void act(async () => {
      const created = await api.webhook.createWebhook({
        ownerKind: props.ownerKind,
        ownerId: props.ownerId,
        url: form.url.trim(),
        note: form.note.trim(),
        scope: form.scope,
        includeDetails: form.includeDetails,
      });
      setSecret(created.secret);
      setForm({ url: "", note: "", scope: "all", includeDetails: false, accepted: false });
    }, t("webhook.added"));
  };

  const setActive = (hook: Webhook, active: boolean) =>
    void act(() => api.webhook.updateWebhook({ id: hook.id, active }), t("common.save"));

  const remove = (hook: Webhook) =>
    void act(() => api.webhook.deleteWebhook({ id: hook.id }), t("webhook.removed"));

  return (
    <section>
      <h2>{t("webhook.heading")}</h2>
      <p class="card-meta">{t("webhook.note")}</p>
      <p class="card-meta">{t("webhook.count", { count: count(), limit: limit() })}</p>

      <ErrorAlert error={error()} />
      <Show when={status()}>{(s) => <StatusMessage>{s()}</StatusMessage>}</Show>

      {/*
        The secret, once. It is in a live region because it appears without
        the reader moving anywhere, and it stays until they dismiss it —
        a panel that cleared it on the next render would lose the one copy
        that exists.
      */}
      <Show when={secret()}>
        <div class="alert alert-status" role="status">
          <p>
            <strong>{t("webhook.secretLabel")}</strong>
          </p>
          <p>
            <code class="secret">{secret()}</code>
          </p>
          <p>{t("webhook.secretNote")}</p>
          <button type="button" class="secondary compact" onClick={() => setSecret("")}>
            {t("webhook.secretDone")}
          </button>
        </div>
      </Show>

      <Show when={count() > 0} fallback={<p class="empty">{t("webhook.empty")}</p>}>
        <ul class="roster">
          <For each={webhooks()?.webhooks}>
            {(hook) => (
              <li>
                <span class="roster-name">{hook.url}</span>
                <Show when={hook.note}>
                  <span class="card-meta">{hook.note}</span>
                </Show>
                <span class="badge">{t(`webhook.scope.${hook.scope}`)}</span>
                <Show when={hook.includeDetails}>
                  <span class="badge badge-strong">{t("webhook.sendsDetails")}</span>
                </Show>
                <Show when={!hook.active}>
                  <span class="badge">{t("webhook.paused")}</span>
                </Show>
                <Show when={hook.failureCount > 0}>
                  <span class="badge">
                    {t("webhook.failing", { count: Number(hook.failureCount) })}
                  </span>
                </Show>
                <span class="card-meta">
                  <Show when={hook.lastAttemptAt} fallback={t("webhook.neverSent")}>
                    {(when) => dateTime(when())}
                  </Show>
                </span>
                <button
                  type="button"
                  class="secondary compact"
                  disabled={busy()}
                  onClick={() => setActive(hook, !hook.active)}
                >
                  {hook.active ? t("webhook.pause") : t("webhook.resume")}
                </button>
                <button
                  type="button"
                  class="danger compact"
                  disabled={busy()}
                  aria-label={t("webhook.removeLabel", { url: hook.url })}
                  onClick={() => remove(hook)}
                >
                  {t("webhook.remove")}
                </button>
              </li>
            )}
          </For>
        </ul>
      </Show>

      <Show when={!full()} fallback={<p class="card-meta">{t("webhook.full")}</p>}>
        <form onSubmit={add}>
          <h3>{t("webhook.add")}</h3>
          <div class="field-row">
            <Field
              label={t("webhook.urlLabel")}
              hint={t("webhook.urlHint")}
              required
              requiredText={t("common.required")}
            >
              {(control) => (
                <input
                  {...control}
                  type="url"
                  value={form.url}
                  onInput={(e) => setForm("url", e.currentTarget.value)}
                />
              )}
            </Field>
            <Field label={t("webhook.noteLabel")} optionalText={t("common.optional")}>
              {(control) => (
                <input
                  {...control}
                  type="text"
                  value={form.note}
                  onInput={(e) => setForm("note", e.currentTarget.value)}
                />
              )}
            </Field>
          </div>

          <Field label={t("webhook.scopeLabel")}>
            {(control) => (
              <Select
                control={control}
                value={form.scope}
                options={["all", "structure_only"] as WebhookScope[]}
                label={(scope) => t(`webhook.scope.${scope}`)}
                onChange={(scope) => setForm("scope", scope)}
              />
            )}
          </Field>

          <CheckField
            label={t("webhook.detailsLabel")}
            checked={form.includeDetails}
            onChange={(v) => setForm({ includeDetails: v, accepted: false })}
          />
          {/*
            The warning appears with the switch and not before it: a panel
            that shouted about leaking data at somebody setting up a plain
            notification would be noise, and noise is what gets clicked
            through.
          */}
          <Show when={form.includeDetails}>
            <div class="alert alert-error" role="alert">
              <p>{t("webhook.detailsWarning")}</p>
              <CheckField
                label={t("webhook.detailsAccept")}
                checked={form.accepted}
                onChange={(v) => setForm("accepted", v)}
              />
            </div>
          </Show>

          <div class="form-actions">
            <button type="submit" disabled={busy() || !form.url.trim() || blocked()}>
              {busy() ? t("common.saving") : t("webhook.add")}
            </button>
          </div>
        </form>
      </Show>
    </section>
  );
}
