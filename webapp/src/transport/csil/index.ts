// Vendored copy of the canonical CSIL transport reference implementation,
// github.com/catalystcommunity/csilgen/transports/typescript. Copied rather
// than depended on so the webapp build stays hermetic while the package is
// unpublished — the same call firepit's webapp made.
//
// Trimmed to the RPC envelopes (cbor, conventions, carrier, rpc): tinku
// speaks CSIL-RPC over HTTP, so upstream's events and datagrams modules are
// not vendored.
//
// Do not hand-edit these files. Re-copy them from upstream so
// conformance.test.ts keeps passing against the shared vectors.
export * from "./cbor.ts";
export * from "./conventions.ts";
export * from "./carrier.ts";
export * from "./rpc.ts";
