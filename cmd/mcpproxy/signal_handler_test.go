package main

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// signalTestGuard is the deadlock guard for every blocking step in these
// tests. It is NOT the assertion mechanism: every synchronisation point below
// is a channel handshake, so a correct handler never comes anywhere near this
// bound.
const signalTestGuard = 2 * time.Second

type signalHandlerHarness struct {
	sigChan      chan os.Signal
	graceWindow  chan time.Time
	hardDeadline chan time.Time

	cancelled chan struct{}
	exited    chan int
	returned  chan struct{}

	order          atomic.Int32
	onSignalOrder  atomic.Int32
	cancelOrder    atomic.Int32
	timersArmed    atomic.Int32
	observedSignal atomic.Value
}

func newSignalHandlerHarness(t *testing.T) *signalHandlerHarness {
	t.Helper()
	return newSignalHandlerHarnessWithLogger(t, zap.NewNop())
}

func newSignalHandlerHarnessWithLogger(t *testing.T, logger *zap.Logger) *signalHandlerHarness {
	t.Helper()

	h := &signalHandlerHarness{
		// Buffered like the production channel (signal.Notify needs a buffer).
		sigChan: make(chan os.Signal, 1),
		// Unbuffered on purpose: a send from the test blocks until the handler
		// actually receives it, which makes every step deterministic.
		graceWindow:  make(chan time.Time),
		hardDeadline: make(chan time.Time),
		cancelled:    make(chan struct{}),
		exited:       make(chan int, 4),
		returned:     make(chan struct{}),
	}

	deps := signalHandlerDeps{
		sigChan: h.sigChan,
		cancel: func() {
			h.cancelOrder.Store(h.order.Add(1))
			select {
			case <-h.cancelled:
			default:
				close(h.cancelled)
			}
		},
		onSignal: func(sig os.Signal) {
			h.onSignalOrder.Store(h.order.Add(1))
			h.observedSignal.Store(sig.String())
		},
		exit:   func(code int) { h.exited <- code },
		logger: logger,
		newTimer: func(dur time.Duration) <-chan time.Time {
			h.timersArmed.Add(1)
			switch dur {
			case shutdownGraceWindow:
				return h.graceWindow
			case shutdownHardDeadline:
				return h.hardDeadline
			default:
				t.Errorf("handler armed an unexpected timer: %s", dur)
				return nil
			}
		},
	}

	go func() {
		defer close(h.returned)
		runSignalHandler(deps)
	}()

	return h
}

func (h *signalHandlerHarness) sendSignal(t *testing.T, sig os.Signal) {
	t.Helper()
	select {
	case h.sigChan <- sig:
	case <-time.After(signalTestGuard):
		t.Fatalf("timed out delivering %s: nobody is reading the signal channel", sig)
	}
}

// fire simulates a timer expiring. The send blocks until the handler receives
// it, so a handler that has stopped listening fails here.
func (h *signalHandlerHarness) fire(t *testing.T, ch chan time.Time, what string) {
	t.Helper()
	select {
	case ch <- time.Now():
	case <-time.After(signalTestGuard):
		t.Fatalf("timed out firing %s: the handler is no longer listening on it", what)
	}
}

func (h *signalHandlerHarness) waitCancelled(t *testing.T) {
	t.Helper()
	select {
	case <-h.cancelled:
	case <-time.After(signalTestGuard):
		t.Fatal("graceful shutdown was never started (cancel was not called)")
	}
}

func (h *signalHandlerHarness) waitExit(t *testing.T) int {
	t.Helper()
	select {
	case code := <-h.exited:
		return code
	case <-time.After(signalTestGuard):
		t.Fatal("the process was never asked to exit")
		return -1
	}
}

func (h *signalHandlerHarness) waitReturned(t *testing.T) {
	t.Helper()
	select {
	case <-h.returned:
	case <-time.After(signalTestGuard):
		t.Fatal("runSignalHandler did not return after exiting (goroutine leak)")
	}
}

