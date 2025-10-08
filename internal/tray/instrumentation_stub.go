//go:build linux

package tray

import "context"

//nolint:unused // Used by other build tags (darwin)
type instrumentation interface {
	Start(ctx context.Context)
	NotifyConnectionState(state ConnectionState)
	NotifyStatus()
	NotifyMenus()
	Shutdown()
}

//nolint:unused // Used by other build tags (darwin)
type noopInstrumentation struct{}

//nolint:unused // Used by other build tags (darwin)
func newInstrumentation(*App) instrumentation { return noopInstrumentation{} }

//nolint:unused // Used by other build tags (darwin)
func (noopInstrumentation) Start(context.Context) {}

//nolint:unused // Used by other build tags (darwin)
func (noopInstrumentation) NotifyConnectionState(ConnectionState) {}

//nolint:unused // Used by other build tags (darwin)
func (noopInstrumentation) NotifyStatus() {}

//nolint:unused // Used by other build tags (darwin)
func (noopInstrumentation) NotifyMenus() {}

//nolint:unused // Used by other build tags (darwin)
func (noopInstrumentation) Shutdown() {}
