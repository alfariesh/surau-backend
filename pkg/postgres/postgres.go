// Package postgres implements postgres connection.
package postgres

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	_defaultMaxPoolSize  = 1
	_defaultConnAttempts = 10
	_defaultConnTimeout  = time.Second
)

// Postgres -.
type Postgres struct {
	maxPoolSize     int
	connAttempts    int
	connTimeout     time.Duration
	maxConnLifetime time.Duration
	maxConnIdleTime time.Duration

	Builder squirrel.StatementBuilderType
	Pool    *pgxpool.Pool
}

// New -.
func New(url string, opts ...Option) (*Postgres, error) {
	pg := &Postgres{
		maxPoolSize:  _defaultMaxPoolSize,
		connAttempts: _defaultConnAttempts,
		connTimeout:  _defaultConnTimeout,
	}

	// Custom options
	for _, opt := range opts {
		opt(pg)
	}
	if pg.connAttempts <= 0 {
		return nil, fmt.Errorf("postgres - NewPostgres - connAttempts must be positive")
	}
	if pg.connTimeout <= 0 {
		return nil, fmt.Errorf("postgres - NewPostgres - connTimeout must be positive")
	}

	pg.Builder = squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)

	poolConfig, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("postgres - NewPostgres - pgxpool.ParseConfig: %w", err)
	}

	poolConfig.MaxConns = safeIntToInt32(pg.maxPoolSize)
	// Zero values keep the pgxpool defaults (1h lifetime, 30m idle).
	if pg.maxConnLifetime > 0 {
		poolConfig.MaxConnLifetime = pg.maxConnLifetime
	}

	if pg.maxConnIdleTime > 0 {
		poolConfig.MaxConnIdleTime = pg.maxConnIdleTime
	}

	var lastErr error
	for pg.connAttempts > 0 {
		pg.Pool, err = pgxpool.NewWithConfig(context.Background(), poolConfig)
		if err == nil {
			pingCtx, cancel := context.WithTimeout(context.Background(), pg.connTimeout)
			err = pg.Pool.Ping(pingCtx)
			cancel()
		}
		if err == nil {
			break
		}

		lastErr = err
		if pg.Pool != nil {
			pg.Pool.Close()
			pg.Pool = nil
		}
		log.Printf("Postgres is trying to connect, attempts left: %d", pg.connAttempts)

		time.Sleep(pg.connTimeout)

		pg.connAttempts--
	}

	if err != nil {
		return nil, fmt.Errorf("postgres - NewPostgres - connAttempts == 0: %w", lastErr)
	}

	return pg, nil
}

// Ping verifies that the configured pool can reach PostgreSQL.
func (p *Postgres) Ping(ctx context.Context) error {
	if p.Pool == nil {
		return fmt.Errorf("postgres - Ping - pool is not initialized")
	}

	return p.Pool.Ping(ctx)
}

// Close -.
func (p *Postgres) Close() {
	if p.Pool != nil {
		p.Pool.Close()
	}
}

func safeIntToInt32(v int) int32 {
	if v <= 0 {
		return _defaultMaxPoolSize
	}

	const maxInt32 = int(^uint32(0) >> 1)
	if v > maxInt32 {
		return int32(maxInt32)
	}

	return int32(v)
}
