package skills

import (
	"context"
	"errors"
)

// ErrStopped is returned by a multi-request read that its caller's continue
// check stopped. It is not a failure of the upstream.
var ErrStopped = errors.New("skills: the read was stopped by its caller")

type continueCheckKey struct{}

// WithContinueCheck attaches a check that a multi-request read asks before
// every request it sends. It is how the catalog stops a listing that is still
// paging when skills are switched off: a context alone would need someone to
// watch the configuration and cancel it.
func WithContinueCheck(ctx context.Context, check func() bool) context.Context {
	return context.WithValue(ctx, continueCheckKey{}, check)
}

// ShouldContinue reports whether the read may send its next request. A context
// without a check always may.
func ShouldContinue(ctx context.Context) bool {
	check, ok := ctx.Value(continueCheckKey{}).(func() bool)
	return !ok || check == nil || check()
}
