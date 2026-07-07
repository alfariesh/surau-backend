package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	var (
		email = flag.String("email", "", "user email")
		role  = flag.String("role", entity.UserRoleAdmin, "user role: user, editor, or admin")
		pgURL = flag.String("pg-url", os.Getenv("PG_URL"), "PostgreSQL connection URL")
	)
	flag.Parse()

	normalizedEmail := strings.ToLower(strings.TrimSpace(*email))
	if normalizedEmail == "" {
		fatalf("--email is required")
	}

	if *pgURL == "" {
		fatalf("--pg-url is required or PG_URL must be set")
	}

	normalizedRole, err := entity.NormalizeUserRole(*role)
	if err != nil {
		fatalf("invalid --role %q, use user, editor, or admin", *role)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, *pgURL)
	if err != nil {
		fatalf("connect postgres: %v", err)
	}
	defer pool.Close()

	// Unlike the admin API, this CLI deliberately skips the last-admin guard:
	// it is the recovery escape hatch when no working admin account remains.
	var (
		user         entity.User
		previousRole string
	)

	err = pool.QueryRow(
		ctx, `
WITH existing AS (
    SELECT id, role AS previous_role
    FROM users
    WHERE email = $2 AND deleted_at IS NULL
    FOR UPDATE
)
UPDATE users u
SET role = $1, updated_at = now()
FROM existing e
WHERE u.id = e.id
RETURNING u.id, u.username, u.email, u.role, e.previous_role, u.created_at, u.updated_at`,
		normalizedRole,
		normalizedEmail,
	).Scan(&user.ID, &user.Username, &user.Email, &user.Role, &previousRole, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		fatalf("set user role: %v", err)
	}

	if previousRole == entity.UserRoleAdmin && normalizedRole != entity.UserRoleAdmin {
		var adminCount int
		if err = pool.QueryRow(
			ctx,
			"SELECT count(*) FROM users WHERE role = $1 AND deleted_at IS NULL",
			entity.UserRoleAdmin,
		).Scan(&adminCount); err == nil && adminCount == 0 {
			fmt.Fprintln(os.Stderr, "warning: no admin accounts remain after this change")
		}
	}

	metadata, err := json.Marshal(map[string]string{
		"actor":     "cli",
		"transport": "cli",
		"old_role":  previousRole,
		"new_role":  normalizedRole,
		"role":      normalizedRole,
	})
	if err != nil {
		fatalf("marshal audit metadata: %v", err)
	}

	_, err = pool.Exec(
		ctx, `
INSERT INTO auth_audit_logs (id, event, status, user_id, email, error_code, metadata, created_at)
VALUES ($1, 'role_change', 'success', $2, $3, '', $4, now())`,
		uuid.NewString(),
		user.ID,
		user.Email,
		metadata,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: role updated but audit log insert failed: %v\n", err)
	}

	fmt.Printf("updated %s <%s> role=%s (was %s)\n", user.Username, user.Email, user.Role, previousRole)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
