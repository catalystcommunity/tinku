package cmd

import (
	"fmt"

	log "github.com/sirupsen/logrus"

	"github.com/catalystcommunity/tinku/api/internal/config"
	"github.com/catalystcommunity/tinku/coredb"
)

// Migrate implements `tinku migrate [up|down|status]`, defaulting to up.
// It goes through the same coredb entry points `serve` uses, so a migration
// applied by hand and one applied at boot are the same operation.
func Migrate(args []string, flags map[string]string) error {
	cfg := config.Load(flags)

	verb := "up"
	// args[0] is "migrate"; anything after it that is not a flag is the verb.
	for _, arg := range args[1:] {
		if len(arg) > 0 && arg[0] != '-' {
			verb = arg
			break
		}
	}

	switch verb {
	case "up":
		log.WithField("db_uri", redact(cfg.DBURI)).Info("applying migrations")
		if err := coredb.Up(cfg.DBURI); err != nil {
			return err
		}
		log.Info("migrations applied")
		return nil
	case "down":
		log.WithField("db_uri", redact(cfg.DBURI)).Warn("rolling every migration back to version zero")
		if err := coredb.Reset(cfg.DBURI); err != nil {
			return err
		}
		log.Info("migrations rolled back")
		return nil
	case "status":
		return coredb.Status(cfg.DBURI)
	default:
		return fmt.Errorf("unknown migrate verb %q: expected up, down or status", verb)
	}
}
