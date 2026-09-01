import { A, Route, Router, useLocation } from "@solidjs/router";
import { Show, createEffect, createSignal, type JSX } from "solid-js";
import { useI18n } from "~/i18n";
import { useSession } from "~/lib/session";
import { ErrorAlert } from "~/components/Alert";
import { Crash } from "~/components/Crash";
import { Field } from "~/components/Field";
import Discover from "~/routes/Discover";
import EventDetail from "~/routes/EventDetail";
import GatheringDetail from "~/routes/GatheringDetail";
import Federation from "~/routes/Federation";
import Gatherings from "~/routes/Gatherings";
import OrganizationDetail from "~/routes/OrganizationDetail";
import Organizations from "~/routes/Organizations";
import Mine from "~/routes/Mine";

/**
 * The app shell: the skip link, the landmarks, the navigation and the
 * session controls.
 *
 * Three accessibility decisions live here rather than in a stylesheet,
 * because they are behaviour:
 *
 *   1. A skip link, first in the tab order, so a keyboard user can reach
 *      the content without tabbing through the whole navigation on every
 *      page.
 *   2. Focus moves to the main heading on every route change. A single-page
 *      app replaces the content without a page load, so nothing tells a
 *      screen reader that anything happened unless the app says so.
 *   3. `aria-current="page"` on the active link, so the current location is
 *      conveyed by more than the colour it is drawn in.
 */
function Shell(props: { children?: JSX.Element }): JSX.Element {
  const { t } = useI18n();
  const location = useLocation();
  let main: HTMLElement | undefined;

  createEffect(() => {
    // Reading location.pathname is what subscribes this effect to route
    // changes; the focus move is the effect.
    location.pathname;
    main?.focus();
  });

  return (
    <>
      <a class="skip-link" href="#main">
        {t("app.skipToContent")}
      </a>

      <header class="app-header">
        <div class="app-header-inner">
          <p class="brand">
            <A href="/">
              {/* alt is empty on purpose: the link's own text already names
                  it, and a second reading of "Tinku" is noise. */}
              <img class="brand__mark" src="/tinku-mark.svg" alt="" width="24" height="24" />
              <span>{t("app.name")}</span>
            </A>
          </p>
          <nav aria-label={t("app.primaryNavLabel")}>
            <ul>
              <NavLink href="/" end>
                {t("nav.discover")}
              </NavLink>
              <NavLink href="/gatherings">{t("gathering.plural")}</NavLink>
              <NavLink href="/organizations">{t("organization.pluralShort")}</NavLink>
              <NavLink href="/mine">{t("nav.mine")}</NavLink>
            </ul>
          </nav>
          <SessionControls />
        </div>
      </header>

      {/*
        tabindex="-1" makes the landmark programmatically focusable without
        putting it in the tab order — which is exactly what a focus target
        for a route change needs to be.
      */}
      {/*
        The boundary is inside the landmark, so a screen that throws does
        not take the header and the navigation with it. A reader who lands
        on a broken page can still leave it.
      */}
      <main id="main" class="app" ref={main} tabindex="-1" aria-label={t("app.mainLabel")}>
        <Crash>{props.children}</Crash>
      </main>
    </>
  );
}

function NavLink(props: { href: string; end?: boolean; children: JSX.Element }): JSX.Element {
  const location = useLocation();
  const active = () =>
    props.end ? location.pathname === props.href : location.pathname.startsWith(props.href);
  return (
    <li>
      <A href={props.href} aria-current={active() ? "page" : undefined}>
        {props.children}
      </A>
    </li>
  );
}

/**
 * Sign in and sign out.
 *
 * The form here is the development one, which mints a session with no
 * identity provider. A build with linkkeys configured begins a real login
 * instead; both end at the same place, which is why the rest of the app
 * never asks which happened.
 */
function SessionControls(): JSX.Element {
  const { t } = useI18n();
  const { user, devLogin, logout } = useSession();
  const [handle, setHandle] = createSignal("");
  const [domain, setDomain] = createSignal("example.test");
  const [open, setOpen] = createSignal(false);
  const [error, setError] = createSignal<unknown>();
  const [busy, setBusy] = createSignal(false);

  const signIn = async (e: SubmitEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      await devLogin(handle().trim(), domain().trim());
      setOpen(false);
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="session">
      <Show
        when={user()}
        fallback={
          <>
            <button type="button" onClick={() => setOpen(!open())} aria-expanded={open()}>
              {t("session.signIn")}
            </button>
            <Show when={open()}>
              <form onSubmit={signIn} class="session-form">
                <p class="field-hint">{t("session.devSignInHint")}</p>
                <Field label={t("session.handleLabel")} required requiredText={t("common.required")}>
                  {(control) => (
                    <input
                      {...control}
                      type="text"
                      autocomplete="username"
                      value={handle()}
                      onInput={(e) => setHandle(e.currentTarget.value)}
                    />
                  )}
                </Field>
                <Field label={t("session.domainLabel")} required requiredText={t("common.required")}>
                  {(control) => (
                    <input
                      {...control}
                      type="text"
                      value={domain()}
                      onInput={(e) => setDomain(e.currentTarget.value)}
                    />
                  )}
                </Field>
                <button type="submit" disabled={busy() || !handle().trim()}>
                  {busy() ? t("session.working") : t("session.devSignIn")}
                </button>
                <ErrorAlert error={error()} />
              </form>
            </Show>
          </>
        }
      >
        {(profile) => (
          <>
            <span class="session-who">
              {t("session.signedInAs", {
                address: `${profile().handle}@${profile().linkkeysDomain}`,
              })}
            </span>
            <button type="button" onClick={() => logout()}>
              {t("session.signOut")}
            </button>
          </>
        )}
      </Show>
    </div>
  );
}

export default function App(): JSX.Element {
  return (
    <Router root={Shell}>
      <Route path="/" component={Discover} />
      <Route path="/gatherings" component={Gatherings} />
      <Route path="/gatherings/:id" component={GatheringDetail} />
      <Route path="/organizations" component={Organizations} />
      <Route path="/organizations/:id" component={OrganizationDetail} />
      <Route path="/events/:id" component={EventDetail} />
      <Route path="/mine" component={Mine} />
      <Route path="/federation" component={Federation} />
    </Router>
  );
}
