//go:build linux

package tray

import "context"

type instrumentation interface {
	Start(ctx context.Context)
	NotifyConnectionState(state ConnectionState)
	NotifyStatus()
	NotifyMenus()
	Shutdown()
}

type noopInstrumentation struct{}

func newInstrumentation(*App) instrumentation { return noopInstrumentation{} }

func (noopInstrumentation) Start(context.Context)                 {}
func (noopInstrumentation) NotifyConnectionState(ConnectionState) {}
func (noopInstrumentation) NotifyStatus()                         {}
func (noopInstrumentation) NotifyMenus()                          {}
func (noopInstrumentation) Shutdown()                             {}
