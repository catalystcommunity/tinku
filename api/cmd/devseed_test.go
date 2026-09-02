package cmd

import (
	"path/filepath"
	"testing"

	"github.com/catalystcommunity/tinku/api/internal/store"
	"github.com/catalystcommunity/tinku/coredb"
)

// seedDB makes an empty database and returns its URI. SQLite needs nothing
// installed, which is what lets this run in an ordinary `go test`.
func seedDB(t *testing.T) string {
	t.Helper()
	uri := "sqlite:" + filepath.Join(t.TempDir(), "seed.db")
	if err := coredb.Up(uri); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	return uri
}

// TestDevSeedRefusesOutsideDev is the whole point of the guard: the command
// makes accounts that anybody can then sign in as. Env defaults to prod, so
// a missing --env must refuse rather than seed.
func TestDevSeedRefusesOutsideDev(t *testing.T) {
	uri := seedDB(t)

	for _, env := range []string{"", "prod", "production"} {
		flags := map[string]string{"db-uri": uri}
		if env != "" {
			flags["env"] = env
		}
		if err := DevSeed(nil, flags); err == nil {
			t.Fatalf("dev-seed in env %q returned no error", env)
		}
	}

	st, err := store.Open(uri)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	defer st.Close() //nolint:errcheck // test cleanup

	if _, err := st.UserByHandle(t.Context(), seedAdminHandle, seedDomain); err == nil {
		t.Fatal("a refused dev-seed still made devadmin")
	}
}

// TestDevSeedIsIdempotent covers the shape a person actually meets: the
// command runs again on every `./tools.sh dev`, so a second run must make no
// second account and must not disturb the role.
func TestDevSeedIsIdempotent(t *testing.T) {
	uri := seedDB(t)
	flags := map[string]string{"db-uri": uri, "env": "dev"}

	for i := range 2 {
		if err := DevSeed(nil, flags); err != nil {
			t.Fatalf("dev-seed run %d: %v", i+1, err)
		}
	}

	st, err := store.Open(uri)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	defer st.Close() //nolint:errcheck // test cleanup
	ctx := t.Context()

	admin, err := st.UserByHandle(ctx, seedAdminHandle, seedDomain)
	if err != nil {
		t.Fatalf("devadmin is missing: %v", err)
	}
	if !admin.IsAdmin {
		t.Error("devadmin does not hold the administrator role")
	}

	user, err := st.UserByHandle(ctx, seedUserHandle, seedDomain)
	if err != nil {
		t.Fatalf("devuser is missing: %v", err)
	}
	if user.IsAdmin {
		t.Error("devuser holds the administrator role; it is the account that shows what everybody else sees")
	}

	// Two runs, one account each: the second upsert must find the first row
	// rather than make a second with the same address.
	admins, err := st.ListAdmins(ctx)
	if err != nil {
		t.Fatalf("listing administrators: %v", err)
	}
	if len(admins) != 1 {
		t.Errorf("administrators after two runs = %d, want 1", len(admins))
	}
}

// TestDevSeedKeepsAnAdminGrantedElsewhere: a person can grant the role to
// their own account, and the next `./tools.sh dev` must not take it away.
func TestDevSeedKeepsAnAdminGrantedElsewhere(t *testing.T) {
	uri := seedDB(t)
	st, err := store.Open(uri)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	defer st.Close() //nolint:errcheck // test cleanup
	ctx := t.Context()

	ada, err := st.UpsertUser(ctx, store.UpsertUserParams{
		LinkkeysDomain: seedDomain,
		LinkkeysUserID: "ada@" + seedDomain,
		Handle:         "ada",
		DisplayName:    "ada",
		Kind:           store.UserKindHuman,
	})
	if err != nil {
		t.Fatalf("making ada: %v", err)
	}
	if err := st.SetAdmin(ctx, ada.ID, true); err != nil {
		t.Fatalf("granting to ada: %v", err)
	}

	if err := DevSeed(nil, map[string]string{"db-uri": uri, "env": "dev"}); err != nil {
		t.Fatalf("dev-seed: %v", err)
	}

	after, err := st.UserByHandle(ctx, "ada", seedDomain)
	if err != nil {
		t.Fatalf("ada is missing: %v", err)
	}
	if !after.IsAdmin {
		t.Error("dev-seed took the administrator role away from ada")
	}
}
