package launcher

import (
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSpawn_Stop_GracefulExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("graceful-exit test relies on POSIX signal semantics")
	}

	// `sleep 30` ignores nothing — SIGTERM kills it cleanly. Good for
	// covering the SIGTERM-then-Wait happy path.
	cmd := exec.Command("sleep", "30")
	var sink bytes.Buffer
	h, err := Spawn(context.Background(), &Spec{
		Cmd:       cmd,
		LogSink:   &sink,
		Name:      "test-sleep",
		StopGrace: 2 * time.Second,
	}, zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, h)
	require.Greater(t, h.Pid(), 0)

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = h.Stop(stopCtx)
	assert.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(time.Second):
		t.Fatal("Done() not closed after Stop returned")
	}
	assert.Equal(t, 0, h.Pid(), "pid should reset after exit")
}

func TestSpawn_Stop_SIGKILLFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGKILL fallback test relies on POSIX signal semantics")
	}

	// trap '' TERM → ignore SIGTERM. The launcher must escalate to
	// SIGKILL after StopGrace expires.
	//
	// The script prints "ready" once the trap is installed so we can
	// synchronize Stop with the trap being live — otherwise a
	// vanishingly fast test runner could SIGTERM the shell before it
	// has even parsed the trap directive, which would just terminate
	// it and complete Stop in microseconds.
	// Detecting "trap installed and loop running" via stdout sniffing
	// is fragile: the launcher banner echoes the script verbatim, so
	// any literal marker in the script also appears in the banner.
	// Match the marker as a shell-substituted PID-bracketed token via
	// regex — only the dash-substituted line will match, since the
	// banner shows the un-expanded `$$`.
	const marker = `__LNCTICK__`
	script := `trap '' TERM; while true; do printf '%s\n' "` + marker + `:$$"; sleep 0.1; done`

	// Use a custom matcher that requires marker + ":" + at-least-one-digit.
	sinkCh := make(chan struct{}, 1)
	sink := newRegexDetector(`__LNCTICK__:[0-9]+`, sinkCh)

	h, err := Spawn(context.Background(), &Spec{
		Cmd:       exec.Command("sh", "-c", script),
		Name:      "term-ignorer",
		StopGrace: 300 * time.Millisecond,
		LogSink:   sink,
	}, zap.NewNop())
	require.NoError(t, err)

	select {
	case <-sinkCh:
	case <-time.After(2 * time.Second):
		t.Fatal("script never produced a ticked marker — trap not installed?")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	err = h.Stop(stopCtx)
	elapsed := time.Since(start)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, 300*time.Millisecond, "should have waited at least one grace period")
	assert.Less(t, elapsed, 2*time.Second, "should escalate to SIGKILL promptly after grace expires")
}

// regexDetector closes its channel the first time it sees a Write whose
// payload matches the given regex. Used to detect shell-substituted tokens
// without false-positiving on the launcher's startup banner (which echoes
// the script source un-expanded).
type regexDetector struct {
	re   *regexp.Regexp
	ch   chan<- struct{}
	once sync.Once
}

func newRegexDetector(pattern string, ch chan<- struct{}) *regexDetector {
	return &regexDetector{re: regexp.MustCompile(pattern), ch: ch}
}

func (d *regexDetector) Write(p []byte) (int, error) {
	if d.re.Match(p) {
		d.once.Do(func() { close(d.ch) })
	}
	return len(p), nil
}

func TestSpawn_DoneOnNaturalExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /usr/bin/true — POSIX-specific")
	}
	cmd := exec.Command("sh", "-c", "exit 0")
	h, err := Spawn(context.Background(), &Spec{Cmd: cmd, Name: "exit-zero"}, zap.NewNop())
	require.NoError(t, err)

	select {
	case <-h.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done() not closed after natural exit")
	}
	assert.NoError(t, h.Wait(), "exit 0 should not produce a wait error")
}

func TestSpawn_DoneOnNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh -c — POSIX-specific")
	}
	cmd := exec.Command("sh", "-c", "exit 17")
	h, err := Spawn(context.Background(), &Spec{Cmd: cmd, Name: "exit-17"}, zap.NewNop())
	require.NoError(t, err)

	<-h.Done()
	werr := h.Wait()
	require.Error(t, werr)
	exitErr, ok := werr.(*exec.ExitError)
	require.True(t, ok, "expected *exec.ExitError, got %T", werr)
	assert.Equal(t, 17, exitErr.ExitCode())
}

func TestSpawn_StopIsIdempotent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-specific")
	}
	cmd := exec.Command("sleep", "30")
	h, err := Spawn(context.Background(), &Spec{Cmd: cmd, Name: "sleeper"}, zap.NewNop())
	require.NoError(t, err)

	var wg sync.WaitGroup
	wg.Add(3)
	for i := 0; i < 3; i++ {
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = h.Stop(ctx)
		}()
	}
	wg.Wait()
	select {
	case <-h.Done():
	default:
		t.Fatal("Done not closed after concurrent Stop calls")
	}
}

func TestSpawn_LogSinkCaptured(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-specific")
	}
	cmd := exec.Command("sh", "-c", "echo hello-stdout; echo hello-stderr 1>&2; exit 0")
	var sink bytes.Buffer
	h, err := Spawn(context.Background(), &Spec{
		Cmd:     cmd,
		LogSink: &sink,
		Name:    "loud",
	}, zap.NewNop())
	require.NoError(t, err)

	<-h.Done()
	out := sink.String()
	assert.Contains(t, out, "hello-stdout")
	assert.Contains(t, out, "hello-stderr")
	assert.Contains(t, out, "[launcher stdout]")
	assert.Contains(t, out, "[launcher stderr]")
}

func TestSpawn_NilSpec(t *testing.T) {
	h, err := Spawn(context.Background(), nil, zap.NewNop())
	assert.Error(t, err)
	assert.Nil(t, h)
}

func TestSpawn_NilCmd(t *testing.T) {
	h, err := Spawn(context.Background(), &Spec{}, zap.NewNop())
	assert.Error(t, err)
	assert.Nil(t, h)
}
