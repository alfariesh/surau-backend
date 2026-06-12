package postgres

import "time"

// Option -.
type Option func(*Postgres)

// MaxPoolSize -.
func MaxPoolSize(size int) Option {
	return func(c *Postgres) {
		c.maxPoolSize = size
	}
}

// ConnAttempts -.
func ConnAttempts(attempts int) Option {
	return func(c *Postgres) {
		c.connAttempts = attempts
	}
}

// ConnTimeout -.
func ConnTimeout(timeout time.Duration) Option {
	return func(c *Postgres) {
		c.connTimeout = timeout
	}
}

// MaxConnLifetime bounds how long one pooled connection may live.
func MaxConnLifetime(lifetime time.Duration) Option {
	return func(c *Postgres) {
		c.maxConnLifetime = lifetime
	}
}

// MaxConnIdleTime bounds how long an idle connection stays in the pool.
func MaxConnIdleTime(idle time.Duration) Option {
	return func(c *Postgres) {
		c.maxConnIdleTime = idle
	}
}
