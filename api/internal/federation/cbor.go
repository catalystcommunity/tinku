package federation

import "encoding/binary"

// A minimal canonical CBOR encoder for exactly one shape this package
// needs: a two-element definite-length array `[tag: tstr, payload: bstr]`,
// RFC 8949 shortest-form length encoding. This is the same envelope shape
// linkkeys' own signed structures use (see
// crates/liblinkkeys/src/local_rp.rs's `envelope_signature_input`, ported
// to Go in sdks/regular-rp/go/cbor.go's `envelopeSignatureInput`) — reused
// here, under Tinku's OWN tag (see BatchSignatureTag in verify.go), for
// exactly the same reason linkkeys uses it: a signature must cover a tag
// AND the payload it was made for, so a signature made under one context
// cannot be replayed as though it were made under another.
//
// This file does not attempt to be a general CBOR encoder. csil's own
// generated codec (api/internal/csil/codec.gen.go) already owns that for
// every CSIL wire type; this is the one non-wire-type construction this
// package signs.

func cborHead(major byte, n uint64) []byte {
	m := major << 5
	switch {
	case n < 24:
		return []byte{m | byte(n)}
	case n <= 0xff:
		return []byte{m | 24, byte(n)}
	case n <= 0xffff:
		b := make([]byte, 3)
		b[0] = m | 25
		binary.BigEndian.PutUint16(b[1:], uint16(n))
		return b
	case n <= 0xffffffff:
		b := make([]byte, 5)
		b[0] = m | 26
		binary.BigEndian.PutUint32(b[1:], uint32(n))
		return b
	default:
		b := make([]byte, 9)
		b[0] = m | 27
		binary.BigEndian.PutUint64(b[1:], n)
		return b
	}
}

// signatureInput builds `CBOR([tag, payload])`, the bytes a detached
// signature actually covers. Not a concatenation: a two-element array,
// matching linkkeys' own envelope signature input exactly, so the same
// domain-separation reasoning applies here under Tinku's own tag.
func signatureInput(tag string, payload []byte) []byte {
	head := cborHead(3, uint64(len(tag))) // major type 3: text string
	tagItem := append(head, []byte(tag)...)

	bhead := cborHead(2, uint64(len(payload))) // major type 2: byte string
	payloadItem := append(bhead, payload...)

	out := cborHead(4, 2) // major type 4: array of 2 items
	out = append(out, tagItem...)
	out = append(out, payloadItem...)
	return out
}
