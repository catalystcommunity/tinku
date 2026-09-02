#!/usr/bin/env bash
#
# Tinku task runner. Run it from any directory.
#
#   ./tools.sh gen                # csilgen -> api/internal/csil, webapp/src/gen, clients/
#   ./tools.sh build              # go build (api + coredb) + npm run build (webapp)
#   ./tools.sh test               # go test (api + coredb) + npm test (webapp)
#   ./tools.sh lint               # go vet (api + coredb) + tsc --noEmit (webapp)
#   ./tools.sh migrate [up|down|status]
#   ./tools.sh serve-local        # api on SQLite, no docker, dev auth on
#   ./tools.sh admin grant NAME@example.test   # make a dev user an admin
#   ./tools.sh dev                # the full stack, seeded: devadmin + devuser
#   ./tools.sh dev-down           # stop it again
#   ./tools.sh dev-web            # vite dev server against a local api
#   ./tools.sh build-images       # build the deployable container images
#   ./tools.sh site build         # build the marketing site in website/
#
# See README.md for the architecture these verbs operate on.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

GREEN=$'\e[0;32m'
RED=$'\e[0;31m'
NC=$'\e[0m'

log_status() {
    echo "${GREEN}--------------------------------  ${1}  --------------------------------${NC}"
}
err() {
    echo "${RED}$*${NC}" >&2
}

# LOCAL_DB_URI is the SQLite database `serve-local` and `migrate` use when
# nothing else is set. It needs no server, so a first run is one command.
LOCAL_DB_URI="sqlite:${SCRIPT_DIR}/local.db"

# COMPOSE_DB_URI reaches the compose PostgreSQL from the host. It matches
# docker-compose.yaml, and `dev` seeds through it.
COMPOSE_DB_URI="postgresql://tinku:devpass123@localhost:5432/tinku_db?sslmode=disable"

usage() {
    cat <<EOF
Usage: ./tools.sh <command>
       ./tools.sh help

Commands:
  gen                Regenerate everything derived from csil/tinku.csil
  build              Build the api, coredb and the webapp
  test               Run every test: go test (api, coredb) + npm test (webapp)
  test-go            Go tests only
  test-web           Webapp tests only
  test-pg            The Go tests against the compose Postgres, not SQLite
  lint               go vet (api, coredb) + tsc --noEmit (webapp) +
                     ruff (the CI plugin)
  lint-go            go vet only
  lint-web           tsc --noEmit only
  lint-ci            ruff over .reactorcide/plugins only
  migrate [verb]     Run goose against \$TINKU_DB_URI (default: the local
                     SQLite database). verb is up, down or status.
  serve-local        Run the api on SQLite with dev auth enabled. No docker,
                     no Postgres, no linkkeys.
  admin [verb]       The global admin role, against \$TINKU_DB_URI. verb is
                     list, grant <handle@domain> or revoke <handle@domain>.
                     Granting the first admin is only possible here.
  dev, dev-up        Start the full stack with docker compose: PostgreSQL,
                     the api and the web client. Waits for readiness, seeds
                     the development accounts, then prints the addresses.
  dev-down           Stop the stack. Add --volumes to drop the database.
  dev-logs [svc]     Follow the stack's logs. svc is postgres, api or webapp.
  dev-seed           Make the development accounts against \$TINKU_DB_URI:
                     devadmin, which is an administrator, and devuser, which
                     is not. Idempotent. Run it after 'serve-local' too.
  dev-web            Run the vite dev server, proxying to a local api
  build-images       Build the deployable container images
  site [verb]        The marketing site in website/. verb is build or
                     preview. See website/tools.sh.
  help               Show this help

Environment:
  TINKU_DB_URI     Database URI. Its scheme picks the backend and the
                     migration tree. Defaults per command; see above.
EOF
}

