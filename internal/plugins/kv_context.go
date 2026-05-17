package plugins

import (
	"context"

	"github.com/shareed2k/honey/internal/stepkv"
)

type kvSessionKey struct{}

// WithKVSession attaches the recipe stepkv session for plugin host functions.
func WithKVSession(ctx context.Context, sess *stepkv.Session) context.Context {
	if sess == nil {
		return ctx
	}
	return context.WithValue(ctx, kvSessionKey{}, sess)
}

// KVSessionFromContext returns the session bound for this plugin call, if any.
func KVSessionFromContext(ctx context.Context) (*stepkv.Session, bool) {
	if ctx == nil {
		return nil, false
	}
	s, ok := ctx.Value(kvSessionKey{}).(*stepkv.Session)
	return s, ok && s != nil
}
