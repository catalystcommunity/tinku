import type { Origin } from "~/gen/types.gen";
import { t } from "~/i18n";
import { h } from "~/ui/dom";

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
export function originBadge(origin: Origin, local?: string): HTMLElement {
  return h(
    "span",
    { class: "card-address" },
    local ? `${local}@${origin.domain}` : t("origin.at", { domain: origin.domain }),
    origin.isExternal && [
      " ",
      h("span", { class: "badge badge-external" }, t("origin.external")),
      origin.peerAddress ? ` ${t("origin.viaPeer", { peer: origin.peerAddress })}` : null,
    ],
  );
}
