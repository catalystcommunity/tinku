import { For, Show, createResource, createSignal, type JSX } from "solid-js";
import type { Peer, PeerStatus, PublishSetting } from "~/gen/types.gen";
import { useI18n } from "~/i18n";
import { api } from "~/lib/api";
import { ErrorAlert, StatusMessage } from "~/components/Alert";
import { CheckField, Field } from "~/components/Field";

/**
 * Who this instance federates with.
 *
 * Two statuses per peer, shown as two independent controls, because they
 * are two decisions: accepting what somebody sends has never implied
 * publishing to them. The screen would be smaller with one toggle and it
 * would be saying something false.
 */
export default function Federation(): JSX.Element {
  const { t, plural, dateTime } = useI18n();

  const [identity] = createResource(() => api.federation.federationIdentity({}));
  const [peers, { refetch }] = createResource(() => api.federation.listPeers({}));
  const [settings, { mutate: mutateSettings }] = createResource(() =>
    api.admin.getInstanceSettings({}),
  );
  const [saved, setSaved] = createSignal(false);
  const [volume] = createResource(() => api.federation.listOriginVolume({}));

  const [address, setAddress] = createSignal("");
  const [baseUrl, setBaseUrl] = createSignal("");
  const [note, setNote] = createSignal("");
  const [error, setError] = createSignal<unknown>();
  const [busy, setBusy] = createSignal(false);

  const act = async (run: () => Promise<unknown>) => {
    setBusy(true);
    setError(undefined);
    // Clearing the "saved" line here is what keeps it from outliving the
    // save it describes: without this it is set once and never unset, so a
    // later attempt that FAILS renders its error alert underneath a message
    // still claiming the settings were saved.
    setSaved(false);
    try {
      await run();
      await refetch();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  const addPeer = (e: SubmitEvent) => {
    e.preventDefault();
    void act(async () => {
      await api.federation.addPeer({ address: address(), baseUrl: baseUrl(), note: note() });
      setAddress("");
      setBaseUrl("");
      setNote("");
    });
  };

  const setStatus = (peer: Peer, direction: "inbound" | "outbound", status: PeerStatus) =>
    void act(() =>
      api.federation.setPeerStatus({
        peerId: peer.id,
        ...(direction === "inbound" ? { inboundStatus: status } : { outboundStatus: status }),
      }),
    );

  return (
    <>
      <h1>{t("federation.title")}</h1>

      {/* `enabled` is the server's answer, and it is the one to read: an
          instance that does not federate still answers this op, with an
          empty address. Guarding on the object alone renders "This instance
          is " with a blank where its name should be, and then the whole
          peer UI beneath it. */}
      <Show
        when={identity()?.enabled ? identity() : undefined}
        fallback={<p class="card-meta">{t("federation.identityUnset")}</p>}
      >
        {(who) => (
          <>
            <p class="card-meta">{t("federation.identity", { address: who().address })}</p>
            <p class="card-meta">{t("federation.algorithm", { algorithm: who().algorithm })}</p>
            {/*
              An unsigned scheme is stated where an operator will see it, not
              only in a log line at boot. A page that looks the same whether
              or not deliveries are authenticated is a page that hides the
              thing most worth knowing.
            */}
            <Show when={who().algorithm.startsWith("dev-")}>
              <p class="alert alert-error" role="alert">
                {t("federation.unsignedWarning")}
              </p>
            </Show>
          </>
        )}
      </Show>

      <ErrorAlert error={error()} />

      {/*
        The instance-wide settings, above the peers, because they are what
        the per-peer controls sit inside: a peer with no allowance of its
        own uses the number set here.
      */}
      <section>
        <h2>{t("settings.title")}</h2>
        <Show when={settings()} fallback={<p>{t("common.loading")}</p>}>
          {(current) => {
            const save = (change: Partial<ReturnType<typeof current>>) =>
              void act(async () => {
                mutateSettings(await api.admin.updateInstanceSettings(change));
                setSaved(true);
              });
            return (
              <form onSubmit={(e) => e.preventDefault()}>
                <Field label={t("settings.publishDefault")}>
                  {(control) => (
                    <select
                      {...control}
                      value={current().publishDefault}
                      onChange={(e) =>
                        save({ publishDefault: e.currentTarget.value as PublishSetting })
                      }
                    >
                      <option value="in">{t("publish.in")}</option>
                      <option value="out">{t("publish.out")}</option>
                    </select>
                  )}
                </Field>
                <CheckField
                  label={t("settings.organizationOverride")}
                  checked={current().organizationOverrideAllowed}
                  onChange={(v) => save({ organizationOverrideAllowed: v })}
                />
                <CheckField
                  label={t("settings.gatheringOverride")}
                  checked={current().gatheringOverrideAllowed}
                  onChange={(v) => save({ gatheringOverrideAllowed: v })}
                />
                <Field label={t("settings.retentionDays")} hint={t("settings.retentionHint")}>
                  {(control) => (
                    <input
                      {...control}
                      type="number"
                      min={0}
                      value={current().retentionDays}
                      onChange={(e) => save({ retentionDays: Number(e.currentTarget.value) })}
                    />
                  )}
                </Field>
                <Field label={t("settings.rateLimit")} hint={t("settings.rateLimitHint")}>
                  {(control) => (
                    <input
                      {...control}
                      type="number"
                      min={0}
                      value={current().peerRateLimitPerMinute}
                      onChange={(e) =>
                        save({ peerRateLimitPerMinute: Number(e.currentTarget.value) })
                      }
                    />
                  )}
                </Field>
                <Field
                  label={t("settings.originRateLimit")}
                  hint={t("settings.originRateLimitHint")}
                >
                  {(control) => (
                    <input
                      {...control}
                      type="number"
                      min={0}
                      value={current().originRateLimitPerMinute}
                      onChange={(e) =>
                        save({ originRateLimitPerMinute: Number(e.currentTarget.value) })
                      }
                    />
                  )}
                </Field>
                <Show when={saved()}>
                  <StatusMessage>{t("settings.saved")}</StatusMessage>
                </Show>
              </form>
            );
          }}
        </Show>
      </section>

      <section>
        <h2>{t("federation.peers")}</h2>
        <Show when={peers()} fallback={<p>{t("common.loading")}</p>}>
          {(list) => (
            <Show when={list().peers.length > 0} fallback={<p>{t("federation.noPeers")}</p>}>
              <ul class="cards">
                <For each={list().peers}>
                  {(peer) => (
                    <li class="card" classList={{ "card-locked": peer.suspended }}>
                      <h3>{peer.address}</h3>
                      <p class="card-meta">{peer.baseUrl}</p>
                      <Show when={peer.note}>
                        <p class="card-blurb">{peer.note}</p>
                      </Show>

                      <p class="card-meta">
                        {t("federation.inbound")}:{" "}
                        <span class="badge">{t(`federation.status.${peer.inboundStatus}`)}</span>{" "}
                        {t("federation.outbound")}:{" "}
                        <span class="badge">{t(`federation.status.${peer.outboundStatus}`)}</span>
                      </p>

                      <p class="card-meta">
                        {t("federation.rateLimit")}:{" "}
                        <Show
                          when={peer.rateLimitPerMinute != null}
                          fallback={t("federation.rateLimitInstance")}
                        >
                          {t("federation.rateLimitOwn", { count: peer.rateLimitPerMinute ?? 0 })}
                        </Show>
                        <Show when={peer.rateLimitedTotal > 0}>
                          {" "}
                          · {plural("federation.rateLimitedTotal", peer.rateLimitedTotal)}
                        </Show>
                      </p>
                      <p>
                        <label>
                          {t("federation.setRateLimit")}{" "}
                          <input
                            type="number"
                            min={0}
                            value={peer.rateLimitPerMinute ?? ""}
                            onChange={(e) =>
                              void act(() =>
                                api.federation.setPeerRateLimit({
                                  peerId: peer.id,
                                  // An empty field restores the instance
                                  // limit rather than setting zero, which
                                  // would mean "no limit at all".
                                  rateLimitPerMinute:
                                    e.currentTarget.value === ""
                                      ? undefined
                                      : Number(e.currentTarget.value),
                                }),
                              )
                            }
                          />
                        </label>
                      </p>
                      <p class="card-meta">
                        {plural("federation.queued", peer.pendingDeliveries)}
                        <Show when={peer.lastSuccessAt}>
                          {(when) => <> · {t("federation.lastSuccess", { when: dateTime(when()) })}</>}
                        </Show>
                      </p>

                      {/* A stopped peer says so in words, and says why. */}
                      <Show when={peer.suspended}>
                        <p class="alert alert-error" role="alert">
                          <strong>{t("federation.suspended")}</strong>
                          <Show when={peer.suspendedAt}>
                            {(when) => <> {t("federation.suspendedSince", { when: dateTime(when()) })}</>}
                          </Show>
                          <Show when={peer.lastFailureReason}>
                            {(reason) => <> {t("federation.lastFailure", { reason: reason() })}</>}
                          </Show>
                        </p>
                        <button
                          type="button"
                          aria-label={t("federation.resumePeer", { peer: peer.address })}
                          disabled={busy()}
                          onClick={() => void act(() => api.federation.resumePeer({ peerId: peer.id }))}
                        >
                          {t("federation.resume")}
                        </button>
                      </Show>

                      <p>
                        <button
                          type="button"
                          aria-label={t("federation.approveInbound", { peer: peer.address })}
                          disabled={busy() || peer.inboundStatus === "approved"}
                          onClick={() => setStatus(peer, "inbound", "approved")}
                        >
                          {t("federation.approveInbound", { peer: peer.address })}
                        </button>{" "}
                        <button
                          type="button"
                          class="danger"
                          aria-label={t("federation.blockInbound", { peer: peer.address })}
                          disabled={busy() || peer.inboundStatus === "blocked"}
                          onClick={() => setStatus(peer, "inbound", "blocked")}
                        >
                          {t("federation.blockInbound", { peer: peer.address })}
                        </button>
                      </p>
                      <p>
                        <button
                          type="button"
                          aria-label={t("federation.approveOutbound", { peer: peer.address })}
                          disabled={busy() || peer.outboundStatus === "approved"}
                          onClick={() => setStatus(peer, "outbound", "approved")}
                        >
                          {t("federation.approveOutbound", { peer: peer.address })}
                        </button>{" "}
                        <button
                          type="button"
                          class="danger"
                          aria-label={t("federation.revokeOutbound", { peer: peer.address })}
                          disabled={busy() || peer.outboundStatus === "none"}
                          onClick={() => setStatus(peer, "outbound", "none")}
                        >
                          {t("federation.revokeOutbound", { peer: peer.address })}
                        </button>
                      </p>

                      <button
                        type="button"
                        class="danger"
                        disabled={busy()}
                        aria-label={t("federation.removePeer", { peer: peer.address })}
                        onClick={() => {
                          if (!window.confirm(t("common.confirmDelete", { name: peer.address }))) return;
                          void act(() => api.federation.removePeer({ peerId: peer.id }));
                        }}
                      >
                        {t("federation.remove")}
                      </button>
                    </li>
                  )}
                </For>
              </ul>
            </Show>
          )}
        </Show>
      </section>

      {/*
        The rate limit is enforced on a PEER, and a peer carries many
        organizations. A throttled peer therefore does not say which origin
        caused it. This list does, busiest first.
      */}
      <section>
        <h2>{t("volume.title")}</h2>
        <p class="field-hint">{t("volume.hint")}</p>
        <Show when={volume()} fallback={<p>{t("common.loading")}</p>}>
          {(list) => (
            <Show when={list().origins.length > 0} fallback={<p>{t("volume.none")}</p>}>
              <ul class="cards">
                <For each={list().origins}>
                  {(origin) => (
                    <li class="card">
                      <h3>{origin.organizationName || t("volume.noOrganization")}</h3>
                      <p class="card-address">{origin.peerAddress}</p>
                      <p class="card-meta">
                        {plural("volume.held", origin.held)} ·{" "}
                        {plural("volume.acceptedTotal", origin.acceptedTotal)}
                      </p>
                      <p class="card-meta">
                        <Show
                          when={origin.effectiveRateLimitPerMinute > 0}
                          fallback={t("volume.thisMinuteNoLimit", {
                            count: origin.acceptedThisMinute,
                          })}
                        >
                          {t("volume.thisMinute", {
                            count: origin.acceptedThisMinute,
                            limit: origin.effectiveRateLimitPerMinute,
                          })}
                        </Show>
                        <Show when={origin.lastReceivedAt}>
                          {(when) => <> · {t("volume.lastReceived", { when: dateTime(when()) })}</>}
                        </Show>
                      </p>
                      {/* The peer's state, said here so this list stands on
                          its own rather than needing a cross-reference. */}
                      <Show when={origin.rateLimitedTotal > 0}>
                        <p class="card-meta">
                          {plural("volume.originLimited", origin.rateLimitedTotal)}
                        </p>
                      </Show>
                      <Show when={origin.peerRateLimitedTotal > 0}>
                        <p class="card-meta">
                          {plural("federation.rateLimitedTotal", origin.peerRateLimitedTotal)}
                        </p>
                      </Show>
                      {/* Throttle one organization without touching the
                          peer's others — the reason the limit exists at two
                          levels. An empty field restores the instance one. */}
                      <p>
                        <label>
                          {t("volume.setOriginLimit")}{" "}
                          <input
                            type="number"
                            min={0}
                            placeholder={String(origin.effectiveRateLimitPerMinute)}
                            value={origin.rateLimitPerMinute ?? ""}
                            onChange={(e) =>
                              void act(() =>
                                api.federation.setOriginRateLimit({
                                  peerId: origin.peerId,
                                  organizationName: origin.organizationName,
                                  rateLimitPerMinute:
                                    e.currentTarget.value === ""
                                      ? undefined
                                      : Number(e.currentTarget.value),
                                }),
                              )
                            }
                          />
                        </label>
                      </p>
                      <Show when={origin.peerSuspended}>
                        <p class="alert alert-error" role="alert">
                          {t("volume.peerSuspended")}
                        </p>
                      </Show>
                    </li>
                  )}
                </For>
              </ul>
            </Show>
          )}
        </Show>
      </section>

      <section>
        <h2>{t("federation.addPeer")}</h2>
        <form onSubmit={addPeer}>
          <Field
            label={t("federation.addressLabel")}
            hint={t("federation.addressHint")}
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
          <Field label={t("federation.baseUrlLabel")} required requiredText={t("common.required")}>
            {(control) => (
              <input
                {...control}
                type="url"
                value={baseUrl()}
                onInput={(e) => setBaseUrl(e.currentTarget.value)}
              />
            )}
          </Field>
          <Field label={t("federation.noteLabel")} optionalText={t("common.optional")}>
            {(control) => (
              <input
                {...control}
                type="text"
                value={note()}
                onInput={(e) => setNote(e.currentTarget.value)}
              />
            )}
          </Field>
          <button type="submit" disabled={busy() || !address().includes("@") || !baseUrl()}>
            {t("common.create")}
          </button>
        </form>
      </section>
    </>
  );
}
