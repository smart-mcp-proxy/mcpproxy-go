// Package arms implements the encoding arms of the discovery-effectiveness
// profiler (spec 083). An arm is one deterministic way of rendering tool
// definitions into agent-context text (full JSON baseline, compact signature,
// TSCG, TOON, ...). Arms are compared on frozen corpora for token cost and —
// when they alter what the retrieval index ingests — for retrieval quality.
//
// The behavioral contract every arm must satisfy lives in
// specs/083-discovery-profiler/contracts/arm-interface.md. In short:
// byte-deterministic output (FR-010), explicit errors instead of silent
// truncation (FR-009), self-reported lower-bound labeling when descriptions
// are dropped, an explicit index-ingestion mapping (FR-008), and
// ErrArmUnavailable at registry-resolution time when an external runtime is
// missing (FR-006).
package arms

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"sync"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// ErrArmUnavailable is returned (wrapped, with a human-readable reason) when an
// arm's external runtime is missing — e.g. the TSCG arm without a node binary
// or bench/tscg/node_modules. It is surfaced at registry-resolution time,
// before any tool is processed, so the harness reports an arm-level
// skip-with-reason instead of per-tool failures (contract rule 5).
var ErrArmUnavailable = errors.New("arm runtime unavailable")

// Arm is one deterministic tool-definition encoding under measurement.
type Arm interface {
	// Name is the unique registry key (lowercase snake_case, stable across
	// releases): baseline_json, compact_sig, tscg, toon_listing, toon_results,
	// tron_dedup.
	Name() string

	// IndexAltering reports whether the arm changes any text the retrieval
	// index ingests. True obligates retrieval-quality scoring (FR-008); the
	// two-sided contract test (T022) verifies the declaration against
	// EncodeIndexMetadata diffs on corpus_v2.
	IndexAltering() bool

	// LowerBound reports whether the arm drops or truncates descriptions, so
	// its savings are rendered as a lower-bound estimate (contract rule 3).
	LowerBound() bool

	// EncodeTool renders one tool definition. Byte-deterministic; an
	// unencodable tool returns an error (counted as a skip), never a silently
	// truncated encoding.
	EncodeTool(t bench.Tool) (string, error)

	// EncodeListing renders a whole-response tool listing. Formats with a
	// shared preamble or dictionary (TRON classes, TOON header) amortize it
	// here, not per-tool (contract rule 6).
	EncodeListing(ts []bench.Tool) (string, error)

	// EncodeIndexMetadata returns the exact Name/ServerName/Description/
	// ParamsJSON the production index (internal/index.Manager.BatchIndexTools →
	// BleveIndex.IndexTool) ingests for this arm. It is the single mapping the
	// armindex builder and the IndexAltering contract test consume. Rendering-
	// only arms return the tool's fields unchanged.
	EncodeIndexMetadata(t bench.Tool) (config.ToolMetadata, error)
}

// AvailabilityChecker is an optional interface for arms with external
// runtimes. Registry.Resolve calls Available() and propagates its error, which
// must wrap ErrArmUnavailable when the runtime is missing.
type AvailabilityChecker interface {
	Available() error
}

// armNameRe pins registry names to lowercase snake_case (report consumers key
// on them; see contracts/arm-interface.md registry contract).
var armNameRe = regexp.MustCompile(`^[a-z0-9_]+$`)

// Registry holds the registered encoding arms.
type Registry struct {
	mu   sync.Mutex
	arms map[string]Arm
}

// NewRegistry returns an empty arm registry.
func NewRegistry() *Registry {
	return &Registry{arms: make(map[string]Arm)}
}

// Register adds an arm; duplicate or non-snake_case names are rejected.
func (r *Registry) Register(a Arm) error {
	name := a.Name()
	if !armNameRe.MatchString(name) {
		return fmt.Errorf("arm name %q is invalid: must match %s (lowercase snake_case)", name, armNameRe)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.arms[name]; dup {
		return fmt.Errorf("arm %q already registered", name)
	}
	r.arms[name] = a
	return nil
}

// Resolve returns the named arm, checking runtime availability first: an arm
// implementing AvailabilityChecker whose Available() fails is reported
// unavailable here — before any tool is processed — so the harness can record
// an arm-level skip-with-reason (contract rule 5).
func (r *Registry) Resolve(name string) (Arm, error) {
	r.mu.Lock()
	a, ok := r.arms[name]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("arm %q is not registered (known arms: %v)", name, r.Names())
	}
	if c, checks := a.(AvailabilityChecker); checks {
		if err := c.Available(); err != nil {
			return nil, fmt.Errorf("arm %q: %w", name, err)
		}
	}
	return a, nil
}

// Names returns the registered arm names in sorted (deterministic) order.
func (r *Registry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.arms))
	for n := range r.arms {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// defaultRegistry is the package-level registry each arm file joins in init().
var defaultRegistry = NewRegistry()

// Register adds an arm to the package default registry.
func Register(a Arm) error { return defaultRegistry.Register(a) }

// MustRegister adds an arm to the package default registry, panicking on a
// registration bug (duplicate/invalid name) — a programmer error, caught by
// any test importing the package.
func MustRegister(a Arm) {
	if err := Register(a); err != nil {
		panic(err)
	}
}

// Resolve resolves an arm from the package default registry.
func Resolve(name string) (Arm, error) { return defaultRegistry.Resolve(name) }

// Names lists the package default registry's arms in sorted order.
func Names() []string { return defaultRegistry.Names() }
