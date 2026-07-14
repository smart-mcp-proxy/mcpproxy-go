package arms

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TSCGName is the registry key of the TSCG-compiled arm.
const TSCGName = "tscg"

// tscgShimRelPath is where the pinned shim lives relative to the repo root
// (research D3: committed shim + package-lock, @tscg/core@1.4.3).
var tscgShimRelPath = filepath.Join("bench", "tscg")

// TSCGArm measures the reference TSCG implementation (@tscg/core) by spawning
// the committed Node shim bench/tscg/shim.mjs once per Encode call and
// exchanging JSONL records keyed by tool_id (research D3). TSCG is a pure
// deterministic compiler with pinned options, so identical input bytes always
// produce identical output bytes (FR-010).
type TSCGArm struct {
	// shimDir is the directory containing shim.mjs + node_modules.
	shimDir string
	// nodePath is the resolved node binary; availErr the construction-time
	// availability verdict (contract rule 5: surfaced at registry resolution,
	// before any tool is processed).
	nodePath string
	availErr error
	// timeout bounds one shim invocation; 0 means the corpus-size-proportional
	// default (tscgBaseTimeout + tscgPerToolTimeout per tool). Tests set a tiny
	// value to exercise the kill path.
	timeout time.Duration
}

// Shim-invocation bounds: a hung node process (bad shim, wedged runtime) must
// error out instead of blocking the whole bench run forever and leaving a
// zombie child.
const (
	// tscgBaseTimeout is the fixed floor of one shim invocation.
	tscgBaseTimeout = 120 * time.Second
	// tscgPerToolTimeout scales the bound with the batch size (a full-corpus
	// EncodeListing compiles every tool in one spawn).
	tscgPerToolTimeout = time.Second
	// tscgWaitDelay bounds Wait after the context fires: if the killed child
	// left pipe readers behind, Wait is forcibly released instead of hanging.
	tscgWaitDelay = 5 * time.Second
)

// NewTSCG constructs the tscg arm, locating bench/tscg by walking up from the
// working directory (tests run in bench/arms; the bench CLI runs from the repo
// root) and resolving the node binary from PATH. Both checks happen here, at
// construction; Available() reports the cached verdict.
func NewTSCG() *TSCGArm {
	return NewTSCGAt(findTSCGShimDir())
}

// NewTSCGAt constructs the tscg arm against an explicit shim directory
// (tests use this to exercise the unavailable path).
func NewTSCGAt(shimDir string) *TSCGArm {
	a := &TSCGArm{shimDir: shimDir}
	a.availErr = a.checkRuntime()
	return a
}

// findTSCGShimDir walks up from the working directory looking for
// bench/tscg/shim.mjs, returning the bench/tscg directory (or "" when not
// found — reported as unavailable, never as a hard failure).
func findTSCGShimDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, tscgShimRelPath)
		if _, statErr := os.Stat(filepath.Join(candidate, "shim.mjs")); statErr == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// checkRuntime verifies the external runtime once: a node binary on PATH,
// the committed shim, and an installed node_modules (npm ci --prefix
// bench/tscg). Any absence wraps ErrArmUnavailable (contract rule 5).
func (a *TSCGArm) checkRuntime() error {
	node, err := exec.LookPath("node")
	if err != nil {
		return fmt.Errorf("%w: node binary not found on PATH (install Node.js; CI provides it per FR-006)", ErrArmUnavailable)
	}
	a.nodePath = node
	if a.shimDir == "" {
		return fmt.Errorf("%w: bench/tscg/shim.mjs not found walking up from the working directory (run from within the repo)", ErrArmUnavailable)
	}
	if _, err := os.Stat(filepath.Join(a.shimDir, "shim.mjs")); err != nil {
		return fmt.Errorf("%w: %s/shim.mjs missing", ErrArmUnavailable, a.shimDir)
	}
	if _, err := os.Stat(filepath.Join(a.shimDir, "node_modules")); err != nil {
		return fmt.Errorf("%w: %s/node_modules missing (run: npm ci --prefix %s)", ErrArmUnavailable, a.shimDir, a.shimDir)
	}
	return nil
}

