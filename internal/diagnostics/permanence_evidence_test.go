package diagnostics

import (
	"fmt"
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stdioPrematureExit reproduces the exact error shape the production stdio path
// hands the classifier: core.enrichTransportClosedError folds the CHILD
// PROCESS'S captured stderr into mcpproxy's own wrapper text before the error
// ever reaches Classify.
func stdioPrematureExit(childStderr string) error {
	return fmt.Errorf("failed to connect: server process exited before completing the MCP initialize handshake; recent stderr:\n%s: transport closed", childStderr)
}

// TestParkableCode_ChildStderrNeverProvesPermanence is the regression for the
// review of GH #1145. Classify's stdio/docker string fallbacks are deliberately
// broad AND they run against text the child process wrote. Before this fix a
// transient crash whose stderr merely contained "no such file or directory",
// "permission denied" or "command not found" was classified with a code marked
// RetryPermanent, and the server was parked forever after two attempts.
//
// Only the authority to STOP RETRYING is withdrawn here; the classification is
// whatever Classify thinks is the most useful message.
//
// #1156 later removed most of the hazard at the SOURCE: a child printing ENOENT
// for its own data file is no longer read as a spawn ENOENT at all. Those rows
// therefore no longer reach a code declared permanent, which is a strictly
// better outcome than the gate catching them — so `permanent` records per row
// whether this input still exercises the gate, and the last two rows keep a
// stderr-derived PERMANENT code in the table so the gate is never left untested.
func TestParkableCode_ChildStderrNeverProvesPermanence(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		hints     ClassifierHints
		wantCode  Code
		permanent bool // does this input still reach a code declared permanent?
	}{
		{
			// A Node server that cannot open its own state file on this boot.
			name:     "stdio child prints ENOENT for a data file",
			err:      stdioPrematureExit("[stderr] Error: ENOENT: no such file or directory, open '/tmp/agent/state.json'"),
			hints:    ClassifierHints{Transport: "stdio"},
			wantCode: STDIOExitBeforeInitialize,
		},
		{
			// A cache directory that is not writable yet — clears on its own.
			name:     "stdio child prints EACCES for a cache directory",
			err:      stdioPrematureExit("[stderr] EACCES: permission denied, mkdir '/var/cache/foo'"),
			hints:    ClassifierHints{Transport: "stdio"},
			wantCode: STDIOExitBeforeInitialize,
		},
		{
			name:     "stdio child prints exec format error from a helper it shelled out to",
			err:      stdioPrematureExit("[stderr] ./vendor/helper: exec format error"),
			hints:    ClassifierHints{Transport: "stdio"},
			wantCode: STDIOExitBeforeInitialize,
		},
		{
			// The GH #1145 report itself: a package install inside a container.
			name:     "docker-isolated child prints command not found",
			err:      stdioPrematureExit("[stderr] sh: line 1: jq: command not found"),
			hints:    ClassifierHints{Transport: "stdio", DockerIsolated: true},
			wantCode: DockerMissingToolchain,
		},
		{
			name:     "docker-isolated child prints a bare ENOENT",
			err:      stdioPrematureExit("[stderr] npm ERR! enoent no such file or directory, open '/root/.npm/_cacache/tmp/x'"),
			hints:    ClassifierHints{Transport: "stdio", DockerIsolated: true},
			wantCode: STDIOExitBeforeInitialize,
		},
		{
			// Still reaches a PERMANENT code from stderr alone, so the gate
			// itself stays under test rather than being incidentally bypassed
			// by #1156's better classification.
			name:      "container entrypoint genuinely missing, reported only by stderr",
			err:       stdioPrematureExit(`[stderr] docker: Error response from daemon: OCI runtime create failed: exec: "uvx": executable file not found in $PATH`),
			hints:     ClassifierHints{Transport: "stdio", DockerIsolated: true},
			wantCode:  DockerExecNotFound,
			permanent: true,
		},
		{
			name:      "container exec ENOENT, reported only by stderr",
			err:       stdioPrematureExit("[stderr] exec /usr/local/bin/uvx: no such file or directory"),
			hints:     ClassifierHints{Transport: "stdio", DockerIsolated: true},
			wantCode:  DockerExecNotFound,
			permanent: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.wantCode, Classify(tt.err, tt.hints),
				"the user-facing classification is deliberately unchanged")
			if tt.permanent {
				require.True(t, IsPermanent(tt.wantCode),
					"precondition: this code is declared permanent, which is why the gate matters")
			} else {
				require.False(t, IsPermanent(tt.wantCode),
					"#1156 classifies this benign stderr to a non-permanent code, so the hazard is gone at the source")
			}

			code, parkable := ParkableCode(tt.err, tt.hints)
			assert.Equal(t, tt.wantCode, code, "ParkableCode must report the same code as Classify")
			assert.False(t, parkable,
				"a substring of the child's own stderr must never authorise parking the server")
		})
	}
}

