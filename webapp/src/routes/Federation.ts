import type {
  FederationIdentity,
  InstanceSettings,
  OriginVolume,
  OriginVolumeList,
  Peer,
  PeerList,
  PeerStatus,
  PublishSetting,
  UpdateInstanceSettingsRequest,
} from "~/gen/types.gen";
import { dateTime, plural, t } from "~/i18n";
import { api } from "~/lib/api";
import { TinkuTransportError } from "~/lib/errors";
import { Status } from "~/transport/csil/conventions";
import { errorAlert, errorAlertFor, statusMessage } from "~/components/Alert";
import {
  button,
  checkField,
  numberField,
  selectField,
  setDisabled,
  textField,
  type PushButton,
  type SpinBox,
} from "~/components/Field";
import { fill, h } from "~/ui/dom";
import { Resource, TinkuElement, define } from "~/ui/element";

/**
 * Federation is off by default, and an instance with it off does not
 * register the service at all — so the first call here comes back "unknown
 * service or op". That is an ANSWER, not a failure: this screen exists to
 * say whether the instance federates. Letting it reach the crash panel
 * turned "off" into a crash page, which told an administrator nothing about
 * the setting they came to look at.
 */
function offWhenAbsent(err: unknown): null {
  if (err instanceof TinkuTransportError && err.status === Status.UnknownServiceOrOp) {
    return null;
  }
  throw err;
}

/**
 * A number from a spin box that is committed on `change` only: one save per
 * edit, not one per keystroke. An empty box is `undefined`, which restores
 * the level above rather than setting zero (zero means "no limit at all").
 */
function limitBox(label: string, value: number | null | undefined, placeholder: string | undefined, commit: (v: number | undefined) => void): { el: HTMLElement; control: SpinBox } {
  const field = numberField({ label, value: value ?? null, min: 0, onChange: () => {} });
  if (placeholder !== undefined) field.control.setAttribute("placeholder", placeholder);
  field.control.addEventListener("change", (e) => {
    if (e.target !== field.control) return;
    const v = field.control.value;
    commit(v === null ? undefined : Number(v));
  });
  return { el: field.el, control: field.control };
}

/**
 * Who this instance federates with.
 *
 * Two statuses per peer, shown as two independent controls, because they
 * are two decisions: accepting what somebody sends has never implied
 * publishing to them. The screen would be smaller with one toggle and it
 * would be saying something false.
 */
export class FederationPage extends TinkuElement {
  routeId?: string;

