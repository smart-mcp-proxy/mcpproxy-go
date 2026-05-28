package outputvalidation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.uber.org/zap"
)

// compileCount counts how many times a schema is actually compiled (vs served
// from cache). It is a package-level test hook with negligible production cost;
// only test code reads it.
var compileCount atomic.Int64

// Outcome indicates whether a structured output conforms to its schema.
type Outcome int

const (
	// OutcomePass means the output is valid, there is no schema, or there is
	// nothing to validate. The caller forwards the payload unchanged.
	OutcomePass Outcome = iota

	// OutcomeViolate means the output failed a guard check or schema validation.
	OutcomeViolate
)

// Verdict is the result returned by Validator.Validate.
// It is never persisted; it is purely transient.
type Verdict struct {
	Outcome  Outcome
	Reason   string // human-readable violation detail; empty on pass
	GuardHit string // "" | "max_bytes" | "max_depth"
}

// IsViolation reports whether the verdict represents a violation.
func (v Verdict) IsViolation() bool { return v.Outcome == OutcomeViolate }

// cacheKey uniquely identifies a compiled schema by tool identity and schema content.
type cacheKey struct {
	toolKey    string
	schemaHash uint64
}

// cacheEntry is what gets stored in the sync.Map.
// Either compiled is non-nil (successful compile) or sentinel is true (uncompilable).
type cacheEntry struct {
	compiled *jsonschema.Schema
	sentinel bool // true means "uncompilable; treat as no-op"
}

// Validator validates a tool's structured output against its declared JSON Schema,
// applying byte-size and nesting-depth guards first. Safe for concurrent use.
type Validator struct {
	maxBytes int
	maxDepth int
	cache    sync.Map // key: cacheKey -> *cacheEntry
	logger   *zap.Logger
}

// New creates a new Validator.
//   - maxBytes <= 0 disables the byte-size guard.
//   - maxDepth <= 0 disables the nesting-depth guard.
//   - logger may be nil; if nil, zap.NewNop() is used.
func New(maxBytes, maxDepth int, logger *zap.Logger) *Validator {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Validator{
		maxBytes: maxBytes,
		maxDepth: maxDepth,
		logger:   logger,
	}
}

// Validate checks structured against schemaJSON for the tool identified by toolKey.
//
// Decision tree (in order):
//  1. schemaJSON == ""  → OutcomePass  (FR-A7: no schema declared, no-op)
//  2. structured == nil → OutcomePass  (FR-A8: nothing to validate)
//  3. Guards (byte size, nesting depth) — on breach, return OutcomeViolate immediately
//  4. Schema compilation (cached) — uncompilable schemas log once and return OutcomePass (FR-A9)
//  5. Schema validation — return OutcomeViolate on mismatch, OutcomePass on success
//
// Validate MUST NOT mutate structured.
func (v *Validator) Validate(toolKey, schemaJSON string, structured any) Verdict {
	// Step 1: no schema → no-op
	if schemaJSON == "" {
		return Verdict{Outcome: OutcomePass}
	}

	// Step 2: nothing to validate
	if structured == nil {
		return Verdict{Outcome: OutcomePass}
	}

	// Marshal structured to JSON once; we need the bytes for the byte-size guard
	// and will reuse them as the validation instance.
	jsonBytes, err := json.Marshal(structured)
	if err != nil {
		// If we can't marshal, log and pass (don't block on our own failure).
		v.logger.Warn("outputvalidation: failed to marshal structured output",
			zap.String("tool", toolKey),
			zap.Error(err),
		)
		return Verdict{Outcome: OutcomePass}
	}

	// Step 3a: byte-size guard
	if v.maxBytes > 0 && len(jsonBytes) > v.maxBytes {
		return Verdict{
			Outcome:  OutcomeViolate,
			GuardHit: "max_bytes",
			Reason: fmt.Sprintf("structured output for %q exceeds max_bytes limit (%d > %d bytes)",
				toolKey, len(jsonBytes), v.maxBytes),
		}
	}

	// Step 3b: nesting-depth guard
	if v.maxDepth > 0 {
		depth := nestingDepth(structured)
		if depth > v.maxDepth {
			return Verdict{
				Outcome:  OutcomeViolate,
				GuardHit: "max_depth",
				Reason: fmt.Sprintf("structured output for %q exceeds max_depth limit (%d > %d)",
					toolKey, depth, v.maxDepth),
			}
		}
	}

	// Step 4: look up (or compile) the schema
	entry := v.getOrCompile(toolKey, schemaJSON)
	if entry.sentinel {
		// Uncompilable schema — treat as no-op (FR-A9)
		return Verdict{Outcome: OutcomePass}
	}

	// Step 5: validate the instance
	// Decode the JSON bytes using jsonschema.UnmarshalJSON so numbers use json.Number
	// (required for correct numeric type comparisons in draft 2020-12).
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(jsonBytes))
	if err != nil {
		v.logger.Warn("outputvalidation: failed to unmarshal instance for validation",
			zap.String("tool", toolKey),
			zap.Error(err),
		)
		return Verdict{Outcome: OutcomePass}
	}

	if err := entry.compiled.Validate(instance); err != nil {
		reason := truncate(err.Error(), 500)
		return Verdict{
			Outcome: OutcomeViolate,
			Reason:  reason,
		}
	}

	return Verdict{Outcome: OutcomePass}
}

// getOrCompile returns the cache entry for toolKey + schemaJSON, compiling on first access.
func (v *Validator) getOrCompile(toolKey, schemaJSON string) *cacheEntry {
	key := cacheKey{
		toolKey:    toolKey,
		schemaHash: hashSchema(schemaJSON),
	}

	if val, ok := v.cache.Load(key); ok {
		return val.(*cacheEntry)
	}

	// Not cached — compile. Increment the test hook counter.
	compileCount.Add(1)

	entry := compile(schemaJSON)
	if entry.sentinel {
		v.logger.Warn("outputvalidation: uncompilable output schema; treating tool as no-schema",
			zap.String("tool", toolKey),
		)
	}

	// Store with LoadOrStore to handle concurrent first-callers; we always use
	// whichever entry wins the race.
	actual, _ := v.cache.LoadOrStore(key, entry)
	return actual.(*cacheEntry)
}

// compile attempts to compile schemaJSON and returns a cacheEntry.
// On failure it returns a sentinel entry.
func compile(schemaJSON string) *cacheEntry {
	// Decode the schema document using jsonschema.UnmarshalJSON so that
	// number-valued keywords (e.g. multipleOf) use json.Number.
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(schemaJSON))
	if err != nil {
		return &cacheEntry{sentinel: true}
	}

	// Use an opaque in-memory resource URI rather than a relative name; a
	// relative name resolves against the process cwd, leaking that path into
	// validation error messages (which are surfaced in audit records).
	const schemaURI = "mem://outputschema/schema"
	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaURI, doc); err != nil {
		return &cacheEntry{sentinel: true}
	}

	sch, err := c.Compile(schemaURI)
	if err != nil {
		return &cacheEntry{sentinel: true}
	}

	return &cacheEntry{compiled: sch}
}

// hashSchema returns an FNV-64a hash of the schema bytes for use as a cache key component.
func hashSchema(schemaJSON string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(schemaJSON))
	return h.Sum64()
}

// truncate shortens s to at most maxLen characters, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
