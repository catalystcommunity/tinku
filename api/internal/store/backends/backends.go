// Package backends links every store implementation into the binary. Import
// it for its side effects wherever a Store is opened from a URI:
//
//	import _ "github.com/catalystcommunity/tinku/api/internal/store/backends"
//
// It exists so store.Open can stay free of imports of the packages that
// implement store.Store, and so a test that wants only one backend can
// import that one package directly instead.
package backends

import (
	_ "github.com/catalystcommunity/tinku/api/internal/store/postgres"
	_ "github.com/catalystcommunity/tinku/api/internal/store/sqlite"
)
