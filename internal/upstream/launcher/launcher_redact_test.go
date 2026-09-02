package launcher

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// syncBuf is a race-free sink: Spawn's banner and the two pump goroutines all
// write to it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestSpawnBanner_OmitsArgvWithoutARedactor pins the fail-CLOSED default.
//
// #1158: the `[launcher] starting:` banner wrote the child's FULLY TRANSFORMED
// argv — post docker env->argv injection, post login-shell wrap — into
// ~/.mcpproxy/logs/server-<name>.log on every spawn. This package is
// deliberately dependency-free, so the redactor is injected; a caller that
// forgets to set it must lose the diagnostic, not the secret.
func TestSpawnBanner_OmitsArgvWithoutARedactor(t *testing.T) {
	const secret = "SUPERSECRETBETAARGVVALUE"
	sink := &syncBuf{}

	spec := &Spec{
		Cmd:     exec.Command("/bin/echo", "--api-key", secret),
		LogSink: sink,
		Name:    "no-redactor",
		// RedactArgs deliberately unset.
	}
	h, err := Spawn(context.Background(), spec, zap.NewNop())
	require.NoError(t, err)
	_ = h.Wait()
	require.NoError(t, waitForBanner(sink))

	out := sink.String()
	banner := bannerLine(t, out)
	assert.NotContains(t, banner, secret,
		"a nil redactor must not publish argv: %s", banner)
	assert.Contains(t, banner, "/bin/echo",
		"the command path is the diagnostic that must survive")
	assert.Contains(t, banner, "redacted")
}

// TestSpawnBanner_UsesTheSuppliedRedactor proves the hook is actually wired to
// the banner (and not, say, applied to a copy that is then discarded).
func TestSpawnBanner_UsesTheSuppliedRedactor(t *testing.T) {
	const secret = "SUPERSECRETBETAARGVVALUE"
	sink := &syncBuf{}

	spec := &Spec{
		Cmd:     exec.Command("/bin/echo", "--api-key", secret),
		LogSink: sink,
		Name:    "with-redactor",
		RedactArgs: func(args []string) []string {
			out := make([]string, len(args))
			for i, a := range args {
				if a == secret {
					out[i] = "MASKED"
					continue
				}
				out[i] = a
			}
			return out
		},
	}
	h, err := Spawn(context.Background(), spec, zap.NewNop())
	require.NoError(t, err)
	_ = h.Wait()
	require.NoError(t, waitForBanner(sink))

	banner := bannerLine(t, sink.String())
	assert.NotContains(t, banner, secret)
	assert.Contains(t, banner, "MASKED", "the banner must render the redactor's output")
	assert.Contains(t, banner, "--api-key", "the flag name must still be readable")
}

func waitForBanner(sink *syncBuf) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(sink.String(), "[launcher] starting:") {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return context.DeadlineExceeded
}

func bannerLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "[launcher] starting:") {
			return line
		}
	}
	t.Fatalf("no launcher banner in sink output: %q", out)
	return ""
}
