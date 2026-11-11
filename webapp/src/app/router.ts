/**
 * Routes over the Zest SimpleRouter. The current route goes into the store
 * (collection ROUTE), and the shell shows the page for it.
 *
 * Links are plain `<a href>`. `routeLinks()` routes a click on a same-origin
 * link in the page, so a link works with a middle click and a screen
 * reader's link list like any other.
 */
import SimpleRouter from "@catalystcommunity/simple-router";
import { cell } from "~/state/store";

export type RouteName =
  | "discover"
  | "gatherings"
  | "gathering"
  | "organizations"
  | "organization"
  | "event"
  | "mine"
  | "federation"
  | "notFound";

export interface Route {
  name: RouteName;
  /** The pathname of the URL. */
  path: string;
  /** The query string of the URL. */
  query: URLSearchParams;
  /** `/gatherings/:id` and the others: the id. */
  id?: string;
}

/** The store collection of this module. */
export const ROUTE = "route";

const routeCell = cell<Route>(ROUTE, "current", { name: "notFound", path: "/", query: new URLSearchParams() });
export const currentRoute = (): Route => routeCell.get();

let router: SimpleRouter | null = null;

function setRoute(name: RouteName, id?: string): void {
  routeCell.set({ name, path: location.pathname, query: new URLSearchParams(location.search), id });
}

/** A list route at `/<base>`, and a detail route at `/<base>/:id`. */
function listAndDetail(r: SimpleRouter, base: string, list: RouteName | null, detail: RouteName): void {
  r.registerRoute(`/${base}`, (data) => {
    const segments = data.path.split("/").filter(Boolean);
    if (segments.length === 1 && list) return setRoute(list);
    if (segments.length === 2) return setRoute(detail, decodeURIComponent(segments[1]!));
    setRoute("notFound");
  });
}

/** Register the routes, follow the browser history, and route the current URL. */
export function startRouter(): void {
  router?.cleanup();
  const r = new SimpleRouter();
  // The store tells the pages; no document event is needed.
  r.eventSender = () => {};
  r.registerRoute("/", (data) => setRoute(data.path === "/" ? "discover" : "notFound"));
  listAndDetail(r, "gatherings", "gatherings", "gathering");
  listAndDetail(r, "organizations", "organizations", "organization");
  listAndDetail(r, "events", null, "event");
  r.registerRoute("/mine", () => setRoute("mine"));
  r.registerRoute("/federation", () => setRoute("federation"));
  r.defaultRoute = () => setRoute("notFound");
  router = r;
  r.route("direct", {});
}

/** Stop following the browser history. Tests call it between cases. */
export function stopRouter(): void {
  router?.cleanup();
  router = null;
}

/** Go to an app path. `replace` changes the current history entry. */
export function navigate(path: string, opts: { replace?: boolean } = {}): void {
  if (!router) return;
  if (opts.replace) {
    history.replaceState({}, "", path);
    router.route("direct", {});
  } else if (path !== location.pathname + location.search) {
    router.changePage(path);
  }
}

/** True for an app path that a link click should route in the page. The
 *  API's own paths go to the server. */
function isAppPath(url: URL): boolean {
  return url.origin === location.origin && !url.pathname.startsWith("/csil/") && !url.pathname.startsWith("/auth/");
}

/** Route clicks on same-origin links in the page, as an app link does. */
export function routeLinks(root: EventTarget = document): () => void {
  const onClick = (e: Event) => {
    const ev = e as MouseEvent;
    if (ev.defaultPrevented || ev.button !== 0 || ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.altKey) return;
    const a = (ev.composedPath()[0] as Element | null)?.closest?.("a[href]") as HTMLAnchorElement | null;
    if (!a || (a.target && a.target !== "_self") || a.hasAttribute("download")) return;
    const href = a.getAttribute("href") ?? "";
    // A fragment link (the skip link) stays a fragment link.
    if (href.startsWith("#")) return;
    const url = new URL(a.href, location.href);
    if (!isAppPath(url)) return;
    ev.preventDefault();
    navigate(url.pathname + url.search);
  };
  root.addEventListener("click", onClick);
  return () => root.removeEventListener("click", onClick);
}
