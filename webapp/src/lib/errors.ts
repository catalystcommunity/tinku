// The two failure kinds a CSIL-RPC call can produce. They are different
// channels on the wire and deserve different types here, because a caller
// treats them differently: a ServiceError is an answer (this greeting does
// not exist, you are not logged in), while a transport error means the call
// did not arrive at a handler at all.
import type { ServiceError } from "~/gen/types.gen";

/** Application error codes, mirroring api/internal/csilservices/errors.go. */
export const ErrorCode = {
  Invalid: 1,
  Unauthenticated: 2,
  Forbidden: 3,
  NotFound: 4,
  Unavailable: 5,
} as const;

/**
 * A declared `ServiceError` arm: the op ran and answered with a failure it
 * is contractually allowed to return. Branch on `code`, never on `message`.
 */
export class TinkuServiceError extends Error {
  readonly code: number;
  readonly field?: string;
  readonly resourceType?: string;

  constructor(serviceError: ServiceError) {
    super(serviceError.message);
    this.name = "TinkuServiceError";
    this.code = serviceError.code;
    this.field = serviceError.field;
    this.resourceType = serviceError.resourceType;
  }

  /** True when the caller needs to log in (or log in again). */
  get isUnauthenticated(): boolean {
    return this.code === ErrorCode.Unauthenticated;
  }
}

/**
 * A transport failure: the network, a malformed envelope, an op this server
 * does not have, or a status the handler never got to answer. `status` is
 * the CSIL transport status when the envelope decoded, and the HTTP status
 * when it did not.
 */
export class TinkuTransportError extends Error {
  readonly status?: number;

  constructor(message: string, status?: number) {
    super(message);
    this.name = "TinkuTransportError";
    this.status = status;
  }
}
