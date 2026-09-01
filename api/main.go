// Command tinku is the tinku API. `tinku serve` runs it as a service;
// `tinku migrate` applies the database migrations on their own.
//
// The subcommand shape matches the other Go APIs in this organization
// (longhouse's api/cmd, for one): a thin main that delegates to cmd.Run,
// which parses flags and dispatches.
package main

import (
	"fmt"
	"os"

	"github.com/catalystcommunity/tinku/api/cmd"
)

func main() {
	if err := cmd.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
