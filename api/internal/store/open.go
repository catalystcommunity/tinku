package store

import (
	"fmt"

	"github.com/catalystcommunity/tinku/coredb"
)

// Backend is a constructor for one store implementation. store/postgres and
// store/sqlite register themselves through Register in their own package's
// init, which is what keeps this package free of imports of either — the
// interface does not depend on its implementations.
type Backend func(dbURI string) (Store, error)

var backends = map[coredb.Dialect]Backend{}

// Register wires a dialect to its implementation. Called from the
// implementation packages' init functions; registering the same dialect
// twice panics, because it can only mean two packages disagree about who
// owns a backend.
func Register(dialect coredb.Dialect, backend Backend) {
	if _, exists := backends[dialect]; exists {
		panic("store: duplicate backend registration for dialect " + dialect)
	}
	backends[dialect] = backend
}

// Open picks the backend the URI names and opens it. Importing
// api/internal/store/backends (blank import) is what makes both
// implementations available; a URI naming an unregistered dialect is an
// error here rather than a nil Store discovered later.
func Open(dbURI string) (Store, error) {
	target, err := coredb.ParseTarget(dbURI)
	if err != nil {
		return nil, err
	}
	backend, ok := backends[target.Dialect]
	if !ok {
		return nil, fmt.Errorf("store: no backend registered for dialect %q (is api/internal/store/backends imported?)", target.Dialect)
	}
	return backend(dbURI)
}
