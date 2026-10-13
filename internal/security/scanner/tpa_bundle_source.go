package scanner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Bundle source labels reported by BundleInfo.Source.
const (
	// BundleSourceEmbedded is the corpus compiled into this build
	// (bundled/scanner-bundle.json).
	BundleSourceEmbedded = "embedded"
	// BundleSourceFile is a corpus read from the configured filesystem path
	// (security.tpa_bundle_path / MCPPROXY_TPA_BUNDLE_PATH).
	BundleSourceFile = "file"
)

// BundleInfo is the operator-facing status of the TPA signature corpus the
// scanner is actually running (spec 086 FR-019, GH #938 finding 2). Before this
// existed there was no supported way to answer "which signatures is my proxy
// running, and how old are they?" — the bundle was embed-only and its single
// load report went to an unconfigured global logger.
type BundleInfo struct {
	// Source is BundleSourceEmbedded or BundleSourceFile.
	Source string `json:"source"`
	// Path is the configured filesystem path, when Source is "file".
	Path string `json:"path,omitempty"`
	// BundleVersion / SchemaVersion are the corpus's own declared versions.
	BundleVersion string `json:"bundle_version,omitempty"`
	SchemaVersion string `json:"schema_version,omitempty"`
	// SignatureCount is the bundle's self-declared signature count.
	SignatureCount int `json:"signature_count"`
	// RunnableRules is how many rules are LIVE in the offline tier; SkippedRules
	// and DeclaredSkipped are the not-runnable / bundle-declared-skipped
	// remainder (FR-006/FR-007) — un-evaluated coverage, never clean coverage.
	RunnableRules   int `json:"runnable_rules"`
	SkippedRules    int `json:"skipped_rules"`
	DeclaredSkipped int `json:"declared_skipped"`
	// GeneratedAt is the corpus freshness stamp. It is the bundle's own
	// `generated_at` key when present; for a file-sourced bundle without one it
	// falls back to the file's modification time, so a years-stale corpus is
	// distinguishable from a fresh export.
	GeneratedAt string `json:"generated_at,omitempty"`
	// Fingerprint is the first 12 hex chars of the SHA-256 of the corpus bytes:
	// a stable identity an operator (or automation) can compare across hosts
	// without shipping the whole file around.
	Fingerprint string `json:"fingerprint,omitempty"`
	// LoadedAt is when this corpus became active in this process.
	LoadedAt time.Time `json:"loaded_at"`
	// LoadError is the reason the LAST configured-path load failed, if any. The
	// previously active (or embedded) corpus stays live — a bundle problem must
	// never leave the scanner with no signatures — but the failure is surfaced
	// here rather than swallowed.
	LoadError string `json:"load_error,omitempty"`
}

// activeBundle is the process-wide corpus in use. It is replaced wholesale by
// ConfigureBundle; readers take a snapshot under RLock so a hot-reload can never
// hand a scan a half-swapped corpus.
var (
	activeBundleMu    sync.RWMutex
	activeBundleCheck *BundleCheck
	activeBundleInfo  BundleInfo
)

// ConfigureBundle installs the TPA signature corpus the scanner will run,
// reading it from path when non-empty and falling back to the embedded default
// otherwise (spec 086 FR-019: the path MUST NOT be hardcoded). It is safe to
// call repeatedly — the wiring layer calls it at startup and again on every
// config hot-reload, which is what makes the path re-readable at runtime.
//
// Failure policy (fail-closed, never fail-empty): if the configured file cannot
// be read, parsed, version-checked, or compiled, the currently active corpus
// (or the embedded default on first call) stays live and the reason is recorded
// in BundleInfo.LoadError AND logged through the INJECTED logger. The previous
// implementation logged through the unconfigured global zap.L(), so its one
// status report reached no console and no log file (GH #938 finding 2).
func ConfigureBundle(path string, logger *zap.Logger) {
	if logger == nil {
		logger = zap.NewNop()
	}
	log := logger.Named("security.scanner")

	if path == "" {
		check, info, err := loadEmbeddedBundle()
		if err != nil {
			log.Warn("embedded TPA scanner bundle failed to load; continuing without bundle-backed checks",
				zap.Error(err))
			storeBundle(nil, BundleInfo{Source: BundleSourceEmbedded, LoadedAt: time.Now(), LoadError: err.Error()})
			return
		}
		storeBundle(check, info)
		logBundle(log, info)
		return
	}

	check, info, err := loadBundleFromFile(path)
	if err != nil {
		// Keep the last-known-good corpus (or install the embedded default if
		// this is the first configuration) and make the failure visible.
		prevCheck, prevInfo := snapshotBundle()
		if prevCheck == nil {
			if embeddedCheck, embeddedInfo, embErr := loadEmbeddedBundle(); embErr == nil {
				prevCheck, prevInfo = embeddedCheck, embeddedInfo
			}
		}
		prevInfo.LoadError = err.Error()
		storeBundle(prevCheck, prevInfo)
		log.Error("configured TPA scanner bundle failed to load; keeping the previously active corpus",
			zap.String("path", path),
			zap.String("active_source", prevInfo.Source),
			zap.Int("runnable_rules", prevInfo.RunnableRules),
			zap.Error(err))
		return
	}
	storeBundle(check, info)
	logBundle(log, info)
}

