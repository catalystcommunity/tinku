package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/catalystcommunity/tinku/api/internal/config"
	"github.com/catalystcommunity/tinku/api/internal/store"
	_ "github.com/catalystcommunity/tinku/api/internal/store/backends"
)

// The two accounts a local stack starts with. devadmin holds the global
// administrator role, so that the rules only an administrator can reach —
// deleting an organization, deleting an event that started — can be tried
// without a second person. devuser holds nothing, which is what makes it
// useful: it is the account that shows what everybody else sees.
const (
	seedAdminHandle = "devadmin"
	seedUserHandle  = "devuser"

	// The domain the sign-in form suggests. A seeded account has to match
	// it, or a person types the handle, gets a different domain, and makes
	// a second empty account without noticing.
	seedDomain = "example.test"
)

// DevSeed makes the local accounts and grants the administrator role.
//
// There is no password to set. A local sign-in goes through
// devauth.dev-login, which takes a handle and a domain and mints a session
// with no identity assertion — there is no credential anywhere in the flow
// to give a value to. Sign in as `devadmin` to get the seeded administrator.
//
//	tinku dev-seed [--db-uri=URI] [--domain=example.test]
func DevSeed(args []string, flags map[string]string) error {
	cfg := config.Load(flags)

	// Env defaults to prod, so this refuses unless a person asks for dev.
	// The command creates accounts that anybody can then sign in as.
	switch strings.ToLower(cfg.Env) {
	case "dev", "development", "nonprod", "test":
	default:
		return fmt.Errorf(
			"dev-seed makes accounts anybody can sign in as; it refuses in env %q. Pass --env=dev",
			cfg.Env,
		)
	}

	domain := strings.ToLower(strings.TrimSpace(flags["domain"]))
	if domain == "" {
		domain = seedDomain
	}

	st, err := store.Open(cfg.DBURI)
	if err != nil {
		return fmt.Errorf("opening the database: %w", err)
	}
	defer st.Close() //nolint:errcheck // process is exiting

	ctx := context.Background()
	for _, handle := range []string{seedAdminHandle, seedUserHandle} {
		user, existed, err := seedUser(ctx, st, handle, domain)
		if err != nil {
			return err
		}
		state := "created"
		if existed {
			state = "already here"
		}

		if handle == seedAdminHandle && !user.IsAdmin {
			if err := st.SetAdmin(ctx, user.ID, true); err != nil {
				return fmt.Errorf("granting the administrator role to %s: %w", handle, err)
			}
			state += ", administrator granted"
		} else if handle == seedAdminHandle {
			state += ", administrator"
		}
		fmt.Printf("%s@%s\t%s\n", user.Handle, user.LinkkeysDomain, state)
	}

	fmt.Printf("\nSign in at the web client with the handle %s or %s, domain %s.\n",
		seedAdminHandle, seedUserHandle, domain)
	fmt.Println("There is no password: development sign-in carries no credential.")
	return nil
}

// seedUser upserts one account and reports whether it was already there.
//
// UpsertUser is the same call devauth.dev-login makes, with the same
// LinkkeysUserID shape, so a seeded account and one made by signing in are
// the same row. Seeding twice therefore changes nothing.
func seedUser(ctx context.Context, st store.Store, handle, domain string) (*store.User, bool, error) {
	_, err := st.UserByHandle(ctx, handle, domain)
	existed := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, false, fmt.Errorf("looking up %s@%s: %w", handle, domain, err)
	}

	user, err := st.UpsertUser(ctx, store.UpsertUserParams{
		LinkkeysDomain: domain,
		LinkkeysUserID: handle + "@" + domain,
		Handle:         handle,
		DisplayName:    handle,
		Kind:           store.UserKindHuman,
	})
	if err != nil {
		return nil, false, fmt.Errorf("creating %s@%s: %w", handle, domain, err)
	}
	return user, existed, nil
}