// TestRunSignalHandler_SecondSignalForcesImmediateExit pins the existing
// force-quit behaviour: the second signal kills the process right away.
func TestRunSignalHandler_SecondSignalForcesImmediateExit(t *testing.T) {
	h := newSignalHandlerHarness(t)

	h.sendSignal(t, syscall.SIGINT)
	h.waitCancelled(t)
	h.sendSignal(t, syscall.SIGTERM)

	if code := h.waitExit(t); code != ExitCodeGeneralError {
		t.Fatalf("second signal exit code = %d, want %d", code, ExitCodeGeneralError)
	}
	h.waitReturned(t)

	select {
	case code := <-h.exited:
		t.Fatalf("exit called more than once (extra code %d)", code)
	default:
	}
}

// TestRunSignalHandler_SignalAfterGraceWindowStillHonored is the regression
// test for the one-shot handler. Once the grace window elapses the old handler
// returned, leaving signal.Notify registered on a channel nobody reads: the
// runtime then swallows SIGINT/SIGTERM and the daemon can only be SIGKILLed.
func TestRunSignalHandler_SignalAfterGraceWindowStillHonored(t *testing.T) {
	h := newSignalHandlerHarness(t)

	h.sendSignal(t, syscall.SIGINT)
	h.waitCancelled(t)

	// The 10s "press Ctrl+C again" window elapses while shutdown is still
	// running (e.g. Docker container cleanup is slow).
	h.fire(t, h.graceWindow, "grace window")

	// The operator hits Ctrl+C again. It must still force the exit.
	h.sendSignal(t, syscall.SIGTERM)

	if code := h.waitExit(t); code != ExitCodeGeneralError {
		t.Fatalf("post-grace-window signal exit code = %d, want %d", code, ExitCodeGeneralError)
	}
	h.waitReturned(t)
}

// TestRunSignalHandler_HardDeadlineExits pins the new upper bound on graceful
// shutdown: when it fires the process exits with a distinct code so a
// supervisor (launchd/systemd) can tell a hung shutdown from a plain failure.
func TestRunSignalHandler_HardDeadlineExits(t *testing.T) {
	if ExitCodeShutdownTimeout == ExitCodeGeneralError {
		t.Fatalf("ExitCodeShutdownTimeout must be distinct from ExitCodeGeneralError (%d)", ExitCodeGeneralError)
	}

	h := newSignalHandlerHarness(t)

	h.sendSignal(t, syscall.SIGINT)
	h.waitCancelled(t)
	h.fire(t, h.graceWindow, "grace window")
	h.fire(t, h.hardDeadline, "hard deadline")

	if code := h.waitExit(t); code != ExitCodeShutdownTimeout {
		t.Fatalf("hard deadline exit code = %d, want %d", code, ExitCodeShutdownTimeout)
	}
	h.waitReturned(t)
}

// TestRunSignalHandler_DeadlinesStartAtFirstSignal pins where the clock
// starts. Arming the timers at process start instead would make the hard
// deadline fire ~60s into a healthy run and then kill the daemon the instant
// the first Ctrl+C arrived.
func TestRunSignalHandler_DeadlinesStartAtFirstSignal(t *testing.T) {
	h := newSignalHandlerHarness(t)

	// No signal yet, so the handler is still blocked on the receive and cannot
	// have armed anything. This read cannot race: arming requires the send.
	if armed := h.timersArmed.Load(); armed != 0 {
		t.Fatalf("handler armed %d timer(s) before the first signal", armed)
	}

	h.sendSignal(t, syscall.SIGINT)
	h.waitCancelled(t)

	if armed := h.timersArmed.Load(); armed != 2 {
		t.Fatalf("handler armed %d timer(s) after the first signal, want 2 (grace window + hard deadline)", armed)
	}

	h.sendSignal(t, syscall.SIGTERM)
	h.waitExit(t)
	h.waitReturned(t)
}