cmd_gen() {
    log_status "gen"

    local SPEC="$SCRIPT_DIR/csil/tinku.csil"
    local GO_SERVER_OUT="$SCRIPT_DIR/api/internal/csil"
    local TS_CLIENT_OUT="$SCRIPT_DIR/webapp/src/gen"
    local CLIENTS_OUT="$SCRIPT_DIR/clients"
    local VERSION
    VERSION="$(tr -d '[:space:]' < "$SCRIPT_DIR/version/VERSION.txt")"

    # A non-interactive shell does not always carry these user install paths.
    [ -d "$HOME/.local/bin" ] && export PATH="$HOME/.local/bin:$PATH"
    [ -d "$HOME/.cargo/bin" ] && export PATH="$HOME/.cargo/bin:$PATH"

    if ! command -v csilgen >/dev/null 2>&1; then
        err "csilgen is not on PATH (looked in \$PATH, ~/.local/bin, ~/.cargo/bin)"
        err "install it from the catalystcommunity/csilgen repo: ./tools.sh install-all there"
        exit 1
    fi

    log_status "gen: validate + lint"
    csilgen validate --input "$SPEC"
    # csilgen reports style warnings for this schema's naming conventions
    # (PascalCase service names, `;;;` doc comments). They are the house
    # style every CSIL schema in this organization uses; the exit code still
    # reports real errors.
    csilgen lint "$SCRIPT_DIR/csil"

    # ---- Go server surface (api/internal/csil) ----
    # The bare `go` target emits types plus service interfaces and no
    # emit_packages: this is in-tree server code the api module compiles,
    # not a standalone package. package_name is set in the spec's options
    # block, so nothing needs renaming afterwards.
    log_status "gen: go server -> api/internal/csil"
    mkdir -p "$GO_SERVER_OUT"
    csilgen generate --input "$SPEC" --target go --output "$GO_SERVER_OUT"
    command -v gofmt >/dev/null 2>&1 && gofmt -w "$GO_SERVER_OUT"

    # ---- TypeScript client surface (webapp/src/gen) ----
    # typescript-client emits types plus a typed client and no server
    # surface. In-tree, imported directly by the webapp, so no package.json
    # or tsconfig here (unlike clients/typescript below).
    log_status "gen: typescript-client -> webapp/src/gen"
    mkdir -p "$TS_CLIENT_OUT"
    csilgen generate --input "$SPEC" --target typescript-client --output "$TS_CLIENT_OUT" --readme-csil-rpc

    # ---- Standalone, publishable clients (clients/{go,typescript}) ----
    # Each gets its own options block spliced onto the shared spec, because
    # the package coordinates are per-language and emit_packages must NOT
    # leak into the two in-tree generations above, which share $SPEC
    # unmodified.
    log_status "gen: standalone clients -> clients/{go,typescript}"

    # The spliced specs must sit beside tinku.csil rather than in /tmp, or
    # their relative `include "types/...csil"` paths stop resolving —
    # csilgen resolves an include relative to the including file's own
    # directory. Removed unconditionally on return.
    local TMPDIR="$SCRIPT_DIR/csil"
    _cleanup_spliced() { rm -f "$TMPDIR"/.gen-tmp-*.csil; }
    trap _cleanup_spliced RETURN

    _splice_options() {
        # $1 = extra options (comma-joined, no trailing comma), $2 = output path
        #
        # CSIL's grammar is `*import_statement [options_block] *rule`, so the
        # options block must come AFTER the includes. This replaces the
        # spec's existing block in place rather than prepending a new one,
        # which would parse as an options block followed by more imports —
        # a grammar violation.
        awk -v extra="$1" -v version="$VERSION" '
            /^options[[:space:]]*\{/ { in_opts=1
                print "options {"
                print "    package: \"tinku\","
                print "    version: \"v1alpha\","
                print "    package_version: \"" version "\","
                print "    " extra
                print "}"
                next
            }
            in_opts && /^}/ { in_opts=0; next }
            in_opts { next }
            { print }
        ' "$SPEC" > "$2"
    }

    # go_package is deliberately omitted: its last path segment becomes the
    # Go package name, and this module's last segment is "go" — a reserved
    # word, so `package go` does not parse. package_name alone names it
    # tinkuclient instead.
    local go_spec="$TMPDIR/.gen-tmp-go-client.csil"
    _splice_options 'emit_packages: ["go"], package_name: "tinkuclient", go_module: "github.com/catalystcommunity/tinku/clients/go"' "$go_spec"
    rm -rf "$CLIENTS_OUT/go"
    mkdir -p "$CLIENTS_OUT/go"
    csilgen generate --input "$go_spec" --target go-client --output "$CLIENTS_OUT/go" --readme-csil-rpc
    command -v gofmt >/dev/null 2>&1 && gofmt -w "$CLIENTS_OUT/go"

    local ts_spec="$TMPDIR/.gen-tmp-ts-client.csil"
    _splice_options 'emit_packages: ["typescript"], package_name: "@tinku/client"' "$ts_spec"
    rm -rf "$CLIENTS_OUT/typescript"
    mkdir -p "$CLIENTS_OUT/typescript"
    csilgen generate --input "$ts_spec" --target typescript-client --output "$CLIENTS_OUT/typescript" --readme-csil-rpc

    log_status "gen: done"
    echo "  api/internal/csil:   $(ls "$GO_SERVER_OUT"/*.gen.go 2>/dev/null | wc -l) file(s)"
    echo "  webapp/src/gen:      $(ls "$TS_CLIENT_OUT"/*.gen.ts 2>/dev/null | wc -l) file(s)"
    echo "  clients/go:          $(find "$CLIENTS_OUT/go" -type f 2>/dev/null | wc -l) file(s)"
    echo "  clients/typescript:  $(find "$CLIENTS_OUT/typescript" -type f 2>/dev/null | wc -l) file(s)"
}

