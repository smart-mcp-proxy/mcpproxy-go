// Package shutdown holds the time limits of a graceful shutdown. They nest, from one container up
// to the tray, so they live together and a test pins the order.
package shutdown

import "time"

const (
	// CallGrace is how long requests in progress, tool calls included, may take to finish.
	CallGrace = 15 * time.Second
	// HTTPServer bounds http.Server.Shutdown: CallGrace, plus time for a cancelled handler to return.
	HTTPServer = CallGrace + 5*time.Second

	// ActionDrain is how long reconcile actions, whose context is already cancelled, may take to
	// return. Only a wedged one needs it.
	ActionDrain = 10 * time.Second

	// StdinClose is how long a stdio server may take to exit after its stdin was closed, before it
	// is signalled. mcp-go uses the same 2s, as do the official TypeScript and Python MCP SDKs.
	StdinClose = 2 * time.Second
	// ContainerStop is the --stop-timeout of isolated containers: how long a server may take to exit
	// after SIGTERM. mcp-go gives a native server the same 3s.
	ContainerStop = 3 * time.Second
	// Disconnect is how long all upstream servers together, which stop in parallel, may take.
	Disconnect = 10 * time.Second
	// Sweep and SweepKill bound stopping, then killing, the containers Disconnect left behind.
	Sweep     = 5 * time.Second
	SweepKill = 5 * time.Second
	// Upstreams bounds stopping the upstream servers as a whole.
	Upstreams = 25 * time.Second

	// Core is when the core gives up and exits by itself.
	Core = 60 * time.Second
	// Tray is when the tray force kills the core. It only has to catch a wedged one.
	Tray = 65 * time.Second
)
