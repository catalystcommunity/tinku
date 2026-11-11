/**
 * The app shell: the skip link, the landmarks, the navigation, the session
 * controls, and the page for the current route.
 *
 * Three accessibility decisions live here rather than in a stylesheet,
 * because they are behaviour:
 *
 *   1. A skip link, first in the tab order, so a keyboard user can reach
 *      the content without tabbing through the whole navigation on every
 *      page.
 *   2. Focus moves to the main landmark on every route change. A
 *      single-page app replaces the content without a page load, so nothing
 *      tells a screen reader that anything happened unless the app says so.
 *      It does not move when the focus is already inside the new page, so
 *      it does not take the focus from a field that the page focused.
 *   3. `aria-current="page"` on the active link, so the current location is
 *      conveyed by more than the colour it is drawn in.
 *
 * A page that throws while it draws sends `tinku-crash`. The crash panel
 * replaces the page INSIDE the main landmark, so the header and the
 * navigation survive: a reader who lands on a broken screen can still leave
 * it.
 */
import { t } from "~/i18n";
import { ROUTE, currentRoute, type Route, type RouteName } from "~/app/router";
import { SESSION, useSession } from "~/lib/session";
import { CRASH_EVENT, TinkuElement, define } from "~/ui/element";
import { h } from "~/ui/dom";
import { errorAlert } from "~/components/Alert";
import { button, setButtonText, setDisabled, setInnerAria, textField } from "~/components/Field";
import "~/routes/Discover";
import "~/routes/EventDetail";
import "~/routes/Federation";
import "~/routes/GatheringDetail";
import "~/routes/Gatherings";
import "~/routes/Mine";
import "~/routes/OrganizationDetail";
import "~/routes/Organizations";

/** The page element of each route. */
const PAGE_TAGS: Record<Exclude<RouteName, "notFound">, string> = {
  discover: "tinku-discover-page",
  gatherings: "tinku-gatherings-page",
  gathering: "tinku-gathering-page",
  organizations: "tinku-organizations-page",
  organization: "tinku-organization-page",
  event: "tinku-event-page",
  mine: "tinku-mine-page",
  federation: "tinku-federation-page",
};

/** A page element. The shell sets `routeId` before it connects. */
export interface PageElement extends HTMLElement {
  routeId?: string;
}

function pageFor(route: Route): HTMLElement {
  if (route.name === "notFound") {
    return h(
      "section",
      null,
      h("h1", null, t("notFound.title")),
      h("p", null, t("notFound.explain")),
      h("p", null, h("a", { href: "/" }, t("crash.home"))),
    );
  }
  const page = document.createElement(PAGE_TAGS[route.name]) as PageElement;
  page.routeId = route.id;
  return page;
}

/**
 * The last line of defence for a page that throws while it draws.
 *
 * `reset` is offered because the common causes are transient — a request
 * that failed, a page opened mid-deploy — and drawing the page again is a
 * real fix for those rather than a placebo.
 */
function crashPanel(error: unknown, retry: () => void): HTMLElement {
  return h(
    "section",
    null,
    // role="alert" so this is announced. Somebody who cannot see the page
    // going blank otherwise gets no signal at all.
    h("div", { class: "alert alert-error", role: "alert" }, h("h1", null, t("crash.title")), h("p", null, t("crash.explain"))),
    h("p", null, button(t("crash.retry"), { onClick: retry }), " ", h("a", { href: "/" }, t("crash.home"))),
    // The message is a developer's diagnostic, not display text, so it is
    // folded away rather than shown as if it were for the reader.
    h("details", null, h("summary", null, t("crash.detail")), h("pre", { class: "crash-detail" }, String(error))),
  );
}

class AppShell extends TinkuElement {
  static watches = [ROUTE];
  #main!: HTMLElement;
  #links: { el: HTMLAnchorElement; href: string; end: boolean }[] = [];
  /** The path that the page shows. A change makes a new page. */
  #shown = "";