// Available implements AvailabilityChecker with the construction-time verdict.
func (a *TSCGArm) Available() error { return a.availErr }

// Name implements Arm.
func (*TSCGArm) Name() string { return TSCGName }

// IndexAltering implements Arm: TSCG re-encodes both the description prose and
// the parameter text, so retrieval-quality scoring is obligatory (FR-008).
func (*TSCGArm) IndexAltering() bool { return true }

// LowerBound implements Arm: the balanced profile rewrites descriptions and
// elides filler phrases (measured on corpus_v2: e.g. filesystem:read_text_file
// loses "Use this tool when you need to"), so savings are a lower-bound
// estimate per contract rule 3.
func (*TSCGArm) LowerBound() bool { return true }

// tscgRequest / tscgResponse mirror the shim's JSONL protocol exactly
// (bench/tscg/shim.mjs): {"tool_id","tool"} in, {"tool_id","encoded"} or
// {"tool_id","error"} out, one JSON object per line, responses in input order.
type tscgRequest struct {
	ToolID string   `json:"tool_id"`
	Tool   tscgTool `json:"tool"`
}

type tscgTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type tscgResponse struct {
	ToolID  string `json:"tool_id"`
	Encoded string `json:"encoded,omitempty"`
	Error   string `json:"error,omitempty"`
}

// encodeBatch spawns node ONCE for the whole batch: stream all requests to the
// shim's stdin, close it, read all JSONL responses, wait for exit. Returns the
// encodings keyed by tool_id; the FIRST per-record error aborts the batch
// (contract rule 2: explicit failure, never silent truncation — the harness
// counts skips at the EncodeTool level).
func (a *TSCGArm) encodeBatch(ts []bench.Tool) (map[string]string, error) {
	if a.availErr != nil {
		return nil, a.availErr
	}

	var input bytes.Buffer
	enc := json.NewEncoder(&input)
	enc.SetEscapeHTML(false)
	for _, t := range ts {
		req := tscgRequest{ToolID: t.ToolID, Tool: tscgTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Schema,
		}}
		// json.Encoder validates the RawMessage schema here: invalid schema
		// JSON is a per-tool explicit error before any process is spawned.
		if err := enc.Encode(req); err != nil {
			return nil, fmt.Errorf("tool %s: encode tscg request (invalid input schema?): %w", t.ToolID, err)
		}
	}

	timeout := a.timeout
	if timeout <= 0 {
		timeout = tscgBaseTimeout + time.Duration(len(ts))*tscgPerToolTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// CommandContext kills the child (cmd.Cancel = Kill) when the deadline
	// fires; WaitDelay releases Wait even if pipe copies linger after the
	// kill. Stdin is a fully buffered payload, so exec closes it after the
	// write — no interactive stdin to deadlock on; stdout/stderr drain into
	// buffers via exec's own copier goroutines.
	cmd := exec.CommandContext(ctx, a.nodePath, "shim.mjs") //nolint:gosec // nodePath from LookPath, fixed arg
	cmd.Dir = a.shimDir
	cmd.Stdin = &input
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = tscgWaitDelay
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("tscg shim timed out after %s and was killed: %w (stderr: %s)", timeout, ctx.Err(), strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("tscg shim failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	out := make(map[string]string, len(ts))
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var resp tscgResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			return nil, fmt.Errorf("tscg shim: unparseable response line %q: %w", line, err)
		}
		if resp.Error != "" {
			return nil, fmt.Errorf("tool %s: tscg encoding failed: %s", resp.ToolID, resp.Error)
		}
		if resp.Encoded == "" {
			return nil, fmt.Errorf("tool %s: tscg shim returned empty encoding", resp.ToolID)
		}
		out[resp.ToolID] = resp.Encoded
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read tscg shim output: %w", err)
	}
	for _, t := range ts {
		if _, ok := out[t.ToolID]; !ok {
			return nil, fmt.Errorf("tool %s: tscg shim returned no record", t.ToolID)
		}
	}
	return out, nil
}