  #identity = new Resource<FederationIdentity | null>(this, () =>
    api.federation.federationIdentity({}).catch(offWhenAbsent),
  );
  #peers = new Resource<PeerList | null>(this, () => api.federation.listPeers({}).catch(offWhenAbsent));
  #settings = new Resource<InstanceSettings>(this, () => api.admin.getInstanceSettings({}));
  #volume = new Resource<OriginVolumeList | null>(this, () =>
    api.federation.listOriginVolume({}).catch(offWhenAbsent),
  );

  #address = "";
  #baseUrl = "";
  #note = "";
  #busy = false;
  #alert = errorAlert();
  #saved = statusMessage();

  #offNote!: HTMLElement;
  #identityBox!: HTMLElement;
  #identityShows: unknown = null;
  #settingsBox!: HTMLElement;
  #settingsForm!: HTMLFormElement;
  #settingsLoading!: HTMLElement;
  #settingsShows: unknown = null;
  #publishDefault!: ReturnType<typeof selectField<PublishSetting>>;
  #orgOverride!: ReturnType<typeof checkField>;
  #gatheringOverride!: ReturnType<typeof checkField>;
  #retention!: { control: SpinBox };
  #rateLimit!: { control: SpinBox };
  #originRateLimit!: { control: SpinBox };
  #peersSection!: HTMLElement;
  #peerList!: HTMLElement;
  #peersShows: unknown = null;
  #peerButtons: { el: PushButton; disabled: () => boolean }[] = [];
  #volumeSection!: HTMLElement;
  #volumeList!: HTMLElement;
  #volumeShows: unknown = null;
  #addSection!: HTMLElement;
  #addressField!: ReturnType<typeof textField>;
  #baseUrlField!: ReturnType<typeof textField>;
  #noteField!: ReturnType<typeof textField>;
  #submit!: PushButton;

  // `null` from a load means the service is absent, which is what an
  // instance with federation off answers. The peer screens are hidden then:
  // a peer list that can never have a peer is furniture.
  #off(): boolean {
    return !this.#identity.loading && this.#identity.error === undefined && this.#identity.value === null;
  }

  protected build(): void {
    // The service is absent rather than failing. Say so, and say what to do
    // about it, instead of rendering the peer screens of an instance that
    // has no peers and cannot have any.
    this.#offNote = h("p", { class: "alert alert-status", role: "status", hidden: true }, t("federation.off"));
    this.#identityBox = h("div");

    // ---- instance settings ----
    // A change saves at once: each control is one setting.
    this.#publishDefault = selectField<PublishSetting>({
      label: t("settings.publishDefault"),
      value: "in",
      options: [
        { value: "in", label: t("publish.in") },
        { value: "out", label: t("publish.out") },
      ],
      onChange: (v) => this.#save({ publishDefault: v }),
    });
    this.#orgOverride = checkField({
      label: t("settings.organizationOverride"),
      checked: false,
      onChange: (v) => this.#save({ organizationOverrideAllowed: v }),
    });
    this.#gatheringOverride = checkField({
      label: t("settings.gatheringOverride"),
      checked: false,
      onChange: (v) => this.#save({ gatheringOverrideAllowed: v }),
    });
    const settingNumber = (label: string, hint: string, key: "retentionDays" | "peerRateLimitPerMinute" | "originRateLimitPerMinute") => {
      const field = numberField({ label, hint, value: null, min: 0, onChange: () => {} });
      field.control.addEventListener("change", (e) => {
        if (e.target !== field.control) return;
        this.#save({ [key]: Number(field.control.value ?? 0) });
      });
      return field;
    };
    const retention = settingNumber(t("settings.retentionDays"), t("settings.retentionHint"), "retentionDays");
    const rate = settingNumber(t("settings.rateLimit"), t("settings.rateLimitHint"), "peerRateLimitPerMinute");
    const originRate = settingNumber(
      t("settings.originRateLimit"),
      t("settings.originRateLimitHint"),
      "originRateLimitPerMinute",
    );
    this.#retention = retention;
    this.#rateLimit = rate;
    this.#originRateLimit = originRate;
    this.#settingsForm = h(
      "form",
      { hidden: true },
      this.#publishDefault.el,
      this.#orgOverride.el,
      this.#gatheringOverride.el,
      retention.el,
      rate.el,
      originRate.el,
      this.#saved.el,
    );
    this.#settingsForm.addEventListener("submit", (e) => e.preventDefault());
    this.#settingsLoading = h("p", null, t("common.loading"));
    // The instance-wide settings, above the peers, because they are what
    // the per-peer controls sit inside: a peer with no allowance of its own
    // uses the number set here.
    this.#settingsBox = h("section", null, h("h2", null, t("settings.title")), this.#settingsLoading, this.#settingsForm);

    // ---- peers ----
    this.#peerList = h("div");
    this.#peersSection = h("section", null, h("h2", null, t("federation.peers")), this.#peerList);

    // The rate limit is enforced on a PEER, and a peer carries many
    // organizations. A throttled peer therefore does not say which origin
    // caused it. This list does, busiest first.
    this.#volumeList = h("div");
    this.#volumeSection = h(
      "section",
      null,
      h("h2", null, t("volume.title")),
      h("p", { class: "field-hint" }, t("volume.hint")),
      this.#volumeList,
    );

    // ---- add a peer ----
    this.#addressField = textField({
      label: t("federation.addressLabel"),
      hint: t("federation.addressHint"),
      required: true,
      onInput: (v) => {
        this.#address = v;
        this.invalidate();
      },
    });
    this.#addressField.control.setAttribute("autocapitalize", "none");
    this.#addressField.control.setAttribute("spellcheck", "false");
    this.#baseUrlField = textField({
      label: t("federation.baseUrlLabel"),
      required: true,
      type: "url",
      onInput: (v) => {
        this.#baseUrl = v;
        this.invalidate();
      },
    });
    this.#noteField = textField({
      label: t("federation.noteLabel"),
      optional: true,
      onInput: (v) => {
        this.#note = v;
      },
    });
    this.#submit = button(t("common.create"), { type: "submit" });
    const addForm = h(
      "form",
      null,
      this.#addressField.el,
      this.#baseUrlField.el,
      this.#noteField.el,
      h("div", { class: "form-actions" }, this.#submit),
    );
    addForm.addEventListener("submit", (e) => {
      e.preventDefault();
      if (this.#busy || !this.#address.includes("@") || !this.#baseUrl) return;
      void this.#act(async () => {
        await api.federation.addPeer({ address: this.#address, baseUrl: this.#baseUrl, note: this.#note });
        this.#address = this.#baseUrl = this.#note = "";
        this.#addressField.control.value = "";
        this.#baseUrlField.control.value = "";
        this.#noteField.control.value = "";
      });
    });
    this.#addSection = h("section", null, h("h2", null, t("federation.addPeer")), addForm);

    this.replaceChildren(
      h("h1", null, t("federation.title")),
      this.#offNote,
      this.#identityBox,
      this.#alert.el,
      this.#settingsBox,
      this.#peersSection,
      this.#volumeSection,
      this.#addSection,
    );

    void this.#identity.load();
    void this.#peers.load();
    void this.#settings.load();
    void this.#volume.load();
  }

  async #act(run: () => Promise<unknown>): Promise<void> {
    this.#busy = true;
    this.#alert.set(undefined);
    // Clearing the "saved" line here is what keeps it from outliving the
    // save it describes: without this it is set once and never unset, so a
    // later attempt that FAILS renders its error alert underneath a message
    // still claiming the settings were saved.
    this.#saved.set("");
    this.invalidate();
    try {
      await run();
      await Promise.all([this.#peers.load(), this.#volume.load()]);
    } catch (err) {
      this.#alert.set(err);
    } finally {
      this.#busy = false;
      if (this.isActive()) this.invalidate();
    }
  }

  #save(change: UpdateInstanceSettingsRequest): void {
    void this.#act(async () => {
      this.#settings.value = await api.admin.updateInstanceSettings(change);
      this.#settingsShows = null;
      this.#saved.set(t("settings.saved"));
    });
  }

  #setStatus(peer: Peer, direction: "inbound" | "outbound", status: PeerStatus): void {
    void this.#act(() =>
      api.federation.setPeerStatus({
        peerId: peer.id,
        ...(direction === "inbound" ? { inboundStatus: status } : { outboundStatus: status }),
      }),
    );
  }

  /** A button in a peer card, disabled while busy or when `blocked()`. */
  #peerButton(label: string, opts: Parameters<typeof button>[1], blocked: () => boolean = () => false): PushButton {
    const el = button(label, opts);
    this.#peerButtons.push({ el, disabled: blocked });
    return el;
  }

  #peerCard(peer: Peer): HTMLLIElement {
    const rate = limitBox(t("federation.setRateLimit"), peer.rateLimitPerMinute, undefined, (v) =>
      void this.#act(() =>
        // An empty field restores the instance limit rather than setting
        // zero, which would mean "no limit at all".
        api.federation.setPeerRateLimit({ peerId: peer.id, rateLimitPerMinute: v }),
      ),
    );
    return h(
      "li",
      { class: peer.suspended ? "card card-locked" : "card" },
      h("h3", null, peer.address),
      h("p", { class: "card-meta" }, peer.baseUrl),
      peer.note ? h("p", { class: "card-blurb" }, peer.note) : null,
      // Two directions, two badges, two sets of controls: never one
      // "trusted" flag.
      h(
        "p",
        { class: "card-meta" },
        `${t("federation.inbound")}: `,
        h("span", { class: "badge" }, t(`federation.status.${peer.inboundStatus}`)),
        ` ${t("federation.outbound")}: `,
        h("span", { class: "badge" }, t(`federation.status.${peer.outboundStatus}`)),
      ),
      h(
        "p",
        { class: "card-meta" },
        `${t("federation.rateLimit")}: `,
        peer.rateLimitPerMinute != null
          ? t("federation.rateLimitOwn", { count: peer.rateLimitPerMinute })
          : t("federation.rateLimitInstance"),
        peer.rateLimitedTotal > 0 ? ` · ${plural("federation.rateLimitedTotal", peer.rateLimitedTotal)}` : null,
      ),
      rate.el,
      h(
        "p",
        { class: "card-meta" },
        plural("federation.queued", peer.pendingDeliveries),
        peer.lastSuccessAt ? ` · ${t("federation.lastSuccess", { when: dateTime(peer.lastSuccessAt) })}` : null,
      ),
      // A stopped peer says so in words, and says why. Only a person lifts
      // a suspension (ResumePeer); nothing here does it on a timer.
      peer.suspended && [
        h(
          "p",
          { class: "alert alert-error", role: "alert" },
          h("strong", null, t("federation.suspended")),
          peer.suspendedAt ? ` ${t("federation.suspendedSince", { when: dateTime(peer.suspendedAt) })}` : null,
          peer.lastFailureReason ? ` ${t("federation.lastFailure", { reason: peer.lastFailureReason })}` : null,
        ),
        this.#peerButton(t("federation.resume"), {
          ariaLabel: t("federation.resumePeer", { peer: peer.address }),
          onClick: () => void this.#act(() => api.federation.resumePeer({ peerId: peer.id })),
        }),
      ],
      h(
        "p",
        { class: "form-actions" },
        this.#peerButton(
          t("federation.approveInbound", { peer: peer.address }),
          { onClick: () => this.#setStatus(peer, "inbound", "approved") },
          () => peer.inboundStatus === "approved",
        ),
        this.#peerButton(
          t("federation.blockInbound", { peer: peer.address }),
          { variant: "danger", onClick: () => this.#setStatus(peer, "inbound", "blocked") },
          () => peer.inboundStatus === "blocked",
        ),
      ),
      h(
        "p",
        { class: "form-actions" },
        this.#peerButton(
          t("federation.approveOutbound", { peer: peer.address }),
          { onClick: () => this.#setStatus(peer, "outbound", "approved") },
          () => peer.outboundStatus === "approved",
        ),
        this.#peerButton(
          t("federation.revokeOutbound", { peer: peer.address }),
          { variant: "danger", onClick: () => this.#setStatus(peer, "outbound", "none") },
          () => peer.outboundStatus === "none",
        ),
      ),
      this.#peerButton(t("federation.remove"), {
        variant: "danger",
        ariaLabel: t("federation.removePeer", { peer: peer.address }),
        onClick: () => {
          if (!window.confirm(t("common.confirmDelete", { name: peer.address }))) return;
          void this.#act(() => api.federation.removePeer({ peerId: peer.id }));
        },
      }),
    );
  }

  #originCard(origin: OriginVolume): HTMLLIElement {
    // Throttle one organization without touching the peer's others — the
    // reason the limit exists at two levels. An empty field restores the
    // instance one.
    const limit = limitBox(
      t("volume.setOriginLimit"),
      origin.rateLimitPerMinute,
      String(origin.effectiveRateLimitPerMinute),
      (v) =>
        void this.#act(() =>
          api.federation.setOriginRateLimit({
            peerId: origin.peerId,
            organizationName: origin.organizationName,
            rateLimitPerMinute: v,
          }),
        ),
    );
    return h(
      "li",
      { class: "card" },
      h("h3", null, origin.organizationName || t("volume.noOrganization")),
      // The peer's address: the domain is the identity, not the name.
      h("p", { class: "card-address" }, origin.peerAddress),
      h(
        "p",
        { class: "card-meta" },
        plural("volume.held", origin.held),
        " · ",
        plural("volume.acceptedTotal", origin.acceptedTotal),
      ),
      h(
        "p",
        { class: "card-meta" },
        origin.effectiveRateLimitPerMinute > 0
          ? t("volume.thisMinute", { count: origin.acceptedThisMinute, limit: origin.effectiveRateLimitPerMinute })
          : t("volume.thisMinuteNoLimit", { count: origin.acceptedThisMinute }),
        origin.lastReceivedAt ? ` · ${t("volume.lastReceived", { when: dateTime(origin.lastReceivedAt) })}` : null,
      ),
      // The peer's state, said here so this list stands on its own rather
      // than needing a cross-reference.
      origin.rateLimitedTotal > 0
        ? h("p", { class: "card-meta" }, plural("volume.originLimited", origin.rateLimitedTotal))
        : null,
      origin.peerRateLimitedTotal > 0
        ? h("p", { class: "card-meta" }, plural("federation.rateLimitedTotal", origin.peerRateLimitedTotal))
        : null,
      limit.el,
      origin.peerSuspended ? h("p", { class: "alert alert-error", role: "alert" }, t("volume.peerSuspended")) : null,
    );
  }

  protected update(): void {
    const off = this.#off();
    this.#offNote.hidden = !off;
    this.#peersSection.hidden = off;
    this.#volumeSection.hidden = off;
    this.#addSection.hidden = off;

    // `enabled` is the server's answer, and it is the one to read: an
    // instance that does not federate still answers this op, with an empty
    // address. Guarding on the object alone renders "This instance is "
    // with a blank where its name should be.
    const who = this.#identity.value;
    const identityShows = `${off}|${who?.enabled}|${who?.address}|${who?.algorithm}|${!!this.#identity.error}`;
    if (identityShows !== this.#identityShows) {
      this.#identityShows = identityShows;
      if (who?.enabled) {
        fill(
          this.#identityBox,
          h("p", { class: "card-meta" }, t("federation.identity", { address: who.address })),
          h("p", { class: "card-meta" }, t("federation.algorithm", { algorithm: who.algorithm })),
          // An unsigned scheme is stated where an operator will see it, not
          // only in a log line at boot. A page that looks the same whether or
          // not deliveries are authenticated is a page that hides the thing
          // most worth knowing.
          who.algorithm.startsWith("dev-")
            ? h("p", { class: "alert alert-error", role: "alert" }, t("federation.unsignedWarning"))
            : null,
        );
      } else if (this.#identity.error) {
        fill(this.#identityBox, errorAlertFor(this.#identity.error));
      } else if (!off && !this.#identity.loading) {
        fill(this.#identityBox, h("p", { class: "card-meta" }, t("federation.identityUnset")));
      } else {
        fill(this.#identityBox);
      }
    }

    // Settings: the controls are kept; only their values follow the server.
    const settings = this.#settings.value;
    this.#settingsForm.hidden = !settings;
    if (!settings && this.#settings.error) fill(this.#settingsLoading, errorAlertFor(this.#settings.error));
    this.#settingsLoading.hidden = !!settings;
    if (settings && settings !== this.#settingsShows) {
      this.#settingsShows = settings;
      if (this.#publishDefault.control.value !== settings.publishDefault) {
        this.#publishDefault.control.value = settings.publishDefault;
      }
      this.#orgOverride.control.checked = settings.organizationOverrideAllowed;
      this.#gatheringOverride.control.checked = settings.gatheringOverrideAllowed;
      this.#retention.control.value = settings.retentionDays;
      this.#rateLimit.control.value = settings.peerRateLimitPerMinute;
      this.#originRateLimit.control.value = settings.originRateLimitPerMinute;
    }

    if (!off) {
      const peers = this.#peers.value;
      const peersShows = peers ?? this.#peers.error ?? "loading";
      if (peersShows !== this.#peersShows) {
        this.#peersShows = peersShows;
        this.#peerButtons = [];
        if (peers && peers.peers.length > 0) {
          fill(this.#peerList, h("ul", { class: "cards" }, peers.peers.map((p) => this.#peerCard(p))));
        } else if (peers) {
          fill(this.#peerList, h("p", null, t("federation.noPeers")));
        } else if (this.#peers.error) {
          fill(this.#peerList, errorAlertFor(this.#peers.error));
        } else {
          fill(this.#peerList, h("p", null, t("common.loading")));
        }
      }

      const volume = this.#volume.value;
      const volumeShows = volume ?? this.#volume.error ?? "loading";
      if (volumeShows !== this.#volumeShows) {
        this.#volumeShows = volumeShows;
        if (volume && volume.origins.length > 0) {
          fill(this.#volumeList, h("ul", { class: "cards" }, volume.origins.map((o) => this.#originCard(o))));
        } else if (volume) {
          fill(this.#volumeList, h("p", null, t("volume.none")));
        } else if (this.#volume.error) {
          fill(this.#volumeList, errorAlertFor(this.#volume.error));
        } else {
          fill(this.#volumeList, h("p", null, t("common.loading")));
        }
      }
    }

    for (const b of this.#peerButtons) setDisabled(b.el, this.#busy || b.disabled());
    setDisabled(this.#submit, this.#busy || !this.#address.includes("@") || !this.#baseUrl);
  }
}

define("tinku-federation-page", FederationPage);
