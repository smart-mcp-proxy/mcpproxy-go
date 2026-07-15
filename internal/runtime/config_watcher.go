package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"
)

// configWatchDebounce coalesces bursts of file events (editor write patterns,
// tmp-file + rename sequences) into a single reload, and gives truncate-write
// editors time to finish writing before we read the file.
const configWatchDebounce = 500 * time.Millisecond

// startConfigFileWatcher watches the config file for external edits and
// hot-reloads them through Runtime.ReloadConfiguration — the same canonical
// disk-reload path used by manual reloads (events, telemetry and update-check
// hooks included).
//
// It watches the parent directory rather than the file itself so that
// rename/recreate writes (`mv tmp file`, atomic-write editors, and mcpproxy's
// own atomicWriteFile) can never orphan the watch. The directory watch is
// added synchronously so callers (and tests) have no startup race; only the
// event loop runs in a goroutine, tied to ctx.
//
// On failure the watcher degrades gracefully: a warning is logged, an error is
// returned, and the server keeps running without hot-reload.
func (r *Runtime) startConfigFileWatcher(ctx context.Context, cfgPath string) error {
	absPath, err := filepath.Abs(cfgPath)
	if err != nil {
		r.logger.Warn("Config file watcher unavailable; external edits will not hot-reload",
			zap.String("path", cfgPath), zap.Error(err))
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		r.logger.Warn("Config file watcher unavailable; external edits will not hot-reload",
			zap.String("path", absPath), zap.Error(err))
		return err
	}

	if err := watcher.Add(filepath.Dir(absPath)); err != nil {
		_ = watcher.Close()
		r.logger.Warn("Config file watcher unavailable; external edits will not hot-reload",
			zap.String("path", absPath), zap.Error(err))
		return err
	}

	r.logger.Info("Config file watcher started", zap.String("path", absPath))
	go r.runConfigWatchLoop(ctx, watcher, absPath)
	return nil
}

// runConfigWatchLoop is the watcher event loop: it filters events down to the
// config file, debounces them, and triggers reloadFromDiskIfChanged when the
// debounce window closes.
func (r *Runtime) runConfigWatchLoop(ctx context.Context, watcher *fsnotify.Watcher, absPath string) {
	defer func() { _ = watcher.Close() }()

	// Debounce timer, created stopped. Go 1.23+ timer semantics make
	// Stop/Reset race-free without channel draining.
	debounce := time.NewTimer(configWatchDebounce)
	if !debounce.Stop() {
		<-debounce.C
	}
	defer debounce.Stop()

	// Create catches mv/rename-in; Write catches in-place truncate writes;
	// Rename/Remove re-arm the debounce so the follow-up recreate isn't
	// missed. Chmod is deliberately ignored.
	const relevantOps = fsnotify.Create | fsnotify.Write | fsnotify.Rename | fsnotify.Remove

	for {
		select {
		case <-ctx.Done():
			return

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			r.logger.Warn("Config file watcher error", zap.Error(err))

		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			if filepath.Clean(ev.Name) != absPath || ev.Op&relevantOps == 0 {
				continue
			}
			debounce.Reset(configWatchDebounce)

		case <-debounce.C:
			r.reloadFromDiskIfChanged(absPath)
		}
	}
}

// reloadFromDiskIfChanged reloads the configuration from disk unless the file
// content matches the in-memory snapshot (self-write suppression: our own
// ApplyConfig / SaveConfiguration writes must not echo back into a redundant
// reload, which would re-trigger ConnectAll + reindex churn).
func (r *Runtime) reloadFromDiskIfChanged(absPath string) {
	diskBytes, err := os.ReadFile(absPath)
	if err != nil {
		// Rename window — the file may be briefly absent; the follow-up
		// Create event re-arms the debounce and we retry then.
		r.logger.Debug("Config file not readable after change event; waiting for next event",
			zap.String("path", absPath), zap.Error(err))
		return
	}

	// Self-write suppression: marshal the current snapshot exactly as
	// config.SaveConfig does and byte-compare with disk. Equal bytes mean the
	// event came from our own save. If a future save path diverges, this
	// degrades to one redundant (idempotent) reload — never a loop, since
	// ReloadConfiguration never writes the file.
	if current, merr := json.MarshalIndent(r.ConfigSnapshot().Config, "", "  "); merr == nil {
		if bytes.Equal(bytes.TrimSpace(current), bytes.TrimSpace(diskBytes)) {
			r.logger.Debug("Config file event matches in-memory config; skipping reload",
				zap.String("path", absPath))
			return
		}
	}

	r.logger.Info("Config file changed on disk, hot-reloading", zap.String("path", absPath))
	if err := r.ReloadConfiguration(); err != nil {
		// ReloadFromFile returns before mutating state on parse/validation
		// failure, so a bad file leaves the previous config intact.
		r.logger.Warn("Config hot-reload failed; keeping previous configuration",
			zap.String("path", absPath), zap.Error(err))
	}
}