cmd_build() {
    log_status "go build (api)"
    ( cd "$SCRIPT_DIR/api" && go build ./... )

    log_status "go build (coredb)"
    ( cd "$SCRIPT_DIR/coredb" && go build ./... )

    log_status "npm run build (webapp)"
    ( cd "$SCRIPT_DIR/webapp" && npm run build )

    log_status "build passed"
}

cmd_test_go() {
    log_status "go test ./... (api)"
    ( cd "$SCRIPT_DIR/api" && go test ./... )

    log_status "go test ./... (coredb)"
    ( cd "$SCRIPT_DIR/coredb" && go test ./... )
}

cmd_test_web() {
    log_status "npm test (webapp)"
    ( cd "$SCRIPT_DIR/webapp" && npm test )
}

# The SAME end-to-end suite, against the backend production uses.
#
# store/postgres and store/sqlite hold their own SQL, so a query is only
# exercised on the backend the tests happen to run on. Two bugs have already
# reached this repository that way: an argument list Postgres rejected and
# SQLite silently misbound, and a set of table renames. Run this before
# trusting a change to either backend.
cmd_test_pg() {
    local PG_URI="postgresql://tinku:devpass123@localhost:5432/tinku_db?sslmode=disable"

    log_status "test-pg: starting the compose Postgres"
    docker compose up -d postgres

    # The container reports Started before the server accepts connections.
    log_status "test-pg: waiting for Postgres"
    local ready=""
    for _ in $(seq 1 30); do
        if docker compose exec -T postgres pg_isready -U tinku >/dev/null 2>&1; then
            ready=yes
            break
        fi
        sleep 1
    done
    if [ -z "$ready" ]; then
        err "Postgres did not accept connections within 30 seconds"
        exit 1
    fi

    log_status "test-pg: go test ./... (api) against Postgres"
    ( cd "$SCRIPT_DIR/api" && TINKU_TEST_DB_URI="$PG_URI" go test -count=1 ./... )

    log_status "test-pg: passed"
    log_status "test-pg: the container is still up; stop it with ./tools.sh dev-down"
}

# cmd_dev_down stops the stack. It keeps the database volume unless the
# caller asks for --volumes: a dropped volume is a dropped afternoon, and
# `docker compose down` on its own does not warn.
cmd_dev_down() {
    require_docker "./tools.sh dev-down"
    log_status "dev-down"
    ( cd "$SCRIPT_DIR" && docker compose down "$@" )
}

cmd_test() {
    cmd_test_go
    cmd_test_web
    log_status "all tests passed"
}

cmd_lint_go() {
    log_status "go vet (api)"
    ( cd "$SCRIPT_DIR/api" && go vet ./... )

    log_status "go vet (coredb)"
    ( cd "$SCRIPT_DIR/coredb" && go vet ./... )
}

cmd_lint_web() {
    log_status "tsc --noEmit (webapp)"
    ( cd "$SCRIPT_DIR/webapp" && npm run typecheck )
}

