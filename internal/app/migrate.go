//go:build migrate

package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/golang-migrate/migrate/v4"
	// migrate tools
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5"
)

const (
	_defaultAttempts        = 20
	_defaultTimeout         = time.Second
	a2MigrationVersion uint = 20260715000003
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
	var currentVersion uint
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
	} else {
		currentVersion = version
	}

	// A-2 is the first migration that creates cluster roles and grants existing
	// objects. Prove CREATEROLE and ownership before migrate marks the schema
	// dirty; otherwise a privilege mismatch would turn into a boot loop.
	if currentVersion < a2MigrationVersion {
		if err = preflightA2RoleMigration(databaseURL); err != nil {
			log.Fatalf("Migrate: A-2 role preflight failed before schema mutation: %s", err)
		}
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

func preflightA2RoleMigration(databaseURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	var canCreateRoles, ownsPublicObjects bool
	err = conn.QueryRow(ctx, `
SELECT role.rolsuper OR role.rolcreaterole,
       COALESCE(bool_and(objects.relowner = role.oid), TRUE)
FROM pg_roles role
LEFT JOIN (
    SELECT class.relowner
    FROM pg_class class
    JOIN pg_namespace namespace ON namespace.oid = class.relnamespace
    WHERE namespace.nspname = 'public'
      AND class.relkind IN ('r', 'p', 'v', 'm')
      AND class.relname <> 'schema_migrations'
) objects ON TRUE
WHERE role.rolname = current_user
GROUP BY role.oid, role.rolsuper, role.rolcreaterole`).Scan(&canCreateRoles, &ownsPublicObjects)
	if err != nil {
		return fmt.Errorf("inspect role: %w", err)
	}
	if !canCreateRoles {
		return errors.New("migration login needs CREATEROLE (or superuser) for A-2 NOLOGIN groups")
	}
	if !ownsPublicObjects {
		return errors.New("migration login must own existing public tables/views before A-2 grants and triggers")
	}

	return nil
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
