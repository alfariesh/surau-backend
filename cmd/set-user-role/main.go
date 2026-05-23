package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	var (
		email = flag.String("email", "", "user email")
		role  = flag.String("role", entity.UserRoleAdmin, "user role: user or admin")
		pgURL = flag.String("pg-url", os.Getenv("PG_URL"), "PostgreSQL connection URL")
	)
	flag.Parse()

	if *email == "" {
		fatalf("--email is required")
	}

	if *pgURL == "" {
		fatalf("--pg-url is required or PG_URL must be set")
	}

	switch *role {
	case entity.UserRoleUser, entity.UserRoleAdmin:
	default:
		fatalf("invalid --role %q, use user or admin", *role)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, *pgURL)
	if err != nil {
		fatalf("connect postgres: %v", err)
	}
	defer pool.Close()

	var user entity.User
	err = pool.QueryRow(ctx, `
UPDATE users
SET role = $1, updated_at = now()
WHERE email = $2
RETURNING id, username, email, role, password_hash, created_at, updated_at`,
		*role,
		*email,
	).Scan(&user.ID, &user.Username, &user.Email, &user.Role, &user.PasswordHash, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		fatalf("set user role: %v", err)
	}

	fmt.Printf("updated %s <%s> role=%s\n", user.Username, user.Email, user.Role)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
