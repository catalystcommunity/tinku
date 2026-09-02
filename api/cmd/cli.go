// Package cmd is tinku's command-line surface: argument parsing and one
// file per subcommand.
package cmd

import (
	"fmt"
	"os"
	"strings"
)

const usage = `tinku - a CSIL-RPC hello-world service

Usage:
  tinku serve   [--db-uri=URI] [--api-port=PORT] [--ops-port=PORT] [--env=NAME]
                  [--origin-domain-override=DOMAIN]
                  [--skip-migrate] [--cors-origins=LIST] [--dev-auth]
                  [--session-cookie-insecure]
                  [--linkkeys-domain=DOMAIN] [--linkkeys-idp-domain=DOMAIN]
                  [--linkkeys-idp-url=URL] [--linkkeys-pki-url=URL]
                  [--linkkeys-pki-api-key=KEY] [--app-callback-url=URL]
                  [--post-login-redirect-url=URL]
  tinku migrate [--db-uri=URI] [up|down|status]
  tinku admin   [--db-uri=URI] [list|grant <handle@domain>|revoke <handle@domain>]
  tinku dev-seed [--db-uri=URI] [--domain=example.test] --env=dev
  tinku federation generate-keys [--count=3] [--address=handle@domain]
  tinku --help
  tinku --version

Commands:
  serve     Start the API. Applies migrations, then reports ready and listens.
  migrate   Apply, roll back, or report on the database migrations.
  admin     Grant, revoke or list the global administrator role. This is how
            the FIRST administrator is made: granting the role over the API
            needs the role, and at first nobody has it.
  dev-seed  Make the local development accounts: devadmin, which holds the
            administrator role, and devuser, which holds nothing. Idempotent,
            and refused unless --env is dev or nonprod. There is no password:
            development sign-in carries no credential.
  federation generate-keys
            Generate a local Ed25519 federation signing keyring (3 keys by
            default) and print it as JSON on stdout, for
            TINKU_FEDERATION_SIGNING_KEYS. The public keys still need
            enrolling at your linkkeys home domain before any peer can
            verify anything signed with them — see docs/OPERATING.md.

Options:
  --db-uri=URI             Database URI. Its scheme picks the backend AND the
                           migration tree: postgresql://... or sqlite:./dev.db
                           [default: the docker-compose Postgres]
                           [env: TINKU_DB_URI]
  --api-port=PORT          Port serving CSIL-RPC and the auth callback
                           [default: 5080] [env: TINKU_API_PORT]
  --ops-port=PORT          Port serving /metrics, /healthz and /readyz. A
                           separate listener from the API on purpose
                           [default: 9090] [env: TINKU_OPS_PORT]
  --env=NAME               Deployment: dev, nonprod or prod. Gates --dev-auth
                           [default: dev] [env: TINKU_ENV]
  --skip-migrate           Do not apply migrations at boot. serve still
                           refuses to report ready while any are pending
                           [env: TINKU_MIGRATE_ON_BOOT]
  --cors-origins=LIST      Comma-separated allowed origins [default: *]
                           [env: TINKU_CORS_ORIGINS]
  --dev-auth               Expose devauth.dev-login, which mints a session
                           with no identity assertion. Ignored unless --env is
                           dev or nonprod [env: TINKU_DEV_AUTH_ENABLED]
  --session-cookie-insecure
                           Drop the Secure attribute from the session cookie,
                           for a plain-HTTP local run
                           [env: TINKU_SESSION_COOKIE_SECURE]

Linkkeys (all four of the first organization are needed before login works):
  --linkkeys-idp-domain=DOMAIN  Identity domain whose assertions we trust
                                [env: TINKU_LINKKEYS_IDP_DOMAIN]
  --linkkeys-idp-url=URL        Base URL of the IDP authorize page
                                [env: TINKU_LINKKEYS_IDP_URL]
  --linkkeys-pki-url=URL        Relying-party sidecar base URL
                                [env: TINKU_LINKKEYS_PKI_URL]
  --app-callback-url=URL        Absolute URL of this API's /auth/callback,
                                which the signed request is bound to
                                [env: TINKU_APP_CALLBACK_URL]
  --linkkeys-domain=DOMAIN      Our own relying-party identity, the audience
                                on an assertion. Falls back to the IDP domain
                                [env: TINKU_LINKKEYS_DOMAIN]
  --linkkeys-pki-api-key=KEY    Relying-party API key
                                [env: TINKU_LINKKEYS_PKI_API_KEY]
  --post-login-redirect-url=URL Where /auth/callback sends the browser
                                [default: /] [env: TINKU_POST_LOGIN_REDIRECT_URL]

  --origin-domain-override=DOMAIN
                           Force this node's name instead of deriving it from
                           the linkkeys identity. For a local run with no
                           linkkeys configured, and nothing else
                           [default: the linkkeys domain, else localhost]
                           [env: TINKU_ORIGIN_DOMAIN]
  --federation             Exchange events with other instances. Needs
                           --federation-handle. Off by default
                           [env: TINKU_FEDERATION_ENABLED]
  --federation-handle=NAME The local part of this instance's own account.
                           The full address is NAME@origin-domain
                           [env: TINKU_FEDERATION_HANDLE]
  --public-base-url=URL    Where a reader reaches this instance. A delivery
                           carries a link back to it
                           [default: https:// + the origin domain]
                           [env: TINKU_PUBLIC_BASE_URL]
  --help                   Show this message
  --version                Show the version

Environment-only settings (no flag) are declared in
api/internal/config/config.go: TINKU_SESSION_TTL,
TINKU_SESSION_NONCE_SECRET, TINKU_LINKKEYS_PKI_ALLOW_INVALID_CERTS,
TINKU_FEDERATION_FAILURE_WINDOW, and TINKU_FEDERATION_POLL_INTERVAL.
`

// version is kept in step with version/VERSION.txt by the release job.
const version = "0.1.0"

// Run parses args and dispatches to a subcommand.
func Run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}

	// Flags may appear anywhere in the argument list, so --version and
	// --help are answered before a subcommand is looked for: `tinku
	// --version` has no command word and must still print the version.
	flags := parseFlags(args)

	if flags["version"] == "true" {
		fmt.Printf("tinku %s\n", version)
		return nil
	}

	command := findCommand(args)
	if flags["help"] == "true" || command == "" {
		fmt.Print(usage)
		return nil
	}

	switch command {
	case "serve":
		return Serve(flags)
	case "migrate":
		return Migrate(args, flags)
	case "admin":
		return Admin(args, flags)
	case "dev-seed":
		return DevSeed(args, flags)
	case "federation":
		return Federation(args, flags)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", command)
		fmt.Print(usage)
		return fmt.Errorf("unknown command: %s", command)
	}
}

// findCommand returns the first non-flag argument.
func findCommand(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

// parseFlags extracts --key=value and bare --flag arguments from anywhere in
// the list. A bare flag's value is "true", so `--dev-auth` and
// `--dev-auth=true` mean the same thing and `--dev-auth=false` still works.
func parseFlags(args []string) map[string]string {
	flags := map[string]string{}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		arg = strings.TrimLeft(arg, "-")
		if key, value, found := strings.Cut(arg, "="); found {
			flags[key] = value
		} else {
			flags[arg] = "true"
		}
	}
	return flags
}