// TestParkableCode_TypedSignalsStillPark pins the other half: the gate narrows
// permanence to structured evidence, it does not remove it. These are the two
// signals mcpproxy produces itself — a real errno from our spawn syscall, and a
// code a producer attached deliberately with WrapError.
func TestParkableCode_TypedSignalsStillPark(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		hints ClassifierHints
		want  Code
	}{
		{
			"exec.Error ENOENT from our own spawn",
			fmt.Errorf("failed to connect: %w", &exec.Error{Name: "some-mcp-server", Err: syscall.ENOENT}),
			ClassifierHints{Transport: "stdio"},
			STDIOSpawnENOENT,
		},
		{
			"exec.Error EACCES from our own spawn",
			fmt.Errorf("failed to connect: %w", &exec.Error{Name: "some-mcp-server", Err: syscall.EACCES}),
			ClassifierHints{Transport: "stdio"},
			STDIOSpawnEACCES,
		},
		{
			"exec.Error ENOEXEC from our own spawn",
			fmt.Errorf("failed to connect: %w", &exec.Error{Name: "some-mcp-server", Err: syscall.ENOEXEC}),
			ClassifierHints{Transport: "stdio"},
			STDIOSpawnExecFormat,
		},
		{
			"the docker binary itself is missing on the direct-exec path",
			fmt.Errorf("failed to connect: %w", &exec.Error{Name: "docker", Err: syscall.ENOENT}),
			ClassifierHints{Transport: "stdio", DockerIsolated: true},
			DockerCLINotFound,
		},
		{
			"a producer attributed the code with WrapError",
			fmt.Errorf("failed to connect: %w", WrapError(ConfigInvalidCommand,
				fmt.Errorf(`server "x": command "npx" has no args — the npm package to run is required`))),
			ClassifierHints{Transport: "stdio"},
			ConfigInvalidCommand,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, parkable := ParkableCode(tt.err, tt.hints)
			assert.Equal(t, tt.want, code)
			assert.True(t, parkable, "a typed signal from mcpproxy's own code must still park")
		})
	}
}

// TestParkableCode_TransientCodesNeverPark: the gate is an AND, so a typed
// signal for a code that is not declared permanent still retries.
func TestParkableCode_TransientCodesNeverPark(t *testing.T) {
	err := fmt.Errorf("failed to connect: %w", WrapError(DockerDaemonDown,
		fmt.Errorf("cannot connect to the Docker daemon")))
	code, parkable := ParkableCode(err, ClassifierHints{Transport: "stdio"})
	assert.Equal(t, DockerDaemonDown, code)
	assert.False(t, parkable, "a transient code must not park however strong the evidence")

	code, parkable = ParkableCode(fmt.Errorf("something odd"), ClassifierHints{})
	assert.Equal(t, UnknownUnclassified, code)
	assert.False(t, parkable)

	code, parkable = ParkableCode(nil, ClassifierHints{})
	assert.Equal(t, Code(""), code)
	assert.False(t, parkable)
}
