//go:build !linux

package core

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/sandbox"

// defaultSandboxRlimits is empty off Linux: the native sandbox only runs on
// Linux (Landlock), and the rlimit resource constants differ across platforms.
func defaultSandboxRlimits() []sandbox.Rlimit { return nil }
