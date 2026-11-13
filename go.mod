module github.com/bengisupinar/campaign

go 1.27.0

toolchain go1.27.4

// The Vue app's node_modules contains Go files (e.g. the npm package "flatted"),
// which `./...` would otherwise build, vet and lint.
ignore ./frontend

require (
	github.com/caarlos0/env/v11 v11.4.2
	github.com/go-chi/chi/v5 v5.5.0
	github.com/jackc/pgx/v5 v5.12.0
	github.com/pressly/goose/v3 v3.30.1
	gorm.io/driver/postgres v1.6.3
	gorm.io/gorm v1.31.2
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.3 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/mfridman/interpolate v0.2.0 // indirect
	github.com/sethvargo/go-retry v0.5.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/text v0.44.0 // indirect
)
