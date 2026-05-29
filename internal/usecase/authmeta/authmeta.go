package authmeta

import (
	"context"
	"strings"
)

type contextKey struct{}

// Meta contains sanitized request metadata used for auth rate limits and audit logs.
type Meta struct {
	ClientIP  string
	UserAgent string
	Transport string
}

// With stores auth request metadata in the context.
func With(ctx context.Context, meta Meta) context.Context {
	meta.ClientIP = strings.TrimSpace(meta.ClientIP)
	meta.UserAgent = strings.TrimSpace(meta.UserAgent)
	meta.Transport = strings.TrimSpace(meta.Transport)

	return context.WithValue(ctx, contextKey{}, meta)
}

// From extracts auth request metadata from the context.
func From(ctx context.Context) Meta {
	meta, _ := ctx.Value(contextKey{}).(Meta)
	if meta.Transport == "" {
		meta.Transport = "unknown"
	}
	if meta.ClientIP == "" {
		meta.ClientIP = meta.Transport
	}

	return meta
}
