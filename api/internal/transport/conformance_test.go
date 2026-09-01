// Verify the vendored RPC codec against the checked-in conformance vectors.
//
// This guards the vendored encoders/decoders against drift from upstream
// (github.com/catalystcommunity/csilgen/transports/go): reconstruct each
// vector's envelope from its language-neutral `input`, assert encode → `hex`,
// and assert decode(`hex`) round-trips. Trimmed to the RPC vectors only — this
// directory vendors the RPC envelopes, not events/datagrams.
package transport

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type vectorEntry struct {
	Name  string         `json:"name"`
	Hex   string         `json:"hex"`
	Input map[string]any `json:"input"`
}

type vectorFile struct {
	Vectors []vectorEntry `json:"vectors"`
}

func loadRPCVectors(t *testing.T) []vectorEntry {
	t.Helper()
	path := filepath.Join("conformance", "rpc.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc vectorFile
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc.Vectors
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	if b == nil {
		b = []byte{}
	}
	return b
}

func optStr(m map[string]any, k string) *string {
	v, ok := m[k]
	if !ok || v == nil {
		return nil
	}
	s := v.(string)
	return &s
}

func optU64(m map[string]any, k string) *uint64 {
	v, ok := m[k]
	if !ok || v == nil {
		return nil
	}
	n := uint64(v.(float64))
	return &n
}

func reqStr(t *testing.T, m map[string]any, k string) string {
	t.Helper()
	v, ok := m[k]
	if !ok || v == nil {
		t.Fatalf("missing required string field %q", k)
	}
	return v.(string)
}

func reqI64(t *testing.T, m map[string]any, k string) int64 {
	t.Helper()
	v, ok := m[k]
	if !ok || v == nil {
		t.Fatalf("missing required integer field %q", k)
	}
	return int64(v.(float64))
}

func TestRpcVectors(t *testing.T) {
	for _, vec := range loadRPCVectors(t) {
		in := vec.Input
		var actual []byte
		switch kind := reqStr(t, in, "kind"); kind {
		case "request":
			r := NewRpcRequest(reqStr(t, in, "service"), reqStr(t, in, "op"), unhex(t, reqStr(t, in, "payload_hex")))
			r.ID = optU64(in, "id")
			r.Auth = optStr(in, "auth")
			b, err := r.Encode()
			if err != nil {
				t.Fatalf("%s encode: %v", vec.Name, err)
			}
			dec, err := DecodeRpcRequest(b)
			if err != nil {
				t.Fatalf("%s decode: %v", vec.Name, err)
			}
			if !reflect.DeepEqual(dec, r) {
				t.Errorf("%s decode mismatch: got %+v want %+v", vec.Name, dec, r)
			}
			actual = b
		case "response":
			r := RpcResponse{
				ID:      optU64(in, "id"),
				Status:  StatusFromCode(reqI64(t, in, "status")),
				Variant: optStr(in, "variant"),
				Error:   optStr(in, "error"),
				Payload: unhex(t, reqStr(t, in, "payload_hex")),
			}
			b, err := r.Encode()
			if err != nil {
				t.Fatalf("%s encode: %v", vec.Name, err)
			}
			dec, err := DecodeRpcResponse(b)
			if err != nil {
				t.Fatalf("%s decode: %v", vec.Name, err)
			}
			if !reflect.DeepEqual(dec, r) {
				t.Errorf("%s decode mismatch: got %+v want %+v", vec.Name, dec, r)
			}
			actual = b
		case "push":
			p := NewRpcPush(reqStr(t, in, "service"), reqStr(t, in, "event"), unhex(t, reqStr(t, in, "payload_hex")))
			b, err := p.Encode()
			if err != nil {
				t.Fatalf("%s encode: %v", vec.Name, err)
			}
			dec, err := DecodeRpcPush(b)
			if err != nil {
				t.Fatalf("%s decode: %v", vec.Name, err)
			}
			if !reflect.DeepEqual(dec, p) {
				t.Errorf("%s decode mismatch: got %+v want %+v", vec.Name, dec, p)
			}
			actual = b
		default:
			t.Fatalf("unknown rpc kind %q", kind)
		}
		if got := hex.EncodeToString(actual); got != vec.Hex {
			t.Errorf("%s encode mismatch:\n got %s\nwant %s", vec.Name, got, vec.Hex)
		}
	}
}
