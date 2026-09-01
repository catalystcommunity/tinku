// Command migrate is the CLI backing `./tools.sh migrate`: it runs goose
// up/down/status against a database URI, embedding coredb's migrations for
// whichever dialect that URI names.
//
// `tinku migrate` (api/cmd/migrate.go) does the same thing through the
// same coredb entry points. This binary exists so the migrations can be run
// without building the api — a release pipeline that applies migrations as
// its own step wants the small binary, not the server.
package main

import (
	"fmt"
	"os"

	"github.com/catalystcommunity/tinku/coredb"
)

// defaultDBURI matches the postgres service in the repo-root
// docker-compose.yaml, for local `./tools.sh dev` usage.
const defaultDBURI = "postgresql://tinku:devpass123@localhost:5432/tinku_db?sslmode=disable"

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help") {
		usage(os.Stdout)
		return
	}
	if len(os.Args) != 2 {
		usage(os.Stderr)
		os.Exit(1)
	}

	dbURI := os.Getenv("TINKU_DB_URI")
	if dbURI == "" {
		dbURI = os.Getenv("DB_URI")
	}
	if dbURI == "" {
		dbURI = defaultDBURI
	}

	var err error
	switch verb := os.Args[1]; verb {
	case "up":
		err = coredb.Up(dbURI)
	case "down":
		err = coredb.Reset(dbURI)
	case "status":
		err = coredb.Status(dbURI)
	default:
		usage(os.Stderr)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage(w *os.File) {
	fmt.Fprintln(w, "Usage: migrate <up|down|status>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run tinku database migrations against TINKU_DB_URI (or DB_URI).")
	fmt.Fprintln(w, "If neither is set, use the local Docker Compose database.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "The URI's scheme selects the dialect and the migration tree:")
	fmt.Fprintln(w, "  postgresql://user:pass@host:5432/db   migrations/postgres")
	fmt.Fprintln(w, "  sqlite:./dev.db                       migrations/sqlite")
}
