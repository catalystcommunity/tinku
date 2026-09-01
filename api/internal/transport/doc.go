// Package transport is a VENDORED copy of the canonical CSIL-RPC reference
// transport, github.com/catalystcommunity/csilgen/transports/go.
//
// It is copied here verbatim (cbor.go, conventions.go, carrier.go, rpc.go
// and the conformance/ vectors) rather than pulled in as a module
// dependency, so the build stays hermetic while the upstream module
// stabilizes — the same call longhouse and firepit made. When the module is
// published and CI can fetch it, replace this directory with a normal
// require + import and drop the local conformance test.
//
// Trimmed to the RPC envelopes only (cbor.go / conventions.go / carrier.go /
// rpc.go). Tinku serves CSIL-RPC over the HTTP carrier, so upstream's
// events.go, datagrams.go and udp.go are not vendored.
//
// Do not hand-edit the copied files. Re-copy them from upstream so the
// conformance vectors keep passing on both ends of the wire.
package transport