  protected build(): void {
    const navLink = (href: string, text: string, end = false) => {
      const el = h("a", { href }, text);
      this.#links.push({ el, href, end });
      return h("li", null, el);
    };
    // tabindex="-1" makes the landmark programmatically focusable without
    // putting it in the tab order — which is exactly what a focus target
    // for a route change needs to be.
    this.#main = h("main", { id: "main", class: "app", tabindex: "-1", "aria-label": t("app.mainLabel") });
    this.#main.addEventListener(CRASH_EVENT, (e) => {
      e.stopPropagation();
      this.#main.replaceChildren(crashPanel((e as CustomEvent).detail, () => this.#showPage(true)));
    });
    this.replaceChildren(
      h("a", { class: "skip-link", href: "#main" }, t("app.skipToContent")),
      h(
        "header",
        { class: "app-header" },
        h(
          "div",
          { class: "app-header-inner" },
          h(
            "p",
            { class: "brand" },
            h(
              "a",
              { href: "/" },
              // alt is empty on purpose: the link's own text already names
              // it, and a second reading of "Tinku" is noise.
              h("img", { class: "brand__mark", src: "/tinku-mark.svg", alt: "", width: "24", height: "24" }),
              h("span", null, t("app.name")),
            ),
          ),
          h(
            "nav",
            { "aria-label": t("app.primaryNavLabel") },
            h(
              "ul",
              null,
              navLink("/", t("nav.discover"), true),
              navLink("/gatherings", t("gathering.plural")),
              navLink("/organizations", t("organization.pluralShort")),
              navLink("/mine", t("nav.mine")),
            ),
          ),
          h("tinku-session-controls"),
        ),
      ),
      this.#main,
    );
  }

  protected update(): void {
    const route = currentRoute();
    for (const link of this.#links) {
      const active = link.end ? route.path === link.href : route.path.startsWith(link.href);
      if (active) link.el.setAttribute("aria-current", "page");
      else link.el.removeAttribute("aria-current");
    }
    this.#showPage(false);
  }

  #showPage(force: boolean): void {
    const route = currentRoute();
    const key = route.path;
    if (!force && key === this.#shown) return;
    this.#shown = key;
    this.#main.replaceChildren(pageFor(route));
    if (!this.#main.contains(document.activeElement)) this.#main.focus({ preventScroll: true });
  }
}
define("tinku-app", AppShell);

/**
 * Sign in and sign out.
 *
 * The form here is the development one, which mints a session with no
 * identity provider. A build with linkkeys configured begins a real login
 * instead; both end at the same place, which is why the rest of the app
 * never asks which happened.
 */
class SessionControls extends TinkuElement {
  static watches = [SESSION];
  #toggle!: ReturnType<typeof button>;
  #form!: HTMLFormElement;
  #submit!: ReturnType<typeof button>;
  #who!: HTMLElement;
  #signOut!: ReturnType<typeof button>;
  #signedOut!: HTMLElement;
  #signedIn!: HTMLElement;
  #handle = "";
  #domain = "example.test";
  #busy = false;
  #alert = errorAlert();

  protected build(): void {
    const session = useSession();
    this.classList.add("session");
    this.#toggle = button(t("session.signIn"), {
      variant: "default",
      onClick: () => {
        this.#form.hidden = !this.#form.hidden;
        this.invalidate();
      },
    });
    const handle = textField({
      label: t("session.handleLabel"),
      required: true,
      autocomplete: "username",
      onInput: (v) => {
        this.#handle = v;
        this.invalidate();
      },
    });
    handle.control.setAttribute("autocapitalize", "none");
    handle.control.setAttribute("spellcheck", "false");
    const domain = textField({
      label: t("session.domainLabel"),
      required: true,
      value: this.#domain,
      onInput: (v) => {
        this.#domain = v;
        this.invalidate();
      },
    });
    this.#submit = button(t("session.devSignIn"), { type: "submit" });
    this.#form = h(
      "form",
      { class: "session-form", hidden: true },
      h("p", { class: "field-hint" }, t("session.devSignInHint")),
      handle.el,
      domain.el,
      this.#submit,
      this.#alert.el,
    );
    this.#form.addEventListener("submit", async (e) => {
      e.preventDefault();
      if (this.#busy || !this.#handle.trim()) return;
      this.#busy = true;
      this.#alert.set(undefined);
      this.invalidate();
      try {
        await session.devLogin(this.#handle.trim(), this.#domain.trim());
        this.#form.hidden = true;
      } catch (err) {
        this.#alert.set(err);
      } finally {
        this.#busy = false;
        this.invalidate();
      }
    });
    this.#signedOut = h("div", { class: "session-out" }, this.#toggle, this.#form);
    this.#who = h("span", { class: "session-who" });
    this.#signOut = button(t("session.signOut"), { variant: "default", onClick: () => void session.logout() });
    this.#signedIn = h("div", { class: "session-in" }, this.#who, this.#signOut);
    this.replaceChildren(this.#signedOut, this.#signedIn);
  }

  protected update(): void {
    const user = useSession().user();
    this.#signedIn.hidden = !user;
    this.#signedOut.hidden = !!user;
    if (user) {
      const text = t("session.signedInAs", { address: `${user.handle}@${user.linkkeysDomain}` });
      if (this.#who.textContent !== text) this.#who.textContent = text;
    }
    setInnerAria(this.#toggle, "aria-expanded", String(!this.#form.hidden));
    setDisabled(this.#submit, this.#busy || !this.#handle.trim());
    setButtonText(this.#submit, this.#busy ? t("session.working") : t("session.devSignIn"));
  }
}
define("tinku-session-controls", SessionControls);
