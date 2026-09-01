/**
 * A link this app is willing to put in an href.
 *
 * Two of the URLs on these screens are written by somebody else: an event's
 * `online_url`, typed by whoever runs the gathering, and a remote event's
 * `canonical_url`, sent by a peer instance. Neither is a page this instance
 * controls, and a browser will happily run `javascript:` out of an href —
 * on THIS origin, with this instance's session cookie attached. An
 * allow-list of schemes is what stops that, and it is an allow-list rather
 * than a deny-list because the set of schemes a browser understands grows.
 *
 * `undefined` means "not a link". Every caller renders the text without an
 * anchor rather than emitting an href it cannot vouch for — a dead link is
 * a smaller failure than a live script.
 */
const allowedSchemes = new Set(["http:", "https:"]);

export function safeUrl(raw: string | null | undefined): string | undefined {
  if (!raw) return undefined;
  let parsed: URL;
  try {
    parsed = new URL(raw);
  } catch {
    // A relative URL resolves against this origin, which is exactly what a
    // link to somebody else's site must not do.
    return undefined;
  }
  return allowedSchemes.has(parsed.protocol) ? parsed.href : undefined;
}
