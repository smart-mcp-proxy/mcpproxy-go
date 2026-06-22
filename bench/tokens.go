// Package bench is the mcpproxy benchmark harness (roadmap #19 / MCP-42).
//
// It produces the reproducible numbers behind mcpproxy's marketing claims —
// token reduction, discovery accuracy, and latency — by comparing three ways
// an agent can be wired to upstream MCP tools:
//
//   - baseline: every upstream tool definition is loaded directly into the
//     agent's context (no proxy discovery).
//   - retrieve_tools: only mcpproxy's discovery + call_tool variants occupy the
//     context; tools are found on demand via BM25 search.
//   - code_execution: only code_execution + retrieve_tools occupy the context;
//     the agent orchestrates many tools from sandboxed JS in one round-trip.
//
// The token-reduction measurement in this file is fully deterministic and
// offline: it counts the context cost of each mode over a frozen tool corpus
// using the tiktoken cl100k_base encoding (a reproducible, model-agnostic
// estimator). It reuses the Spec 065 frozen corpus
// (specs/065-evaluation-foundation/datasets/corpus_v1.tools.json) as its tool
// universe so the benchmark scores a versioned, non-drifting snapshot (CN-002).
//
// Methodology, limitations, and the live (docker-compose) run that adds full
// JSON input schemas and end-to-end accuracy/latency are documented in
// bench/README.md.
package bench

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/pkoukk/tiktoken-go"
)

// DefaultEncoding is the tiktoken encoding used for token estimation. cl100k_base
// is a widely-used, reproducible BPE; exact counts for a specific pinned model
// (e.g. Claude) will differ, but the *relative* savings between modes are stable.
const DefaultEncoding = "cl100k_base"

// Routing modes the benchmark compares. The mode names mirror the mcpproxy
// MCP servers in internal/server/mcp.go (codeExecServer, callToolServer).
const (
	ModeBaseline      = "baseline"
	ModeRetrieveTools = "retrieve_tools"
	ModeCodeExecution = "code_execution"
)

// Tool is a single tool definition the benchmark scores token cost over. It
// matches the shape of both the Spec 065 corpus snapshot and the embedded
// proxy-tool fixture.
type Tool struct {
	ToolID      string `json:"tool_id"`
	Server      string `json:"server"`
	Name        string `json:"tool"`
	Description string `json:"description"`
}

// Corpus is a frozen, versioned set of tool definitions.
type Corpus struct {
	Version string `json:"version"`
	Tools   []Tool `json:"tools"`
}

// LoadCorpus reads a frozen corpus snapshot (e.g. the Spec 065
// corpus_v1.tools.json) from disk.
func LoadCorpus(path string) (*Corpus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read corpus %q: %w", path, err)
	}
	var c Corpus
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse corpus %q: %w", path, err)
	}
	if len(c.Tools) == 0 {
		return nil, fmt.Errorf("corpus %q contains no tools", path)
	}
	return &c, nil
}

// Tokenizer wraps a tiktoken encoding for reproducible token estimation.
type Tokenizer struct {
	enc      *tiktoken.Tiktoken
	encoding string
}

// NewTokenizer constructs a Tokenizer for the given tiktoken encoding name.
func NewTokenizer(encoding string) (*Tokenizer, error) {
	enc, err := tiktoken.GetEncoding(encoding)
	if err != nil {
		return nil, fmt.Errorf("load tiktoken encoding %q: %w", encoding, err)
	}
	return &Tokenizer{enc: enc, encoding: encoding}, nil
}

// Count returns the number of tokens in text.
func (t *Tokenizer) Count(text string) int {
	return len(t.enc.Encode(text, nil, nil))
}

// CountTool returns the context-token cost of a single tool definition.
//
// It counts the tool name and description only. Input JSON schemas are excluded
// uniformly across every mode because the committed Spec 065 corpus snapshot
// does not carry schemas. This is deliberately conservative for the headline
// claim: upstream tools carry far larger schemas than mcpproxy's handful of
// proxy tools, so excluding schemas *understates* the baseline and therefore
// understates the measured savings. The live docker-compose run (README.md)
// adds full schemas from GET /api/v1/tools for the exact headline number.
func (t *Tokenizer) CountTool(tl Tool) int {
	return t.Count(tl.Name + "\n" + tl.Description)
}

func (t *Tokenizer) countTools(tools []Tool) int {
	total := 0
	for _, tl := range tools {
		total += t.CountTool(tl)
	}
	return total
}

// ModeResult is the per-mode context-cost outcome.
type ModeResult struct {
	Mode         string  `json:"mode"`
	ContextTools int     `json:"context_tools"`
	Tokens       int     `json:"tokens"`
	SavingsRatio float64 `json:"savings_vs_baseline"`
}

// Report is the full token-reduction benchmark result.
type Report struct {
	Encoding      string       `json:"encoding"`
	CorpusVersion string       `json:"corpus_version"`
	CorpusTools   int          `json:"corpus_tools"`
	Modes         []ModeResult `json:"modes"`
	Notes         []string     `json:"notes"`
}

// ComputeReport computes the per-mode context-token cost over the corpus and the
// savings of each proxy mode versus the baseline (all tools loaded directly).
func ComputeReport(tk *Tokenizer, corpus *Corpus) *Report {
	baseTokens := tk.countTools(corpus.Tools)

	rtTools := ProxyToolsForMode(ModeRetrieveTools)
	ceTools := ProxyToolsForMode(ModeCodeExecution)

	savings := func(tokens int) float64 {
		if baseTokens == 0 {
			return 0
		}
		return 1.0 - float64(tokens)/float64(baseTokens)
	}

	rtTokens := tk.countTools(rtTools)
	ceTokens := tk.countTools(ceTools)

	return &Report{
		Encoding:      tk.encoding,
		CorpusVersion: corpus.Version,
		CorpusTools:   len(corpus.Tools),
		Modes: []ModeResult{
			{Mode: ModeBaseline, ContextTools: len(corpus.Tools), Tokens: baseTokens, SavingsRatio: 0},
			{Mode: ModeRetrieveTools, ContextTools: len(rtTools), Tokens: rtTokens, SavingsRatio: savings(rtTokens)},
			{Mode: ModeCodeExecution, ContextTools: len(ceTools), Tokens: ceTokens, SavingsRatio: savings(ceTokens)},
		},
		Notes: []string{
			"Token counts use the tiktoken " + tk.encoding + " encoding as a reproducible, model-agnostic estimator; exact counts for a pinned model may differ.",
			"Counts tool name + description only; JSON input schemas are excluded uniformly, which understates the baseline and is therefore conservative for the savings claim.",
			"Corpus is the frozen Spec 065 snapshot (specs/065-evaluation-foundation/datasets/corpus_v1.tools.json); see bench/README.md for the live run with full schemas.",
		},
	}
}
