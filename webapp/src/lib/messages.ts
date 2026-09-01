// Turning a thrown error into a message a person can read.
//
// The server's `message` is a diagnostic, not display text: it is English,
// it is not in the catalog, and no translator has seen it. So the UI
// branches on `code` and renders its own message, and keeps the server's
// text only where a code cannot say enough — a validation failure, whose
// whole content is which field and why.
import { TinkuServiceError, TinkuTransportError, ErrorCode } from "./errors";
import type { MessageKey } from "~/i18n/en-US";

/** The catalog key for a thrown error. */
export function errorKey(error: unknown): MessageKey {
  if (error instanceof TinkuServiceError) {
    switch (error.code) {
      case ErrorCode.Unauthenticated:
        return "error.unauthenticated";
      case ErrorCode.Forbidden:
        return "error.forbidden";
      case ErrorCode.NotFound:
        return "error.notFound";
      case ErrorCode.Invalid:
        return "error.invalid";
      case ErrorCode.Unavailable:
        return "error.unavailable";
      default:
        return "error.unknown";
    }
  }
  if (error instanceof TinkuTransportError) return "error.transport";
  return "error.unknown";
}

/**
 * The server's own text, when it adds something the code cannot carry.
 *
 * Only validation failures qualify: "a blurb is at most 300 words" names
 * the rule that was broken, and no generic message can. Everything else
 * reads better as the translated sentence alone.
 */
export function errorDetail(error: unknown): string | undefined {
  if (error instanceof TinkuServiceError && error.code === ErrorCode.Invalid) {
    return error.message;
  }
  return undefined;
}

/** The field a validation failure names, for wiring aria-invalid. */
export function errorField(error: unknown): string | undefined {
  return error instanceof TinkuServiceError ? error.field : undefined;
}
