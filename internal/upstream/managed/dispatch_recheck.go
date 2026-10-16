package managed

import (
	"context"
	"errors"
	"fmt"
)

// ErrDispatchRecheckRefused marks a call that a WithDispatchRecheck check
// refused immediately before the transport. Nothing was sent upstream. The
// check's own error is wrapped alongside it, so callers can errors.Is on
// either.
var ErrDispatchRecheckRefused = errors.New("dispatch refused by the pre-transport re-check")

type dispatchRecheckKey struct{}

// WithDispatchRecheck attaches check to ctx. callTool runs every attached
// check AFTER the admission-control queue wait and immediately before the
// transport (and again before a session re-init's retry), so a policy verdict
// a gate read before the wait — for example Spec 117 FR-018's review-dirty
// mark — is re-read for a call that sat in the queue while it changed. A
// non-nil error refuses the call with ErrDispatchRecheckRefused and zero
// transport calls. Checks attached by nested contexts all run, innermost
// first.
func WithDispatchRecheck(ctx context.Context, check func() error) context.Context {
	if check == nil {
		return ctx
	}
	if prev, ok := ctx.Value(dispatchRecheckKey{}).(func() error); ok && prev != nil {
		inner := check
		check = func() error {
			if err := inner(); err != nil {
				return err
			}
			return prev()
		}
	}
	return context.WithValue(ctx, dispatchRecheckKey{}, check)
}

// runDispatchRecheck runs ctx's attached checks; nil when there are none or
// all pass.
func runDispatchRecheck(ctx context.Context) error {
	check, ok := ctx.Value(dispatchRecheckKey{}).(func() error)
	if !ok || check == nil {
		return nil
	}
	if err := check(); err != nil {
		return fmt.Errorf("%w: %w", ErrDispatchRecheckRefused, err)
	}
	return nil
}