# The CI plugin is Python and runs only in CI, so nothing else would ever
# catch a name that does not exist. One did: a refactor left `home`
# referenced in the deploy job after the binding moved into a helper, every
# local check passed, and the job died in production having already pushed
# the image. `ruff --select F` is pyflakes' rule set — undefined names,
# unused imports — which is exactly the class a test suite cannot reach for
# code no test imports.
cmd_lint_ci() {
    log_status "ruff (.reactorcide/plugins)"
    if ! command -v uv >/dev/null 2>&1; then
        err "uv is not installed; skipping the CI plugin check. See https://docs.astral.sh/uv/"
        return 0
    fi
    ( cd "$SCRIPT_DIR" && uv tool run ruff check --select F .reactorcide/plugins )
}

cmd_lint() {
    cmd_lint_go
    cmd_lint_web
    cmd_lint_ci
    log_status "lint passed"
}

cmd_migrate() {
    log_status "migrate"
    local verb="${2:-up}"
    case "$verb" in
        up|down|status) ;;
        *) err "unknown migrate verb: $verb (expected up, down or status)"; exit 1 ;;
    esac
    ( cd "$SCRIPT_DIR/coredb" && TINKU_DB_URI="${TINKU_DB_URI:-$LOCAL_DB_URI}" go run ./cmd/migrate "$verb" )
}

# serve-local is the shortest path from a clone to a running API: SQLite so
# nothing has to be installed, dev auth so somebody can log in without a
# linkkeys relying party, and an insecure cookie because it serves plain
# HTTP on localhost.
cmd_serve_local() {
    log_status "serve-local"
    echo "  api:  http://localhost:5080${NC}"
    echo "  ops:  http://localhost:9090/metrics, /healthz, /readyz"
    echo "  db:   ${TINKU_DB_URI:-$LOCAL_DB_URI}"
    ( cd "$SCRIPT_DIR/api" && go run . serve \
        --db-uri="${TINKU_DB_URI:-$LOCAL_DB_URI}" \
        --env=dev --dev-auth --session-cookie-insecure )
}

require_docker() {
    command -v docker >/dev/null 2>&1 || { err "docker is required for $1"; exit 1; }
}

# cmd_dev_seed makes the two accounts a person signs in as locally. There is
# no password to set: development sign-in is devauth.dev-login, which takes
# a handle and a domain and carries no credential at all.
cmd_dev_seed() {
    local uri="${1:-${TINKU_DB_URI:-$LOCAL_DB_URI}}"
    # A person who runs this before anything has touched the local database
    # meets "no such table: users", which says nothing. The compose stack
    # migrates at boot, so only the local file needs this.
    if [ -z "${1:-}" ]; then
        ( cd "$SCRIPT_DIR/coredb" && TINKU_DB_URI="$uri" go run ./cmd/migrate up >/dev/null )
    fi
    log_status "dev-seed"
    ( cd "$SCRIPT_DIR/api" && go run . dev-seed --db-uri="$uri" --env=dev )
}

# cmd_dev starts the stack and waits for it, so the command finishes when the
# stack is usable rather than when docker has accepted the request. The api
# is not ready while it migrates, and seeding into a database that has no
# schema yet fails. `dev-up` is the same command under the name people
# reach for.
cmd_dev() {
    log_status "dev"
    require_docker "./tools.sh dev"
    ( cd "$SCRIPT_DIR" && docker compose up -d --build "$@" )

    printf '  waiting for the api'
    local i
    for i in $(seq 1 60); do
        if curl -fsS -o /dev/null http://localhost:9090/readyz 2>/dev/null; then
            echo
            cmd_dev_seed "$COMPOSE_DB_URI"
            log_status "dev: ready"
            echo "  web client: http://localhost:8080"
            echo "  api:        http://localhost:5080"
            echo "  ops:        http://localhost:9090/metrics, /healthz, /readyz"
            echo "  postgres:   $COMPOSE_DB_URI"
            echo
            echo "  sign in as devadmin (an administrator) or devuser, domain example.test"
            echo "  logs: ./tools.sh dev-logs      stop: ./tools.sh dev-down"
            return 0
        fi
        printf '.'
        sleep 2
    done
    echo
    err "the api did not become ready. Its log:"
    ( cd "$SCRIPT_DIR" && docker compose logs --tail 30 api )
    exit 1
}

