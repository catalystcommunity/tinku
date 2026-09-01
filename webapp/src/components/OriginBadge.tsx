import { Show, type JSX } from "solid-js";
import type { Origin } from "~/gen/types.gen";
import { useI18n } from "~/i18n";

/**
 * Which domain owns a record.
 *
 * Always rendered, never only for foreign records. A name is not an
 * identity — two domains can hold an organization called the same thing,
 * and a directory can show both at once. Showing the domain every time is
 * what makes "is this the one I think it is?" a question the screen has
 * already answered, rather than one a reader has to think to ask.
 *
 * A foreign record gets a badge as well as the domain, because a reader
 * scanning a list reads shape before text.
 */
export function OriginBadge(props: { origin: Origin; local?: string }): JSX.Element {
  const { t } = useI18n();
  return (
    <span class="card-address">
      {props.local ? `${props.local}@${props.origin.domain}` : t("origin.at", { domain: props.origin.domain })}
      <Show when={props.origin.isExternal}>
        {" "}
        <span class="badge badge-external">{t("origin.external")}</span>
        <Show when={props.origin.peerAddress}>
          {(peer) => <> {t("origin.viaPeer", { peer: peer() })}</>}
        </Show>
      </Show>
    </span>
  );
}