// EncodeTool implements Arm: a batch of one through the shim.
func (a *TSCGArm) EncodeTool(t bench.Tool) (string, error) {
	out, err := a.encodeBatch([]bench.Tool{t})
	if err != nil {
		return "", err
	}
	return out[t.ToolID], nil
}

// EncodeListing implements Arm: one node spawn for the whole listing, per-tool
// compilations joined by the shared separator (TSCG has no listing-level
// preamble or dictionary to amortize — contract rule 6).
func (a *TSCGArm) EncodeListing(ts []bench.Tool) (string, error) {
	out, err := a.encodeBatch(ts)
	if err != nil {
		return "", err
	}
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = out[t.ToolID]
	}
	return strings.Join(parts, listingSeparator), nil
}

// EncodeIndexMetadata implements Arm (FR-008). The compiled text has a fixed
// three-part shape (verified against @tscg/core@1.4.3 output on all of
// corpus_v2):
//
//	<name>: <rewritten description>
//	  <param>[*] (<type>): <desc> | <param2> ...   ← absent for parameterless tools
//	[CLOSURE:<name>(<required params>)]
//
// Mapping decision: Name stays the tool name (TSCG never re-encodes it, and
// the "<name>: " header prefix would duplicate the Name field the index
// already ingests); Description gets the TSCG-rewritten prose (the compiled
// header may span multiple lines — e.g. sequential-thinking — so the split is
// structural, not line-count-based); ParamsJSON gets the compiled parameter
// representation — the trailing run of two-space-indented parameter lines plus
// the CLOSURE signature line — replacing the JSON schema, mirroring how the
// compiled text itself represents params. The three fields reconstruct the
// compiled encoding exactly (Name + ": " + Description + "\n" + ParamsJSON),
// so the index ingests precisely what this arm renders: nothing more, nothing
// less.
func (a *TSCGArm) EncodeIndexMetadata(t bench.Tool) (config.ToolMetadata, error) {
	encoded, err := a.EncodeTool(t)
	if err != nil {
		return config.ToolMetadata{}, err
	}

	prefix := t.Name + ": "
	if !strings.HasPrefix(encoded, prefix) {
		return config.ToolMetadata{}, fmt.Errorf("tool %s: tscg output missing %q header: %q", t.ToolID, prefix, encoded)
	}
	closureIdx := strings.LastIndex(encoded, "\n[CLOSURE:")
	if closureIdx < 0 || !strings.HasSuffix(encoded, "]") {
		return config.ToolMetadata{}, fmt.Errorf("tool %s: tscg output missing CLOSURE line: %q", t.ToolID, encoded)
	}
	closure := encoded[closureIdx+1:] // "[CLOSURE:...]"
	body := encoded[len(prefix):closureIdx]

	// Parameter block = the trailing run of two-space-indented lines directly
	// above the CLOSURE line; everything before it is the rewritten
	// description (which may itself be multi-line).
	lines := strings.Split(body, "\n")
	paramStart := len(lines)
	for paramStart > 0 && strings.HasPrefix(lines[paramStart-1], "  ") {
		paramStart--
	}
	description := strings.Join(lines[:paramStart], "\n")
	params := strings.Join(lines[paramStart:], "\n")

	paramsJSON := closure
	if params != "" {
		paramsJSON = params + "\n" + closure
	}
	return config.ToolMetadata{
		Name:        t.Name,
		ServerName:  t.Server,
		Description: description,
		ParamsJSON:  paramsJSON,
	}, nil
}

func init() {
	MustRegister(NewTSCG())
}