cmd_dev_logs() {
    require_docker "./tools.sh dev-logs"
    ( cd "$SCRIPT_DIR" && docker compose logs -f "$@" )
}

# ensure_web_deps installs the client's dependencies when they are absent.
# A person who has just cloned the repository has no node_modules, and a
# command that fails with "vite: not found" tells them nothing.
ensure_web_deps() {
    if [ ! -d "$SCRIPT_DIR/webapp/node_modules" ]; then
        log_status "npm install (webapp)"
        ( cd "$SCRIPT_DIR/webapp" && npm install )
    fi
}

cmd_dev_web() {
    ensure_web_deps
    log_status "dev-web"
    echo "  web client: http://localhost:8080"
    echo "  proxying /csil and /auth to http://localhost:5080 (run './tools.sh serve-local' in another shell)"
    ( cd "$SCRIPT_DIR/webapp" && npm run dev )
}

# cmd_admin bootstraps the global admin role. The first admin cannot be
# granted through the API, because granting needs the role.
cmd_admin() {
    local verb="${2:-list}"
    case "$verb" in
        list|grant|revoke) ;;
        *) err "unknown admin verb: $verb (expected list, grant or revoke)"; exit 1 ;;
    esac
    if [ "$verb" != "list" ] && [ -z "${3:-}" ]; then
        err "admin $verb needs an address, for example: ./tools.sh admin $verb ada@example.test"
        exit 1
    fi
    log_status "admin $verb"
    ( cd "$SCRIPT_DIR/api" && go run . admin "$verb" "${3:-}" \
        --db-uri="${TINKU_DB_URI:-$LOCAL_DB_URI}" )
}

cmd_build_images() {
    log_status "build-images"
    command -v docker >/dev/null 2>&1 || { err "docker is required for './tools.sh build-images'"; exit 1; }

    local version sha tag
    version="$(tr -d '[:space:]' < "$SCRIPT_DIR/version/VERSION.txt")"
    sha="$(git -C "$SCRIPT_DIR" rev-parse --short HEAD 2>/dev/null || echo "nogit")"
    tag="${version}-${sha}"

    # Both Dockerfiles need the repository root as their build context: the
    # api image reaches the sibling coredb module, and the webapp image
    # reaches version/.
    log_status "build-images: api -> tinku/api:${tag}"
    docker build -f "$SCRIPT_DIR/api/Dockerfile" \
        -t "tinku/api:${tag}" -t "tinku/api:dev" "$SCRIPT_DIR"

    log_status "build-images: webapp -> tinku/webapp:${tag}"
    docker build -f "$SCRIPT_DIR/webapp/Dockerfile" \
        -t "tinku/webapp:${tag}" -t "tinku/webapp:dev" "$SCRIPT_DIR"

    log_status "build-images: done"
    echo "  tinku/api:${tag} (+ :dev)"
    echo "  tinku/webapp:${tag} (+ :dev)"
}

case "${1:-}" in
    gen)               cmd_gen ;;
    build)             cmd_build ;;
    test)              cmd_test ;;
    test-go)           cmd_test_go ;;
    test-web)          cmd_test_web ;;
    test-pg)           cmd_test_pg ;;
    lint)              cmd_lint ;;
    lint-go)           cmd_lint_go ;;
    lint-web)          cmd_lint_web ;;
    lint-ci)           cmd_lint_ci ;;
    migrate)           cmd_migrate "$@" ;;
    serve-local)       cmd_serve_local ;;
    admin)             cmd_admin "$@" ;;
    dev|dev-up)        shift; cmd_dev "$@" ;;
    dev-logs)          shift; cmd_dev_logs "$@" ;;
    dev-seed)          cmd_dev_seed ;;
    dev-web)           cmd_dev_web ;;
    dev-down)          shift; cmd_dev_down "$@" ;;
    build-images)      cmd_build_images ;;
    site)              shift; "$SCRIPT_DIR/website/tools.sh" "$@" ;;
    ""|-h|--help|help) usage ;;
    *)
        err "unknown command: $1"
        usage
        exit 1
        ;;
esac
