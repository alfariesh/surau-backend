// Command reset-user-mfa is the A-3 emergency escape hatch: it removes a
// user's MFA enrollment and recovery codes directly in the database, for the
// case where the last admin lost both the authenticator and the recovery
// codes (the API reset flow requires a recovery code). Mirrors
// cmd/set-user-role: direct SQL, loud audit row with actor=cli.
//
//	go run ./cmd/reset-user-mfa --email admin@example.com
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	var (
		email          = flag.String("email", "", "user email")
		revokeSessions = flag.Bool("revoke-sessions", true, "revoke all sessions (bump token_version)")
		pgURL          = flag.String("pg-url", os.Getenv("PG_URL"), "PostgreSQL connection URL")
	)
	flag.Parse()

	normalizedEmail := strings.ToLower(strings.TrimSpace(*email))
	if normalizedEmail == "" {
		fatalf("--email is required")
	}

	if *pgURL == "" {
		fatalf("--pg-url is required or PG_URL must be set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, *pgURL)
	if err != nil {
		fatalf("connect postgres: %v", err)
	}
	defer pool.Close()

	var (
		userID   string
		username string
	)

	err = pool.QueryRow(
		ctx,
		"SELECT id, username FROM users WHERE email = $1 AND deleted_at IS NULL",
		normalizedEmail,
	).Scan(&userID, &username)
	if err != nil {
		fatalf("find user: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, "DELETE FROM user_mfa WHERE user_id = $1", userID)
	if err != nil {
		fatalf("delete user_mfa: %v", err)
	}

	if tag.RowsAffected() == 0 {
		fatalf("user %s has no MFA enrollment", normalizedEmail)
	}

	if _, err = tx.Exec(ctx, "DELETE FROM user_mfa_recovery_codes WHERE user_id = $1", userID); err != nil {
		fatalf("delete recovery codes: %v", err)
	}

	if _, err = tx.Exec(ctx, "DELETE FROM mfa_challenges WHERE user_id = $1", userID); err != nil {
		fatalf("delete challenges: %v", err)
	}

	if *revokeSessions {
		// Same lever the API uses: bumping token_version kills every live
		// access token; deleting sessions kills the refresh chains.
		if _, err = tx.Exec(ctx, "UPDATE users SET token_version = token_version + 1, updated_at = now() WHERE id = $1", userID); err != nil {
			fatalf("bump token_version: %v", err)
		}

		if _, err = tx.Exec(ctx, "DELETE FROM auth_sessions WHERE user_id = $1", userID); err != nil {
			fatalf("delete sessions: %v", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		fatalf("commit: %v", err)
	}

	metadata, err := json.Marshal(map[string]string{
		"actor":            "cli",
		"transport":        "cli",
		"revoked_sessions": fmt.Sprintf("%t", *revokeSessions),
	})
	if err != nil {
		fatalf("marshal audit metadata: %v", err)
	}

	_, err = pool.Exec(
		ctx, `
INSERT INTO auth_audit_logs (id, event, status, user_id, email, error_code, metadata, created_at)
VALUES ($1, 'mfa_reset_confirm', 'success', $2, $3, '', $4, now())`,
		uuid.NewString(),
		userID,
		normalizedEmail,
		metadata,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: MFA removed but audit log insert failed: %v\n", err)
	}

	fmt.Printf("removed MFA from %s <%s> (sessions revoked: %t) — user logs in with password only and should re-enroll\n",
		username, normalizedEmail, *revokeSessions)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
