package diagnostics

import (
	"errors"
	"testing"
)

// The loose "handshake timeout" substring is unbounded child stderr — Go's own
// `net/http: TLS handshake timeout` carries it, and a docker image pull over
// TLS prints exactly that. When mcpproxy's OWN premature-exit wrapper is also
// present the child provably died, so the timeout reading is impossible: the
// text came from the stderr tail. mcpproxy never writes both wrappers, so their
// co-occurrence is proof, not ambiguity.
//
// Letting the loose key win there both suppressed every docker arm and claimed
// the leading arm of classifyStdio, turning a real image-pull failure into
// "raise your timeout".
func TestClassify_LooseHandshakeTimeoutInStderrDoesNotBeatTheExitWrapper(t *testing.T) {
	const pfx = `stdio transport (command="/usr/local/bin/docker", docker_isolation=true): `
	const exited = "server process exited before completing the MCP initialize handshake; recent stderr:\n  | "
	hints := ClassifierHints{Transport: "stdio", DockerIsolated: true}

	for _, tc := range []struct {
		name, stderr string
		want         Code
	}{
		{
			// Falls through to the premature-exit code rather than an image-pull
			// one: this wording carries no image/pull/fail evidence for
			// dockerLayerCode to key on. That is the same answer clean main
			// gives, so no diagnosis is lost — the point here is only that the
			// stderr's "TLS handshake timeout" must not claim the timeout code.
			name:   "TLS handshake timeout pulling the image",
			stderr: `docker: Error response from daemon: Get "https://ghcr.io/v2/": net/http: TLS handshake timeout.`,
			want:   STDIOExitBeforeInitialize,
		},
		{
			name:   "app-level handshake timeout next to a real exec failure",
			stderr: `app: websocket handshake timeout` + "\n  | " + `docker: OCI runtime create failed: exec: "uvx": executable file not found in $PATH`,
			want:   DockerExecNotFound,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(errors.New(pfx+exited+tc.stderr+": transport closed"), hints)
			if got == STDIOHandshakeTimeout {
				t.Fatalf("stderr text won the timeout guard on the EXIT path: got %s, want %s", got, tc.want)
			}
			if got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// mcpproxy's own timeout wrapper must still classify as a timeout.
func TestClassify_RealHandshakeTimeoutStillClassifies(t *testing.T) {
	const pfx = `stdio transport (command="/usr/local/bin/docker", docker_isolation=true): `
	err := errors.New(pfx + "server did not respond to MCP initialize within 30s (subprocess may have crashed); recent stderr:\n  | still loading")
	if got := Classify(err, ClassifierHints{Transport: "stdio", DockerIsolated: true}); got != STDIOHandshakeTimeout {
		t.Fatalf("real timeout lost its code: got %s, want %s", got, STDIOHandshakeTimeout)
	}
}
