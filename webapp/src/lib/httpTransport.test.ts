// The transport's job is to turn the three wire outcomes into three
// distinguishable results for a caller: a payload, a TinkuServiceError, or
// a TinkuTransportError. These tests build real envelopes with the
// vendored codec, so they pin the actual bytes rather than a mock's idea of
// them.
import { describe, expect, it } from "vitest";
import { toServiceErrorCbor, toGreetingListCbor } from "~/gen/codec.gen";
import { Status } from "~/transport/csil/conventions";
import { RpcRequest, RpcResponse } from "~/transport/csil/rpc";
import { TinkuServiceError, TinkuTransportError } from "./errors";
import { createHttpTransport } from "./httpTransport";

/** A fetch stand-in that answers with one encoded envelope and records the request. */
function respondWith(response: RpcResponse, seen: { request?: RpcRequest } = {}) {
  const fetchImpl = async (_url: string | URL | Request, init?: RequestInit) => {
    seen.request = RpcRequest.decode(new Uint8Array(init!.body as ArrayBuffer));
    return new Response(response.encode() as unknown as BodyInit, { status: 200 });
  };
  return { fetchImpl: fetchImpl as unknown as typeof fetch, seen };
}

describe("createHttpTransport", () => {
  it("puts the canonical service and op on the wire", async () => {
    const reply = RpcResponse.ok("GreetingList", toGreetingListCbor({ greetings: [] }));

    const { fetchImpl, seen } = respondWith(reply);
    await createHttpTransport({ fetchImpl }).call("GreetingService", "list-greetings", new Uint8Array());

    // "GreetingService" is what the generated client passes; "greeting" is
    // what api/internal/server/dispatch.go is keyed on.
    expect(seen.request?.service).toBe("greeting");
    expect(seen.request?.op).toBe("list-greetings");
  });

  it("returns the payload for a typed reply", async () => {
    const reply = RpcResponse.ok("GreetingList", toGreetingListCbor({ greetings: [] }));

    const { fetchImpl } = respondWith(reply);
    const out = await createHttpTransport({ fetchImpl }).call("GreetingService", "list-greetings", new Uint8Array());
    expect(out).toEqual(reply.payload);
  });

  it("throws a ServiceError for the declared error arm, not a transport error", async () => {
    // A declared error arm is a SUCCESSFUL transport outcome — status 0 —
    // distinguished only by its variant name. Getting this wrong is the
    // classic CSIL-RPC client bug.
    const reply = RpcResponse.ok("ServiceError", toServiceErrorCbor({ code: 2, message: "no active session" }));

    const { fetchImpl } = respondWith(reply);
    const call = createHttpTransport({ fetchImpl }).call("AuthService", "whoami", new Uint8Array());

    await expect(call).rejects.toBeInstanceOf(TinkuServiceError);
    await expect(call).rejects.toMatchObject({ code: 2, message: "no active session" });
  });

  it("throws a transport error for a non-zero transport status", async () => {
    const reply = RpcResponse.transportError(Status.UnknownServiceOrOp, "unknown op: greeting/nope");

    const { fetchImpl } = respondWith(reply);
    const call = createHttpTransport({ fetchImpl }).call("GreetingService", "nope", new Uint8Array());

    await expect(call).rejects.toBeInstanceOf(TinkuTransportError);
    await expect(call).rejects.toMatchObject({ status: Status.UnknownServiceOrOp });
  });

  it("throws a transport error when the network fails", async () => {
    const fetchImpl = (async () => {
      throw new TypeError("Failed to fetch");
    }) as unknown as typeof fetch;

    const call = createHttpTransport({ fetchImpl }).call("AuthService", "whoami", new Uint8Array());
    await expect(call).rejects.toBeInstanceOf(TinkuTransportError);
  });
});
