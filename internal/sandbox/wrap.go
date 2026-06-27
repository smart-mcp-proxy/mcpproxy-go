package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
)

// Re-exec wrapper protocol (MCP-34.3).
//
// Landlock confines the *current* process and every process it then execs, and
// the confinement is irreversible — so it cannot be applied in-process before
// mcp-go spawns an upstream stdio server. The integration is therefore a tiny
// re-exec wrapper: mcpproxy launches itself as
//
//	mcpproxy __sandbox_exec -- <command> [args...]
//
// with the desired confinement encoded in the environment. The child decodes the
// Spec, calls Apply, then execs <command>, replacing its own image so the
// untrusted server inherits the locked-down Landlock domain and the server's
// stdin/stdout/stderr pass straight through with no intervening mux.
const (
	// Subcommand is the hidden mcpproxy subcommand that runs the re-exec child.
	Subcommand = "__sandbox_exec"

	// EnvSpec carries the JSON-encoded Spec from the parent to the re-exec child.
	EnvSpec = "MCPPROXY_SANDBOX_SPEC"
)

// WrapCommand builds the argv and extra environment needed to launch
// command/args confined by spec, by re-executing self (the absolute path to the
// running mcpproxy binary) as the sandbox child. The returned extraEnv entries
// must be appended to the child process's environment so SpecFromEnv can decode
// the Spec on the other side.
//
// It performs no syscalls and is safe to call on every platform; whether the
// confinement actually takes effect is decided later by Apply inside the child
// (a no-op on non-Linux / Landlock-less kernels).
func WrapCommand(self string, spec Spec, command string, args []string) (wrappedCommand string, wrappedArgs []string, extraEnv []string, err error) {
	enc, err := json.Marshal(spec)
	if err != nil {
		return "", nil, nil, fmt.Errorf("sandbox: encode spec: %w", err)
	}
	wrappedArgs = make([]string, 0, len(args)+3)
	wrappedArgs = append(wrappedArgs, Subcommand, "--", command)
	wrappedArgs = append(wrappedArgs, args...)
	extraEnv = []string{EnvSpec + "=" + string(enc)}
	return self, wrappedArgs, extraEnv, nil
}

// SpecFromEnv decodes the Spec the parent encoded into EnvSpec. ok is false when
// the variable is absent — i.e. the process was not launched as a sandbox child.
func SpecFromEnv() (spec Spec, ok bool, err error) {
	raw, present := os.LookupEnv(EnvSpec)
	if !present {
		return Spec{}, false, nil
	}
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return Spec{}, true, fmt.Errorf("sandbox: decode %s: %w", EnvSpec, err)
	}
	return spec, true, nil
}