// BundleStatus returns a snapshot of the active corpus for the operator-facing
// surfaces (REST /api/v1/security/overview, `mcpproxy security overview`).
// It lazily installs the embedded default so a caller that never configured a
// bundle still gets a truthful answer instead of a zero value.
func BundleStatus() BundleInfo {
	if check, info := snapshotBundle(); check != nil || info.LoadError != "" {
		return info
	}
	ConfigureBundle("", zap.NewNop())
	_, info := snapshotBundle()
	return info
}

// logBundle emits the one status report an operator can grep for.
func logBundle(log *zap.Logger, info BundleInfo) {
	log.Info("loaded TPA scanner bundle",
		zap.String("source", info.Source),
		zap.String("path", info.Path),
		zap.String("bundle_version", info.BundleVersion),
		zap.String("generated_at", info.GeneratedAt),
		zap.String("fingerprint", info.Fingerprint),
		zap.Int("signature_count", info.SignatureCount),
		zap.Int("runnable_rules", info.RunnableRules),
		zap.Int("skipped_rules", info.SkippedRules),
		zap.Int("declared_skipped", info.DeclaredSkipped))
}

func storeBundle(check *BundleCheck, info BundleInfo) {
	activeBundleMu.Lock()
	activeBundleCheck = check
	activeBundleInfo = info
	activeBundleMu.Unlock()
}

func snapshotBundle() (*BundleCheck, BundleInfo) {
	activeBundleMu.RLock()
	defer activeBundleMu.RUnlock()
	return activeBundleCheck, activeBundleInfo
}

// loadEmbeddedBundle compiles the corpus shipped with this build.
func loadEmbeddedBundle() (*BundleCheck, BundleInfo, error) {
	check, info, err := loadBundleWithInfo(bundledScannerBundle)
	if err != nil {
		return nil, BundleInfo{}, err
	}
	info.Source = BundleSourceEmbedded
	return check, info, nil
}

// loadBundleFromFile reads and compiles a corpus from the configured path. A
// missing file is an error (not a silent fallback) so a mistyped path is
// reported rather than looking like a working configuration.
func loadBundleFromFile(path string) (*BundleCheck, BundleInfo, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-configured path, same trust level as mcp_config.json
	if err != nil {
		return nil, BundleInfo{}, fmt.Errorf("read scanner bundle %s: %w", path, err)
	}
	check, info, err := loadBundleWithInfo(data)
	if err != nil {
		return nil, BundleInfo{}, fmt.Errorf("scanner bundle %s: %w", path, err)
	}
	info.Source = BundleSourceFile
	info.Path = path
	if info.GeneratedAt == "" {
		// The v0.1.0 bundle format carries no generated_at; the file's mtime is
		// the next-best freshness signal and is strictly better than nothing.
		if st, statErr := os.Stat(path); statErr == nil {
			info.GeneratedAt = st.ModTime().UTC().Format(time.RFC3339)
		}
	}
	return check, info, nil
}

// loadBundleWithInfo compiles raw bundle bytes and derives the operator-facing
// metadata alongside the check.
func loadBundleWithInfo(data []byte) (*BundleCheck, BundleInfo, error) {
	check, stats, err := loadBundleCheck(data)
	if err != nil {
		return nil, BundleInfo{}, err
	}
	meta := bundleMetadata(data)
	sum := sha256.Sum256(data)
	return check, BundleInfo{
		BundleVersion:   meta.BundleVersion,
		SchemaVersion:   meta.SchemaVersion,
		SignatureCount:  meta.SignatureCount,
		GeneratedAt:     meta.GeneratedAt,
		RunnableRules:   stats.Runnable,
		SkippedRules:    stats.Skipped,
		DeclaredSkipped: stats.Declared,
		Fingerprint:     hex.EncodeToString(sum[:])[:12],
		LoadedAt:        time.Now(),
	}, nil
}
