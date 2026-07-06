//go:build migrate

package app

import (
	"errors"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/golang-migrate/migrate/v4"
	// migrate tools
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

const (
	_defaultAttempts = 20
	_defaultTimeout  = time.Second
)

func init() {
	databaseURL, ok := os.LookupEnv("PG_URL")
	if !ok || len(databaseURL) == 0 {
		log.Fatalf("migrate: environment variable not declared: PG_URL")
	}

	databaseURL = withDefaultSSLMode(databaseURL, "disable")

	var (
		attempts = _defaultAttempts
		err      error
		m        *migrate.Migrate
	)

	for attempts > 0 {
		m, err = migrate.New("file://migrations", databaseURL)
		if err == nil {
			break
		}

		log.Printf("Migrate: postgres is trying to connect, attempts left: %d", attempts)
		time.Sleep(_defaultTimeout)
		attempts--
	}

	if err != nil {
		log.Fatalf("Migrate: postgres connect error: %s", err)
	}

	defer m.Close()

	// Refuse to auto-migrate over a DIRTY schema (a previous migration aborted
	// mid-way). Auto-forcing would silently skip a real migration, so instead fail
	// with the exact recovery steps — otherwise the container just crash-loops on a
	// cryptic "Dirty database" error with no guidance.
	if version, dirty, verr := m.Version(); verr != nil {
		if !errors.Is(verr, migrate.ErrNilVersion) {
			log.Fatalf("Migrate: cannot read schema version: %s", verr)
		}
		// ErrNilVersion: no migration has ever run on this database — nothing to check.
	} else if dirty {
		log.Fatalf("Migrate: schema is DIRTY at version %d — a previous migration aborted "+
			"mid-way. Do NOT redeploy blindly. Inspect that migration, fix the data/schema, "+
			"then run `migrate -path migrations -database $PG_URL force <last-good-version>` "+
			"and redeploy. Refusing to auto-migrate.", version)
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		log.Fatalf("Migrate: up error: %s (if this left the schema dirty, see the DIRTY "+
			"recovery steps above before redeploying)", err)
	}

	if errors.Is(err, migrate.ErrNoChange) {
		log.Printf("Migrate: no change")
		return
	}

	log.Printf("Migrate: up success")
}

func withDefaultSSLMode(databaseURL, sslMode string) string {
	parsedURL, err := url.Parse(databaseURL)
	if err != nil {
		return databaseURL
	}

	query := parsedURL.Query()
	if query.Has("sslmode") {
		return databaseURL
	}

	query.Set("sslmode", sslMode)
	parsedURL.RawQuery = query.Encode()

	return parsedURL.String()
}
