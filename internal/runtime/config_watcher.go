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

// Self-write suppression bounds. Entries expire after selfWriteTTL: the
// suppression only needs to cover the echo window of our own write (the
// ~500ms debounce plus fs latency), and any event long after that is an
// external edit — 10s is generous. The set keeps at most selfWriteMaxEntries
// payloads so back-to-back ApplyConfig saves cannot evict each other's
// still-pending markers (single-slot race, PR #857 round 7).
const (
	selfWriteTTL        = 10 * time.Second
	selfWriteMaxEntries = 4
)

// selfWriteEntry is one recorded self-written config payload (trimmed
// marshaled bytes) with the time it was armed, for TTL expiry.
type selfWriteEntry struct {
	payload []byte
	at      time.Time
}

// noteConfigSelfWrite records the marshaled form of a config mcpproxy itself
// is about to save to disk, so the watcher can suppress the echo even when
// the in-memory snapshot intentionally diverges from the file — the
// restart-required ApplyConfig path saves to disk but defers the in-memory
// apply until restart (`requires_restart` contract), and the watcher must not
// hot-apply it behind the API's back. Marshals exactly as config.SaveConfig
// does; a marshal failure just skips recording (the write itself would have
// failed the same way). Recording an already-present payload refreshes its
// timestamp; when full, the oldest entry is evicted.
func (r *Runtime) noteConfigSelfWrite(cfg *config.Config) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	payload := bytes.TrimSpace(data)
	now := time.Now()

	r.selfWriteMu.Lock()
	defer r.selfWriteMu.Unlock()
	r.pruneExpiredSelfWritesLocked(now)
	for i := range r.recentSelfWrites {
		if bytes.Equal(r.recentSelfWrites[i].payload, payload) {
			r.recentSelfWrites[i].at = now
			return
		}
	}
	r.recentSelfWrites = append(r.recentSelfWrites, selfWriteEntry{payload: payload, at: now})
	if len(r.recentSelfWrites) > selfWriteMaxEntries {
		r.recentSelfWrites = r.recentSelfWrites[len(r.recentSelfWrites)-selfWriteMaxEntries:]
	}
}

// forgetConfigSelfWrite removes the entry for a config whose save FAILED:
// those bytes never reached disk, so a later byte-identical write of them is
// a genuine external edit the watcher must reload. Only the failed payload is
// removed — markers pre-armed by other (successful) saves stay live.
func (r *Runtime) forgetConfigSelfWrite(cfg *config.Config) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	payload := bytes.TrimSpace(data)

	r.selfWriteMu.Lock()
	defer r.selfWriteMu.Unlock()
	kept := r.recentSelfWrites[:0]
	for _, e := range r.recentSelfWrites {
		if !bytes.Equal(e.payload, payload) {
			kept = append(kept, e)
		}
	}
	r.recentSelfWrites = kept
}

// matchesRecentSelfWrite reports whether trimmed disk bytes equal any config
// mcpproxy itself recently saved (see noteConfigSelfWrite). A match does NOT
// remove the entry: within the TTL, repeat events for the same bytes (e.g.
// the pending restart-required state) must keep suppressing.
func (r *Runtime) matchesRecentSelfWrite(trimmedDisk []byte) bool {
	r.selfWriteMu.Lock()
	defer r.selfWriteMu.Unlock()
	r.pruneExpiredSelfWritesLocked(time.Now())
	for _, e := range r.recentSelfWrites {
		if bytes.Equal(e.payload, trimmedDisk) {
			return true
		}
	}
	return false
}

// clearSelfWrites drops every recorded self-write. Called when a genuine
// external change is about to reload: from that point the file's history has
// diverged from our saves, and keeping the stale records would suppress a
// later external revert to those exact bytes (editor undo, `git checkout`).
func (r *Runtime) clearSelfWrites() {
	r.selfWriteMu.Lock()
	r.recentSelfWrites = nil
	r.selfWriteMu.Unlock()
}

// pruneExpiredSelfWritesLocked drops entries older than selfWriteTTL. Caller
// must hold selfWriteMu.
func (r *Runtime) pruneExpiredSelfWritesLocked(now time.Time) {
	kept := r.recentSelfWrites[:0]
	for _, e := range r.recentSelfWrites {
		if now.Sub(e.at) <= selfWriteTTL {
			kept = append(kept, e)
		}
	}
	r.recentSelfWrites = kept
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
			// The file now matches memory. If that content matches none of
			// the recorded self-writes, the file has moved past our saves
			// (e.g. an external revert cancelling a pending restart-required
			// apply) — drop the stale markers so a later external re-write of
			// those old self-saved bytes still reloads.
			if !r.matchesRecentSelfWrite(trimmedDisk) {
				r.clearSelfWrites()
			}
			r.logger.Debug("Config file event matches in-memory config; skipping reload",
				zap.String("path", absPath))
			return
		}
	}

	// Restart-required applies save to disk without touching memory, so the
	// snapshot comparison above can't recognize them as ours — check the
	// recorded recent self-writes too.
	if r.matchesRecentSelfWrite(trimmedDisk) {
		r.logger.Debug("Config file event matches one of mcpproxy's own recent saves; skipping reload",
			zap.String("path", absPath))
		return
	}

	// Genuine external change: the file no longer descends from our saves,
	// so the recorded self-writes are stale — drop them (see clearSelfWrites
	// for the revert scenario they would otherwise break).
	r.clearSelfWrites()

	r.logger.Info("Config file changed on disk, hot-reloading", zap.String("path", absPath))
	if err := r.ReloadConfiguration(); err != nil {
		// ReloadFromFile returns before mutating state on parse/validation
		// failure, so a bad file leaves the previous config intact.
		r.logger.Warn("Config hot-reload failed; keeping previous configuration",
			zap.String("path", absPath), zap.Error(err))
	}
}
