#!/usr/bin/env bash
#
# Marketing site task runner. Run it from any directory.
#
#   ./tools.sh build      # PySocha -> ../website/site
#   ./tools.sh preview    # PySocha's own preview server
#
# The repository root runner delegates here: `./tools.sh site build`.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

GREEN=$'\e[0;32m'
RED=$'\e[0;31m'
NC=$'\e[0m'

log_status() {
    echo "${GREEN}--------------------------------  ${1}  --------------------------------${NC}"
}
err() {
    echo "${RED}$*${NC}" >&2
}

# Pinned, like every other PySocha site here: a moving ref would rebuild the
# site differently without anything in this repository changing.
PYSOCHA_REF="18b2d6e704ba63e80f82d287b5831c1151c388a4"
PYSOCHA_SOURCE="git+https://github.com/catalystcommunity/pysocha.git@${PYSOCHA_REF}"

# `uv tool run` rather than the `uvx` alias. They are the same thing where
# both exist, but the CI runner image ships only the `uv` binary, and its
# musl build does not accept `--from` when invoked through a `uvx` symlink.
# The long form works everywhere.
pysocha() {
    if ! command -v uv >/dev/null 2>&1; then
        err "uv is not installed. See https://docs.astral.sh/uv/"
        exit 1
    fi
    cd "$SCRIPT_DIR/site-src"
    uv tool run --from "$PYSOCHA_SOURCE" pysocha "$@" --config-file config.yaml
}

usage() {
    cat <<EOF
Usage: ./website/tools.sh <command>

Commands:
  build      Build the site into website/site
  preview    Run the PySocha preview server
  help       Show this help
EOF
}

case "${1:-}" in
    build)
        log_status "website: build"
        pysocha build
        log_status "website: site/ written"
        ;;
    preview)
        pysocha preview
        ;;
    ""|-h|--help|help) usage ;;
    *)
        err "unknown command: $1"
        usage
        exit 1
        ;;
esac
