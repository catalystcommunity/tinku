import { Show, type JSX } from "solid-js";
import { useI18n } from "~/i18n";
import { errorDetail, errorKey } from "~/lib/messages";

/**
 * An error, announced.
 *
 * `role="alert"` is what makes an assistive technology read this the moment
 * it appears — without it a failure is silent for anybody not watching the
 * region it rendered into, which is the whole population this matters most
 * for.
 */
export function ErrorAlert(props: { error: unknown }): JSX.Element {
  const { t } = useI18n();
  return (
    <Show when={props.error}>
      <p class="alert alert-error" role="alert">
        {t(errorKey(props.error))}
        <Show when={errorDetail(props.error)}>
          {(detail) => <span class="alert-detail"> {detail()}</span>}
        </Show>
      </p>
    </Show>
  );
}

/**
 * A status message.
 *
 * `aria-live="polite"` announces it at the next pause rather than
 * interrupting, which is right for a confirmation and wrong for an error —
 * hence two components rather than one with a flag.
 */
export function StatusMessage(props: { children: JSX.Element }): JSX.Element {
  return (
    <p class="alert alert-status" role="status" aria-live="polite">
      {props.children}
    </p>
  );
}
