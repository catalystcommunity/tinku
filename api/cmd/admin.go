package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/catalystcommunity/tinku/api/internal/config"
	"github.com/catalystcommunity/tinku/api/internal/store"
	_ "github.com/catalystcommunity/tinku/api/internal/store/backends"
)

// Admin manages the global admin role from the command line.
//
// This subcommand exists for one reason: the FIRST admin cannot be granted
// through AdminService, because granting requires the role and nobody has
// it yet. Everything here is also possible over CSIL-RPC once one admin
// exists — this is the bootstrap, not a parallel interface.
//
//	tinku admin list
//	tinku admin grant  <handle@domain>
//	tinku admin revoke <handle@domain>
func Admin(args []string, flags map[string]string) error {
	cfg := config.Load(flags)
	action, address := adminArgs(args)

	st, err := store.Open(cfg.DBURI)
	if err != nil {
		return fmt.Errorf("opening the database: %w", err)
	}
	defer st.Close() //nolint:errcheck // process is exiting

	ctx := context.Background()
	switch action {
	case "list", "":
		return listAdmins(ctx, st)
	case "grant":
		return setAdmin(ctx, st, address, true)
	case "revoke":
		return setAdmin(ctx, st, address, false)
	default:
		fmt.Fprintf(os.Stderr, "unknown admin action: %s\n\n", action)
		fmt.Print(usage)
		return fmt.Errorf("unknown admin action: %s", action)
	}
}

// adminArgs reads the action and address that follow the "admin" word.
func adminArgs(args []string) (action, address string) {
	rest := []string{}
	seenCommand := false
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if !seenCommand {
			seenCommand = true // the "admin" word itself
			continue
		}
		rest = append(rest, arg)
	}
	if len(rest) > 0 {
		action = rest[0]
	}
	if len(rest) > 1 {
		address = rest[1]
	}
	return action, address
}

func listAdmins(ctx context.Context, st store.Store) error {
	admins, err := st.ListAdmins(ctx)
	if err != nil {
		return fmt.Errorf("listing administrators: %w", err)
	}
	if len(admins) == 0 {
		fmt.Println("no administrators. Grant one with: tinku admin grant <handle@domain>")
		return nil
	}
	for _, a := range admins {
		fmt.Printf("%s@%s\t%s\n", a.Handle, a.LinkkeysDomain, a.DisplayName)
	}
	return nil
}

// setAdmin resolves a federated address and flips the role.
//
// The address must already name somebody this node knows: a user row is
// created by a successful login, and granting the role to an address that
// has never logged in would create an account nobody can use.
func setAdmin(ctx context.Context, st store.Store, address string, granted bool) error {
	handle, domain, found := strings.Cut(address, "@")
	if !found || handle == "" || domain == "" {
		return fmt.Errorf("expected an address like handle@domain, got %q", address)
	}

	user, err := st.UserByHandle(ctx, strings.ToLower(handle), strings.ToLower(domain))
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("nobody here has the address %s: they have to log in once first", address)
	}
	if err != nil {
		return fmt.Errorf("looking up %s: %w", address, err)
	}

	// Revoking the last admin would leave a deployment with no way to
	// delete an organization and no way to make an admin again over the API. The
	// same rule the service enforces applies here.
	if !granted {
		admins, err := st.ListAdmins(ctx)
		if err != nil {
			return fmt.Errorf("counting administrators: %w", err)
		}
		if len(admins) <= 1 && user.IsAdmin {
			return errors.New("this is the last administrator; grant another one before revoking this one")
		}
	}

	if err := st.SetAdmin(ctx, user.ID, granted); err != nil {
		return fmt.Errorf("changing the administrator role: %w", err)
	}
	verb := "granted to"
	if !granted {
		verb = "revoked from"
	}
	fmt.Printf("administrator %s %s@%s\n", verb, user.Handle, user.LinkkeysDomain)
	return nil
}
