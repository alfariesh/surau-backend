package persistent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const licensePublishConstraint = "book_license_publish_permitted_check"

// getBookLicenseStatusForPublish reads the Edition-level status. A publishing
// transaction requests FOR SHARE so a concurrent curator decision cannot race
// the final visibility transition; the database triggers remain the backstop
// for every other writer.
func getBookLicenseStatusForPublish(
	ctx context.Context,
	q productionQuerier,
	bookID int,
	lock bool,
) (string, error) {
	query := `SELECT license_status FROM books WHERE id = $1`
	if lock {
		query += ` FOR SHARE`
	}

	var status string
	if err := q.QueryRow(ctx, query, bookID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", entity.ErrBookNotFound
		}

		return "", fmt.Errorf("license publish gate: %w", err)
	}

	return status, nil
}

// mapLicensePublishError translates the stable PostgreSQL trigger contract to
// the public domain error used by every editorial publish endpoint.
func mapLicensePublishError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}

	if pgErr.ConstraintName == licensePublishConstraint ||
		strings.Contains(pgErr.Message, "license_not_permitted") {
		return entity.ErrLicenseNotPermitted
	}

	return err
}