// TestRunSignalHandler_BufferedSecondSignalWaitsForCallback reproduces a
// review finding: the forcer goroutine used to be spawned BEFORE onSignal and
// cancel ran, and Go gives no ordering guarantee between a freshly spawned
// goroutine and the rest of the spawning goroutine's own code. If a second
// signal was already sitting in the (buffered, capacity-1) signal channel by
// the time onSignal/cancel completed, the forcer could dequeue it and call
// exit first -- the process could die before Spec 024 activity logging ever
// recorded the first signal or graceful shutdown ever started.
func TestRunSignalHandler_BufferedSecondSignalWaitsForCallback(t *testing.T) {
	release := make(chan struct{})
	onSignalCalled := make(chan struct{})

	h := &signalHandlerHarness{
		sigChan:      make(chan os.Signal, 1),
		graceWindow:  make(chan time.Time),
		hardDeadline: make(chan time.Time),
		cancelled:    make(chan struct{}),
		exited:       make(chan int, 4),
		returned:     make(chan struct{}),
	}

	deps := signalHandlerDeps{
		sigChan: h.sigChan,
		cancel: func() {
			select {
			case <-h.cancelled:
			default:
				close(h.cancelled)
			}
		},
		onSignal: func(sig os.Signal) {
			close(onSignalCalled)
			<-release // held open until the test releases it
		},
		exit:   func(code int) { h.exited <- code },
		logger: zap.NewNop(),
		newTimer: func(dur time.Duration) <-chan time.Time {
			h.timersArmed.Add(1)
			switch dur {
			case shutdownGraceWindow:
				return h.graceWindow
			case shutdownHardDeadline:
				return h.hardDeadline
			default:
				t.Errorf("handler armed an unexpected timer: %s", dur)
				return nil
			}
		},
	}

	go func() {
		defer close(h.returned)
		runSignalHandler(deps)
	}()

	// First signal.
	h.sendSignal(t, syscall.SIGINT)

	// Wait until onSignal has actually been entered (and is now blocked) so we
	// know the handler is past the initial receive and has armed its timers.
	select {
	case <-onSignalCalled:
	case <-time.After(signalTestGuard):
		t.Fatal("onSignal was never invoked")
	}

	// A second signal is already buffered before onSignal/cancel completed --
	// the exact race the finding describes.
	h.sendSignal(t, syscall.SIGTERM)

	// The process must NOT exit while onSignal/cancel have not finished.
	select {
	case code := <-h.exited:
		t.Fatalf("process exited (code %d) before the shutdown callback (onSignal/cancel) completed", code)
	case <-time.After(200 * time.Millisecond):
	}

	close(release)

	if code := h.waitExit(t); code != ExitCodeGeneralError {
		t.Fatalf("exit code after callback completed = %d, want %d", code, ExitCodeGeneralError)
	}
	h.waitReturned(t)
}

// blockingSyncer is a zapcore.WriteSyncer whose Write blocks for entries
// containing blockOn, so a test can hold the handler inside a log call.
type blockingSyncer struct {
	blockOn string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingSyncer) Write(p []byte) (int, error) {
	if strings.Contains(string(p), b.blockOn) {
		b.once.Do(func() { close(b.entered) })
		<-b.release
	}
	return len(p), nil
}

func (b *blockingSyncer) Sync() error { return nil }

func newBlockingLogger(syncer *blockingSyncer) *zap.Logger {
	return zap.New(zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		syncer,
		zapcore.InfoLevel,
	))
}

