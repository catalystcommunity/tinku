module github.com/catalystcommunity/tinku/api

go 1.26.4

replace github.com/catalystcommunity/tinku/coredb => ../coredb

require (
	github.com/catalystcommunity/linkkeys/sdks/regular-rp/go v0.0.0-20260831021739-02c163cb5e77
	github.com/catalystcommunity/tinku/coredb v0.0.0-00010101000000-000000000000
	github.com/oklog/ulid/v2 v2.1.2
	github.com/prometheus/client_golang v1.24.1
	github.com/rs/cors v1.11.1
	github.com/sirupsen/logrus v1.10.2
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/catalystcommunity/csilgen/transports/go v0.0.0-20260713013116-a661c8727022 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.10.0 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pressly/goose/v3 v3.27.3 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.70.1 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/sethvargo/go-retry v0.4.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.57.0 // indirect
)
