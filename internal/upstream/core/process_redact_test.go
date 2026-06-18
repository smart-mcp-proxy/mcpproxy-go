//go:build unix

package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// TestProcessGroupCommandFunc_RedactsSecretArgs is a guard test for GH #712:
// the "Process group configuration applied" debug log must not emit cleartext
// upstream secrets passed as `-e KEY=VALUE` to docker-command upstreams.
func TestProcessGroupCommandFunc_RedactsSecretArgs(t *testing.T) {
	const secretValue = "xoxc-abc123456789"

	obsCore, recorded := observer.New(zapcore.DebugLevel)
	logger := zap.New(obsCore)

	// client=nil keeps this off the spawn path: the CommandFunc builds the
	// *exec.Cmd and logs but never starts a process.
	fn := createProcessGroupCommandFunc(nil, "", logger)

	cases := [][]string{
		// Shell-wrapped form (secret embedded in the -c command string).
		{"-l", "-c", "docker run --rm -e SLACK_TOKEN=" + secretValue + " myimage"},
		// Direct-exec form (two-token -e KEY=VALUE).
		{"run", "--rm", "-e", "SLACK_TOKEN=" + secretValue, "myimage"},
	}
	for _, args := range cases {
		if _, err := fn(context.Background(), "/bin/sh", nil, args); err != nil {
			t.Fatalf("CommandFunc returned error: %v", err)
		}
	}

	for _, entry := range recorded.All() {
		for _, v := range entry.ContextMap() {
			if strings.Contains(fmt.Sprintf("%v", v), secretValue) {
				t.Errorf("cleartext secret leaked in log field: %v", v)
			}
		}
	}
}
