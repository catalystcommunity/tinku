// The browser transport: one CBOR envelope per call to `POST /csil/v1/rpc`.
//
// What this file knows about the wire (matching api/internal/server):
//
//   - The endpoint is same-origin `/csil/v1/rpc`. The path carries no
//     routing meaning — (service, op) live inside the envelope.
//   - `Content-Type: application/cbor`. The whole body is one request
//     envelope; the whole response body is one response envelope.
//   - A well-formed request always answers HTTP 200, even when it failed.
//     HTTP status codes are reserved for failures the envelope cannot
//     express: a wrong mount (404), an over-size body (413). So a non-200
//     here means something below CSIL-RPC went wrong.
//   - The session is an httpOnly cookie. `credentials: "same-origin"` is
//     what carries it; there is no bearer token to attach.
import type { AsyncServiceTransport } from "~/gen/client.async.gen";
import { fromServiceErrorCbor } from "~/gen/codec.gen";
import { Status, statusName } from "~/transport/csil/conventions";
import { RpcRequest, RpcResponse } from "~/transport/csil/rpc";
import { TinkuServiceError, TinkuTransportError } from "./errors";
import { methodToOp, serviceToWire } from "./opNaming";

export const RPC_ENDPOINT = "/csil/v1/rpc";

export interface HttpTransportOptions {
  endpoint?: string;
  /** Override for tests; defaults to the global `fetch`. */
  fetchImpl?: typeof fetch;
}

export function createHttpTransport(options: HttpTransportOptions = {}): AsyncServiceTransport {
  const endpoint = options.endpoint ?? RPC_ENDPOINT;
  const doFetch = options.fetchImpl ?? fetch;

  return {
    async call(service: string, op: string, payload: Uint8Array): Promise<Uint8Array> {
      const envelope = new RpcRequest(serviceToWire(service), methodToOp(op), payload).encode();

      let res: Response;
      try {
        res = await doFetch(endpoint, {
          method: "POST",
          credentials: "same-origin",
          headers: { "Content-Type": "application/cbor", Accept: "application/cbor" },
          // lib.dom's BodyInit wants Uint8Array<ArrayBuffer>; the codec
          // returns Uint8Array<ArrayBufferLike>. Same bytes on every
          // runtime that matters, so this is a type-only cast.
          body: envelope as unknown as BodyInit,
        });
      } catch (e) {
        const detail = e instanceof Error ? e.message : String(e);
        throw new TinkuTransportError(`network error calling ${service}/${op}: ${detail}`);
      }

      const body = new Uint8Array(await res.arrayBuffer());

      let response: RpcResponse;
      try {
        response = RpcResponse.decode(body);
      } catch {
        throw new TinkuTransportError(
          res.ok
            ? `undecodable response envelope for ${service}/${op}`
            : `${service}/${op}: ${res.status} ${res.statusText}`,
          res.ok ? undefined : res.status,
        );
      }

      if (response.status !== Status.Ok) {
        throw new TinkuTransportError(
          response.error ?? `transport status ${statusName(response.status)} calling ${service}/${op}`,
          response.status,
        );
      }

      // A declared error arm rides at status 0 like any other reply — the
      // variant name is what tells them apart.
      if (response.variant === "ServiceError") {
        throw new TinkuServiceError(fromServiceErrorCbor(response.payload));
      }

      return response.payload;
    },
  };
}
