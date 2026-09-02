/**
 * Where the api is, from the browser's point of view.
 *
 * The client and the api are two origins: the client is served on one port
 * and the api listens on another. That is a deployment fact the bundle
 * cannot know at build time — the same built assets are served from
 * localhost, from a port-forwarded hostname, and from a real domain — so it
 * is resolved at RUNTIME, in this order:
 *
 *   1. `window.__TINKU__.apiBaseUrl`, injected by whatever serves the page.
 *      The container renders it from TINKU_PUBLIC_API_URL at start.
 *
 *      An EMPTY value is not an answer, it is an unset variable: envsubst
 *      renders one as empty whether the operator meant "same origin" or
 *      never set it at all, and the two must not be the same instruction.
 *      Empty therefore falls through to the default below. A deployment
 *      that wants same-origin — something in front proxying /csil — says so
 *      with "/".
 *
 *   2. `VITE_TINKU_API_BASE_URL`, for a build or a dev run that wants to
 *      pin it.
 *
 *   3. The default: the same protocol and host as the page, on the api's
 *      port. Serving the client on somehost.tld:8080 therefore calls
 *      somehost.tld:5080 with no configuration at all, which is what a
 *      port-forward of both ports gives you.
 *
 * The port in rule 3 is a build-time default (VITE_TINKU_API_PORT) because
 * it is only a fallback; anything that knows better uses rule 1.
 */

declare global {
  interface Window {
    __TINKU__?: { apiBaseUrl?: string };
  }
}

/** The api port the default rule assumes. Matches TINKU_API_PORT's default. */
export const DEFAULT_API_PORT =
  (import.meta.env?.VITE_TINKU_API_PORT as string | undefined) ?? "5080";

/**
 * The origin to send CSIL-RPC to, with no trailing slash. An empty string
 * means "same origin as this page", which makes every request relative.
 */
export function apiBaseUrl(): string {
  const injected = typeof window !== "undefined" ? window.__TINKU__?.apiBaseUrl : undefined;
  if (injected) {
    return trimSlash(injected);
  }

  const configured = import.meta.env?.VITE_TINKU_API_BASE_URL as string | undefined;
  if (configured) {
    return trimSlash(configured);
  }

  // No window at all — a unit test, or server-side rendering. Relative is
  // the only answer that cannot be wrong.
  if (typeof window === "undefined" || !window.location?.hostname) {
    return "";
  }

  // Already on the api's port: the page is being served by something that
  // also serves the api, so leave it relative rather than pointing a URL at
  // the origin it already has.
  if (window.location.port === DEFAULT_API_PORT) {
    return "";
  }

  return `${window.location.protocol}//${window.location.hostname}:${DEFAULT_API_PORT}`;
}

function trimSlash(url: string): string {
  return url.endsWith("/") ? url.slice(0, -1) : url;
}
