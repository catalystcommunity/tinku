package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/federation"
)

// Federation holds bootstrap actions for the real (linkkeys application-key)
// federation signing scheme. Everything under `tinku serve` reads its
// keyring from configuration (TINKU_FEDERATION_SIGNING_KEYS); this is how
// that value gets made in the first place.
//
//	tinku federation generate-keys [--count=3] [--address=handle@domain]
func Federation(args []string, flags map[string]string) error {
	action := federationAction(args)
	switch action {
	case "generate-keys", "":
		return federationGenerateKeys(flags)
	default:
		fmt.Fprintf(os.Stderr, "unknown federation action: %s\n\n", action)
		fmt.Print(usage)
		return fmt.Errorf("unknown federation action: %s", action)
	}
}

func federationAction(args []string) string {
	seenCommand := false
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if !seenCommand {
			seenCommand = true // the "federation" word itself
			continue
		}
		return arg
	}
	return ""
}

// federationGenerateKeys generates a fresh local signing keyring and prints
// two things to two different streams, on purpose:
//
//   - stdout: the JSON secret (MarshalSecret's output) — the operator
//     redirects this straight into whatever secret store TINKU_FEDERATION_SIGNING_KEYS
//     will be read from. Never printed to a terminal an operator might
//     leave logged, and never mixed with anything else on stdout, so a
//     script can safely do `tinku federation generate-keys > keys.json`
//     without also capturing the messages below.
//   - stderr: the public key material (id, fingerprint) for each key —
//     what the operator pastes into the linkkeys enrollment step
//     (Account/enroll-application-instance), which is a human-supervised
//     action outside this command's own reach — see
//     api/internal/federation/enrollment.go's package doc.
func federationGenerateKeys(flags map[string]string) error {
	count := federation.RecommendedSigningKeys
	if raw, ok := flags["count"]; ok {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("--count must be a number, got %q", raw)
		}
		count = n
	}
	address := flags["address"]
	if address == "" {
		address = "tinku@example.test"
		fmt.Fprintf(os.Stderr,
			"no --address given; using the placeholder %q — pass --address=handle@domain for a real deployment\n",
			address)
	}

	kr, err := federation.NewKeyring(address, count, time.Now())
	if err != nil {
		return fmt.Errorf("generating a keyring: %w", err)
	}
	secret, err := kr.MarshalSecret()
	if err != nil {
		return fmt.Errorf("encoding the keyring: %w", err)
	}

	fmt.Fprintf(os.Stderr, "generated %d signing key(s) for %s:\n", len(kr.Keys()), address)
	for _, k := range kr.Keys() {
		fmt.Fprintf(os.Stderr, "  key_id=%s public_key=%s\n", k.KeyID, publicKeyHex(k.PublicKey))
	}
	fmt.Fprintln(os.Stderr,
		"\nThe JSON on stdout is a SECRET. Put it in your secret store as TINKU_FEDERATION_SIGNING_KEYS.\n"+
			"Do not commit it, log it, or leave it in shell history.\n"+
			"The public keys above still need to be enrolled at your linkkeys home domain\n"+
			"(Account/enroll-application-instance) before any peer can verify anything signed with them —\n"+
			"see docs/OPERATING.md, \"Federation signing keys\".")

	_, err = os.Stdout.Write(secret)
	if err != nil {
		return fmt.Errorf("writing the keyring: %w", err)
	}
	fmt.Fprintln(os.Stdout)
	return nil
}

func publicKeyHex(pub [32]byte) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, 64)
	for _, b := range pub {
		out = append(out, hexDigits[b>>4], hexDigits[b&0x0f])
	}
	return string(out)
}
