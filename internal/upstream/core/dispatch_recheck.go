package core

import (
	"context"
	"errors"
	"fmt"
)

// ErrDispatchRecheckRefused marks a call that a WithDispatchRecheck check
// refused immediately before the transport. Nothing was sent upstream. The
// check's own error is wrapped alongside it, so callers can errors.Is on
// either. managed.ErrDispatchRecheckRefused is this same value.
var ErrDispatchRecheckRefused = errors.New("dispatch refused by the pre-transport re-check")

type dispatchRecheckKey struct{}

// WithDispatchRecheck attaches check to ctx. The attached checks run at every
// point a call may have waited after its gate read a policy verdict — the
// managed layer's admission-control queue, and Client.CallTool once it holds
// the SSE request lock, immediately before the request is handed to the
// transport — so a verdict such as Spec 117 FR-018's review-dirty mark is
// re-read for a call that was parked while it changed. A non-nil error refuses
// the call with ErrDispatchRecheckRefused and zero transport calls. Checks
// attached by nested contexts all run, innermost first.
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

// RunDispatchRecheck runs ctx's attached checks; nil when there are none or
// all pass.
func RunDispatchRecheck(ctx context.Context) error {
	check, ok := ctx.Value(dispatchRecheckKey{}).(func() error)
	if !ok || check == nil {
		return nil
	}
	if err := check(); err != nil {
		return fmt.Errorf("%w: %w", ErrDispatchRecheckRefused, err)
	}
	return nil
}
