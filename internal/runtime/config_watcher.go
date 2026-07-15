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

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
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

// noteConfigSelfWrite records the marshaled form of a config mcpproxy itself
// just saved to disk, so the watcher can suppress the echo even when the
// in-memory snapshot intentionally diverges from the file — the
// restart-required ApplyConfig path saves to disk but defers the in-memory
// apply until restart (`requires_restart` contract), and the watcher must not
// hot-apply it behind the API's back. Marshals exactly as config.SaveConfig
// does; a marshal failure just skips recording (the write itself would have
// failed the same way).
func (r *Runtime) noteConfigSelfWrite(cfg *config.Config) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	r.selfWriteMu.Lock()
	r.lastSelfWrite = bytes.TrimSpace(data)
	r.selfWriteMu.Unlock()
}

// matchesLastSelfWrite reports whether trimmed disk bytes equal the last
// config mcpproxy itself saved (see noteConfigSelfWrite).
func (r *Runtime) matchesLastSelfWrite(trimmedDisk []byte) bool {
	r.selfWriteMu.Lock()
	defer r.selfWriteMu.Unlock()
	return len(r.lastSelfWrite) > 0 && bytes.Equal(r.lastSelfWrite, trimmedDisk)
}

// clearLastSelfWrite drops the recorded self-write. Called when a genuine
// external change is about to reload: from that point the file's history has
// diverged from our last save, and keeping the stale record would suppress a
// later external revert to those exact bytes (editor undo, `git checkout`).
// It is NOT cleared on a suppression match itself, so the restart-required
// pending state keeps suppressing repeat events for the same bytes.
func (r *Runtime) clearLastSelfWrite() {
	r.selfWriteMu.Lock()
	r.lastSelfWrite = nil
	r.selfWriteMu.Unlock()
}

// reloadFromDiskIfChanged reloads the configuration from disk unless the file
// content matches the in-memory snapshot or the last self-written config
// (self-write suppression: our own ApplyConfig / SaveConfiguration writes
// must not echo back into a redundant reload, which would re-trigger
// ConnectAll + reindex churn — or, for restart-required applies, hot-apply a
// change the API just reported as deferred until restart).
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
	trimmedDisk := bytes.TrimSpace(diskBytes)
	if current, merr := json.MarshalIndent(r.ConfigSnapshot().Config, "", "  "); merr == nil {
		if bytes.Equal(bytes.TrimSpace(current), trimmedDisk) {
			// The file now matches memory. If that content differs from the
			// recorded self-write, the file has moved past our last save
			// (e.g. an external revert cancelling a pending restart-required
			// apply) — drop the stale marker so a later external re-write of
			// those old self-saved bytes still reloads.
			if !r.matchesLastSelfWrite(trimmedDisk) {
				r.clearLastSelfWrite()
			}
			r.logger.Debug("Config file event matches in-memory config; skipping reload",
				zap.String("path", absPath))
			return
		}
	}

	// Restart-required applies save to disk without touching memory, so the
	// snapshot comparison above can't recognize them as ours — check the
	// recorded last self-write too.
	if r.matchesLastSelfWrite(trimmedDisk) {
		r.logger.Debug("Config file event matches mcpproxy's own last save; skipping reload",
			zap.String("path", absPath))
		return
	}

	// Genuine external change: the file no longer descends from our last
	// save, so the recorded self-write is stale — drop it (see
	// clearLastSelfWrite for the revert scenario it would otherwise break).
	r.clearLastSelfWrite()

	r.logger.Info("Config file changed on disk, hot-reloading", zap.String("path", absPath))
	if err := r.ReloadConfiguration(); err != nil {
		// ReloadFromFile returns before mutating state on parse/validation
		// failure, so a bad file leaves the previous config intact.
		r.logger.Warn("Config hot-reload failed; keeping previous configuration",
			zap.String("path", absPath), zap.Error(err))
	}
}
