package telemetry

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// autostartCacheTTL is the max age of a cached /autostart result before it
// is refreshed from the tray sidecar. Per research.md R5 / design §7.3.
const autostartCacheTTL = 1 * time.Hour

// autostartSidecarName is the filename the tray writes under
// ~/.mcpproxy/ to expose its login-item state. The core reads this file —
// pragmatic substitute for a tray-side HTTP listener, with identical
// semantics (enabled true/false/absent) and identical TTL caching.
const autostartSidecarName = "tray-autostart.json"

// autostartSidecar is the on-disk schema of tray-autostart.json. The tray is
// the writer; the core is a read-only consumer.
type autostartSidecar struct {
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// AutostartReader reads the tray-written autostart state. Instances are
// safe for concurrent use and cache the result for autostartCacheTTL. A
// nil return value means "state unknown" — this is expected on Linux (no
// tray today), when the tray is not running, or when the tray sidecar is
// absent / malformed.
type AutostartReader struct {
	// Path is the filesystem location of the tray-written sidecar. Tests
	// override this; production uses DefaultAutostartReader.
	Path string

	// now is an injectable clock for deterministic cache-TTL testing. When
	// nil, time.Now is used.
	now func() time.Time

	mu         sync.Mutex
	cached     *bool
	cachedAt   time.Time
	cachedOnce bool
}

// DefaultAutostartReader returns the production reader targeting
// ~/.mcpproxy/tray-autostart.json. On Linux the sidecar is never written
// (by design), so the returned reader will simply always yield nil — the
// heartbeat's AutostartEnabled field stays JSON-null, matching data-model.md.
func DefaultAutostartReader() *AutostartReader {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		// Fall back to a non-existent path — Read will return nil.
		return &AutostartReader{Path: ""}
	}
	return &AutostartReader{
		Path: filepath.Join(home, ".mcpproxy", autostartSidecarName),
	}
}

// Read returns the current autostart state (true=enabled, false=disabled,
// nil=unknown). Result is cached for autostartCacheTTL after the first
// successful read. Errors and missing files are logged nowhere (the caller
// — the telemetry service — already logs pipeline failures).
func (r *AutostartReader) Read() *bool {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	// Serve from cache if within TTL.
	nowFn := r.now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()
	if r.cachedOnce && now.Sub(r.cachedAt) < autostartCacheTTL {
		return copyBoolPtr(r.cached)
	}

	// Linux: tray does not write a sidecar. Skip I/O entirely and cache nil.
	if runtime.GOOS == "linux" {
		r.cached = nil
		r.cachedAt = now
		r.cachedOnce = true
		return nil
	}

	if r.Path == "" {
		// Sentinel: no known sidecar path.
		r.cached = nil
		r.cachedAt = now
		r.cachedOnce = true
		return nil
	}

	data, err := os.ReadFile(r.Path)
	if err != nil {
		// File absent → tray not running (or hasn't written the sidecar yet).
		// Tray-core boot race: if core starts slightly before the tray, the
		// first Read() sees ErrNotExist and would poison the 1h TTL with nil,
		// causing AutostartEnabled to stay null for the whole first hour even
		// after the tray publishes `true`. Do NOT mark cachedOnce so the next
		// Read() re-probes. Gemini P1 cross-review.
		if errors.Is(err, fs.ErrNotExist) {
			r.cached = nil
			return nil
		}
		// Other errors (permissions, etc.): same short-retry behaviour — do
		// not mark cachedOnce so the next heartbeat tries again.
		r.cached = nil
		return nil
	}

	var sc autostartSidecar
	if err := json.Unmarshal(data, &sc); err != nil {
		// Malformed payload → unknown. Cache nil with full TTL so we don't
		// re-read a garbage file every heartbeat.
		r.cached = nil
		r.cachedAt = now
		r.cachedOnce = true
		return nil
	}

	v := sc.Enabled
	r.cached = &v
	r.cachedAt = now
	r.cachedOnce = true
	return copyBoolPtr(r.cached)
}

// copyBoolPtr defensively returns a pointer to a copy so callers can't
// mutate the reader's cached value.
func copyBoolPtr(p *bool) *bool {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
