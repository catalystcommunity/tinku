// Verify the vendored CSIL-RPC codec against the shared conformance vectors —
// the same rpc.json the Go and Rust references assert against. This is the
// interop guarantee: a request the TS client encodes is byte-identical to what
// every other CSIL host expects, and vice versa.

import { describe, expect, it } from "vitest";
import vectors from "./conformance.rpc.json";
import { RpcPush, RpcRequest, RpcResponse } from "./rpc";
import { bytesToHex, hexToBytes } from "./conventions";

type VectorInput = Record<string, unknown>;
interface Vector {
  name: string;
  hex: string;
  input: VectorInput;
}

const optStr = (m: VectorInput, k: string): string | undefined =>
  m[k] === null || m[k] === undefined ? undefined : (m[k] as string);
const optNum = (m: VectorInput, k: string): number | undefined =>
  m[k] === null || m[k] === undefined ? undefined : (m[k] as number);

describe("CSIL-RPC conformance vectors", () => {
  for (const vec of (vectors as { vectors: Vector[] }).vectors) {
    it(vec.name, () => {
      const input = vec.input;
      const payload = hexToBytes(optStr(input, "payload_hex") ?? "");

      switch (input.kind) {
        case "request": {
          const req = new RpcRequest(input.service as string, input.op as string, payload);
          req.id = optNum(input, "id");
          req.auth = optStr(input, "auth");
          expect(bytesToHex(req.encode())).toBe(vec.hex);

          const dec = RpcRequest.decode(hexToBytes(vec.hex));
          expect(dec.service).toBe(req.service);
          expect(dec.op).toBe(req.op);
          expect(dec.id).toBe(req.id);
          expect(dec.auth).toBe(req.auth);
          expect(bytesToHex(dec.payload)).toBe(bytesToHex(payload));
          break;
        }
        case "response": {
          const resp = new RpcResponse(input.status as number, payload);
          resp.id = optNum(input, "id");
          resp.variant = optStr(input, "variant");
          resp.error = optStr(input, "error");
          expect(bytesToHex(resp.encode())).toBe(vec.hex);

          const dec = RpcResponse.decode(hexToBytes(vec.hex));
          expect(dec.status).toBe(resp.status);
          expect(dec.id).toBe(resp.id);
          expect(dec.variant).toBe(resp.variant);
          expect(dec.error).toBe(resp.error);
          expect(bytesToHex(dec.payload)).toBe(bytesToHex(payload));
          break;
        }
        case "push": {
          const push = new RpcPush(input.service as string, input.event as string, payload);
          expect(bytesToHex(push.encode())).toBe(vec.hex);

          const dec = RpcPush.decode(hexToBytes(vec.hex));
          expect(dec.service).toBe(push.service);
          expect(dec.event).toBe(push.event);
          expect(bytesToHex(dec.payload)).toBe(bytesToHex(payload));
          break;
        }
        default:
          throw new Error(`unknown rpc kind ${String(input.kind)}`);
      }
    });
  }
});
