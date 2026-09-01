import { A } from "@solidjs/router";
import { ErrorBoundary, type JSX } from "solid-js";
import { useI18n } from "~/i18n";

/**
 * The last line of defence for a screen that throws while rendering.
 *
 * Without one, a single throw unmounts the whole app and leaves a blank
 * page — no navigation, no message, nothing to click. That is not a
 * hypothetical: reading an errored Solid resource rethrows during render,
 * so any op that 404s after a delete, or any response the client cannot
 * decode after a schema change, blanks the site.
 *
 * It is placed INSIDE the shell rather than around it, so the header and
 * the navigation survive: a reader who lands on a broken screen can still
 * leave it. Wrapping the whole app would take the escape route out along
 * with the page.
 *
 * `reset` is offered because the common causes are transient — a request
 * that failed, a page opened mid-deploy — and re-running the render is a
 * real fix for those rather than a placebo.
 */
export function Crash(props: { children: JSX.Element }): JSX.Element {
  const { t } = useI18n();
  return (
    <ErrorBoundary
      fallback={(error, reset) => (
        <section>
          {/* role="alert" so this is announced. Somebody who cannot see the
              page going blank otherwise gets no signal at all. */}
          <div class="alert alert-error" role="alert">
            <h1>{t("crash.title")}</h1>
            <p>{t("crash.explain")}</p>
          </div>
          <p>
            <button type="button" onClick={reset}>
              {t("crash.retry")}
            </button>{" "}
            <A href="/">{t("crash.home")}</A>
          </p>
          {/* The message is a developer's diagnostic, not display text, so
              it is folded away rather than shown as if it were for the
              reader. */}
          <details>
            <summary>{t("crash.detail")}</summary>
            <pre class="crash-detail">{String(error)}</pre>
          </details>
        </section>
      )}
    >
      {props.children}
    </ErrorBoundary>
  );
}
