package main

import (
	"context"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	// shutdownGraceWindow is how long we wait before reminding the operator
	// that the force-quit shortcut is still available. It does NOT bound the
	// shutdown itself.
	shutdownGraceWindow = 10 * time.Second

	// shutdownHardDeadline bounds graceful shutdown as a whole, measured from
	// the first signal. 60s is MCPProxy's chosen bound: long enough for Docker
	// container cleanup, short enough that a supervisor can restart a wedged
	// daemon instead of waiting for a human with SIGKILL. It sits under
	// systemd's 90s DefaultTimeoutStopSec, so systemd's own SIGKILL stays a
	// backstop rather than the normal outcome.
	shutdownHardDeadline = 60 * time.Second
)

// signalHandlerDeps are the collaborators of runSignalHandler. Everything that
// touches the process or the clock is injected so the handler can be unit
// tested without real signals, real sleeps or a real os.Exit.
type signalHandlerDeps struct {
	// sigChan delivers SIGINT/SIGTERM (signal.Notify in production).
	sigChan <-chan os.Signal
	// cancel triggers the graceful shutdown path in runServer.
	cancel context.CancelFunc
	// onSignal records the first signal for Spec 024 activity logging. It MUST
	// run before cancel(), because the ctx.Done() branch in runServer reads the
	// stored value as soon as it wakes up. It MUST also be fast and
	// non-blocking (a value store, in production): runSignalHandler calls it
	// before spawning the forcer goroutine, so anything it blocks on delays
	// the point at which a second signal can force an exit.
	onSignal func(os.Signal)
	// exit terminates the process (os.Exit in production).
	exit func(int)
	// logger is never nil in production; tests pass zap.NewNop().
	logger *zap.Logger
	// newTimer returns a channel that fires after the given duration. It is
	// called only AFTER the first signal arrives, so both windows are measured
	// from that signal rather than from process start. nil means time.After.
	newTimer func(time.Duration) <-chan time.Time
}

// runSignalHandler waits for the first shutdown signal, starts the graceful
// shutdown, and then KEEPS LISTENING until the process dies.
//
// Staying alive is the whole point. signal.Notify stays registered on sigChan
// for the lifetime of the process, so the moment nobody reads that channel the
// Go runtime keeps intercepting SIGINT/SIGTERM (their default disposition is
// disabled) and dropping them into a one-slot buffer. The old handler returned
// as soon as its 10s force-quit timer expired; if shutdown then ran longer
// than that - exactly when an operator is hammering Ctrl+C - the daemon became
// unkillable by anything short of SIGKILL.
//
// The three ways out:
//   - a further signal    -> immediate exit, ExitCodeGeneralError
//   - the hard deadline   -> forced exit, ExitCodeShutdownTimeout
//   - shutdown completing -> runServer returns and the process exits normally
//
// Both forced exits are decided by a small goroutine that touches nothing but
// channels. Logging is deliberately kept off that path: the failure that wedges
// shutdown (a full disk, a tray that stopped draining the core's stderr pipe)
// is the same failure that wedges the log sink, so a forced exit that had to
// get past a log write first would not be forced at all.
func runSignalHandler(d signalHandlerDeps) {
	newTimer := d.newTimer
	if newTimer == nil {
		newTimer = time.After
	}

	// Nothing may run before this receive - not even a log line. Whatever
	// blocks here blocks the only reader of sigChan.
	sig, open := <-d.sigChan
	if !open {
		return
	}
	started := time.Now()

	// Arm both windows FIRST. Every instant spent before this line is an
	// instant the "hard" deadline is not counting.
	graceWindow := newTimer(shutdownGraceWindow)
	hardDeadline := newTimer(shutdownHardDeadline)

	// exiting is closed once some path has asked the process to die. In
	// production d.exit never returns, so this only matters to the wait below
	// (and to tests): once the decision is made there is nothing left to do.
	exiting := make(chan struct{})
	var exitOnce sync.Once
	forceExit := func(code int) {
		exitOnce.Do(func() {
			d.exit(code)
			close(exiting)
		})
	}

	// handlerDone lets the forcer retire if this function returns first.
	handlerDone := make(chan struct{})
	defer close(handlerDone)

	d.onSignal(sig) // Spec 024: Store signal for activity logging (must precede cancel)
	// Start the graceful shutdown before logging, for the same reason the
	// timers are armed before logging: a blocked sink must not delay it.
	d.cancel()

	// The forcer: the only goroutine that may end the process, and the only
	// reader of sigChan from here on. It never logs.
	//
	// It is started only AFTER onSignal/cancel have returned. Go gives no
	// ordering guarantee between a newly spawned goroutine and the rest of
	// the spawning goroutine's own code, so starting it earlier let a second
	// signal that was already buffered in sigChan race the forcer against
	// onSignal/cancel - the process could exit before the first signal was
	// ever recorded or graceful shutdown ever began. onSignal and cancel are
	// both required to be fast and non-blocking (a store and a context
	// cancellation), so this does not reopen the "logging must not delay the
	// deadline" problem the timer-arming order above guards against.
	go func() {
		select {
		case _, ok := <-d.sigChan:
			if !ok {
				return
			}
			forceExit(ExitCodeGeneralError)
		case <-hardDeadline:
			forceExit(ExitCodeShutdownTimeout)
		case <-handlerDone:
		}
	}()

	// From here on this goroutine only reports. If the sink is wedged it stalls
	// here, and the forcer above still ends the process on time.
	d.logger.Info("Received signal, shutting down", zap.String("signal", sig.String()))
	_ = d.logger.Sync() // Flush logs immediately so we can see shutdown messages
	d.logger.Info("Press Ctrl+C again to force quit",
		zap.Int("force_quit_exit_code", ExitCodeGeneralError),
		zap.Duration("forced_exit_after", shutdownHardDeadline),
		zap.Int("forced_exit_code", ExitCodeShutdownTimeout))
	_ = d.logger.Sync() // Flush again

	select {
	case <-graceWindow:
		// Shutdown is taking a while. Remind the operator that Ctrl+C still
		// works - and, unlike before, it really does.
		d.logger.Info("Graceful shutdown still in progress, press Ctrl+C again to force quit",
			zap.Duration("elapsed", time.Since(started)),
			zap.Duration("forced_exit_after", shutdownHardDeadline))
		_ = d.logger.Sync()
		<-exiting
	case <-exiting:
	}
}
