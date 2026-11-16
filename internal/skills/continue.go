package skills

import (
	"context"
	"errors"
)

// ErrStopped is returned by a multi-request read that its caller's continue
// check stopped. It is not a failure of the upstream.
var ErrStopped = errors.New("skills: the read was stopped by its caller")

// ErrNotServed is returned by a listing that was not sent: the server does not
// announce skills, or it is excluded from them. It is deliberately not an
// empty listing. An empty listing says "this server has no skills now" and
// unlists what was known; a server that was not asked says nothing.
var ErrNotServed = errors.New("skills: the server is not asked for skills")

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