// TestRunSignalHandler_ArmsDeadlinesBeforeLogging pins the ordering that makes
// the hard deadline actually hard: a log write can block (full disk, stalled
// pipe to the tray), and any blocking done before the timers are armed is time
// the deadline is not counting.
func TestRunSignalHandler_ArmsDeadlinesBeforeLogging(t *testing.T) {
	syncer := &blockingSyncer{
		blockOn: "Received signal",
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	h := newSignalHandlerHarnessWithLogger(t, newBlockingLogger(syncer))
	h.sendSignal(t, syscall.SIGINT)

	// The handler is now wedged inside its first post-signal log call.
	select {
	case <-syncer.entered:
	case <-time.After(signalTestGuard):
		t.Fatal("handler never reached the post-signal log call")
	}

	if armed := h.timersArmed.Load(); armed != 2 {
		t.Fatalf("timers armed = %d while logging is blocked, want 2: the deadline clock "+
			"must start before anything that can block", armed)
	}

	close(syncer.release)
	h.waitCancelled(t)
	h.sendSignal(t, syscall.SIGTERM)
	h.waitExit(t)
	h.waitReturned(t)
}

// TestRunSignalHandler_HardDeadlineExitsWhileLoggingIsBlocked is the reason
// the deadline is enforced by its own goroutine. The failure mode that wedges
// shutdown (a full disk, a tray that stopped draining the core's stderr pipe)
// is exactly the one that wedges the log sink, so a deadline that has to get
// past a log call first is not a deadline at all.
func TestRunSignalHandler_HardDeadlineExitsWhileLoggingIsBlocked(t *testing.T) {
	syncer := &blockingSyncer{
		blockOn: "Received signal",
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	h := newSignalHandlerHarnessWithLogger(t, newBlockingLogger(syncer))

	h.sendSignal(t, syscall.SIGINT)

	select {
	case <-syncer.entered:
	case <-time.After(signalTestGuard):
		t.Fatal("handler never reached the post-signal log call")
	}

	// Graceful shutdown must already have been started, before the blocked log
	// call rather than after it.
	h.waitCancelled(t)

	// The handler goroutine is stuck inside logging. The deadline must still
	// kill the process.
	h.fire(t, h.hardDeadline, "hard deadline")
	if code := h.waitExit(t); code != ExitCodeShutdownTimeout {
		t.Fatalf("hard deadline exit code = %d, want %d", code, ExitCodeShutdownTimeout)
	}

	close(syncer.release)
	h.waitReturned(t)
}

// TestRunSignalHandler_SecondSignalExitsWhileLoggingIsBlocked is the operator
// half of the same guarantee: Ctrl+C twice must kill the process even when
// every log write is stalled. The sink here blocks on EVERY entry, so the
// handler goroutine cannot get past its own announcement.
func TestRunSignalHandler_SecondSignalExitsWhileLoggingIsBlocked(t *testing.T) {
	syncer := &blockingSyncer{
		blockOn: "", // every entry blocks
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	h := newSignalHandlerHarnessWithLogger(t, newBlockingLogger(syncer))

	h.sendSignal(t, syscall.SIGINT)

	// Graceful shutdown starts before any logging, so this must be reached
	// even though the sink is dead.
	h.waitCancelled(t)

	select {
	case <-syncer.entered:
	case <-time.After(signalTestGuard):
		t.Fatal("handler never reached its first log call")
	}

	h.sendSignal(t, syscall.SIGTERM)
	if code := h.waitExit(t); code != ExitCodeGeneralError {
		t.Fatalf("second signal exit code = %d, want %d", code, ExitCodeGeneralError)
	}

	close(syncer.release)
	h.waitReturned(t)
}

// TestRunSignalHandler_StoresSignalBeforeCancel guards Spec 024: runServer's
// ctx.Done() branch reads receivedSignal with an unchecked type assertion as
// soon as cancel() fires, so the store has to happen first.
func TestRunSignalHandler_StoresSignalBeforeCancel(t *testing.T) {
	h := newSignalHandlerHarness(t)

	h.sendSignal(t, syscall.SIGINT)
	h.waitCancelled(t)

	onSignalOrder := h.onSignalOrder.Load()
	cancelOrder := h.cancelOrder.Load()
	if onSignalOrder == 0 {
		t.Fatal("onSignal was never called: Spec 024 activity logging would see an empty signal")
	}
	if onSignalOrder > cancelOrder {
		t.Fatalf("onSignal ran after cancel (order %d vs %d); runServer would read a stale signal",
			onSignalOrder, cancelOrder)
	}
	if got := h.observedSignal.Load(); got != syscall.SIGINT.String() {
		t.Fatalf("recorded signal = %v, want %v", got, syscall.SIGINT.String())
	}

	// Unblock the handler so the goroutine does not outlive the test.
	h.sendSignal(t, syscall.SIGTERM)
	h.waitExit(t)
	h.waitReturned(t)
}
