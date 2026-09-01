// Package coredb owns tinku's database schema. It embeds the goose
// migrations for both supported dialects and exposes Open, Up, Reset and
// Status, so the api (`tinku serve` runs migrations before it reports
// ready) and the standalone migrate CLI backing `./tools.sh migrate` apply
// the same migrations the same way.
//
// The schema lives in migrations/postgres/ and migrations/sqlite/ — the same
// logical schema written in each dialect's own SQL. ParseTarget (dburi.go)
// decides which tree a given database URI gets.
package coredb

import "embed"

//go:embed all:migrations
var Migrations embed.FS
