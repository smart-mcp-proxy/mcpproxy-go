// mcptools.go — the live MCP tools/list primitive (Spec 103).
//
// # What this is for, and what it is NOT for
//
// The obvious justification for reading tools/list is "REST serves stub
// schemas". On this branch that is FALSE and must not be repeated: GET
// /api/v1/tools returns real upstream input schemas (MCP-3167 is fixed in
// internal/contracts/converters.go, which now reads "inputSchema" first and
// falls back to the legacy "schema"). Measured against the 7-server reference
// fleet, 42 of 45 REST schemas are real and the 3 empties are genuinely
// parameter-less tools serving identical bytes on both surfaces.
//
// The real justification is FIDELITY. tools/list is the only surface that
// shows what an agent actually receives:
//
//   - it is the MENU, not a catalog. REST /api/v1/tools is a different
//     population (it skips quarantined servers but includes disabled servers
//     and non-callable tools) and a different rendering (no "[server] "
//     prefix, no deferred signature suffix).
//   - it reflects the deployment. bench's in-process derivation
//     (internal/server.ProxyModeToolDefs) pins EnableCodeExecution:true
//     unconditionally, so it always counts code_execution's 2420-char
//     description even for a deployment with code execution off — the shipped
//     default — overstating the proxy menu by 1049 tokens.
//   - it is verifiably the same arithmetic. A captured tools/list run through
//     bench's own tokenizer reproduces the in-process built-in menu EXACTLY
//     (5783 tokens for the retrieve_tools surface, 3781 for code_execution).
//
// # The stub guard is the point of this file
//
// A fleet of stub schemas prices every upstream definition at a fraction of
// its real size. That shrinks the naive baseline, which is the denominator of
// every published savings percentage, so the error lands as INFLATED SAVINGS —
// the one direction of error this benchmark exists to prevent. Two systematic
// sources are known:
//
//  1. The Spec 102 deferred direct surface. With
//     `direct_tool_response_mode: "deferred"`, /mcp/all serves the placeholder
//     `{"type":"object"}` for EVERY upstream tool (internal/server
//     deferredDirectInputSchema / renderDeferredDirectTool) and folds the real
//     schema into the description as a compact signature. It is served with no
//     error and no flag, and REST keeps serving real schemas under the very
//     same config — so this is the live hazard, and it is silent.
//
//     Measured end to end on the 7-server / 45-tool reference fleet, with this
//     guard removed: direct_full's menu collapses from 4368 to 2502 tokens
//     (-42.7%), and the published headline — the direct_full <-> direct_deferred
//     delta — FLIPS SIGN, from +1300 tokens (29.8% saved) to -806 (-32.2%,
//     i.e. deferral reported as COSTING more). That is the failure this file
//     exists to make impossible: not a slightly-off number, but a report that
//     asserts the opposite of the truth while looking entirely well-formed.
//
//  2. The supervisor StateView stub `{"type":"object","properties":{}}`
//     (MCP-3132/MCP-3167). Fixed today; the guard is what keeps a regression
//     from silently re-inflating the headline.
//
// The guard is stated per POPULATION rather than per tool, because a
// per-tool rule would be useless: real fleets contain genuinely parameter-less
// tools whose honest schema is `{"properties":{},"required":[],"type":"object"}`,
// and the deferred surface leaves describe_tool's schema real, so "does
// anything here have a schema" waves the deferred case straight through. The
// rule is instead: within each population PRESENT in the listing — upstream
// tools and mcpproxy's own built-ins — at least one tool must carry a schema
// with real properties. Both known failure modes stub an entire population;
// no healthy fleet stubs one.
//
// # Why the RAW wire bytes
//
// The schema must come off the wire as json.RawMessage, never through
// mcp-go's typed decode. ToolArgumentsSchema keeps only
// {$defs, type, properties, required, additionalProperties} and drops every
// other top-level keyword, and its marshaller ALWAYS emits "properties" and
// "required" — so a round trip rewrites the deferred placeholder
// `{"type":"object"}` as `{"properties":{},"required":[],"type":"object"}` and
// erases the exact distinction the guard depends on. The transport-level path
// is therefore not an optimization; it is what makes the guard possible.
//
// # The "[server] " prefix is stripped
//
// The direct surface renders every description as `fmt.Sprintf("[%s] %s", ...)`
// (internal/server/mcp_routing.go). Isolated on the reference fleet, that
// prefix costs 136 tokens across 45 tools — 3.0% (4595 with, 4459 without).
// It is stripped here, and the reason is not that 3% is small: the encoding
// arms SYNTHESIZE each cell's rendering from a canonical definition, and
// bench/arms.DirectDeferredArm prepends that same "[server] " itself. Keeping
// the prefix would double-charge it in the direct cells and make a live fleet
// non-interchangeable with every frozen corpus the published figures use.
//
// # Known limitation: server attribution is a first-"__" split
//
// Attribution uses the production parser (server.ParseDirectToolName), which
// splits on the FIRST "__" and is knowingly ambiguous: a server whose name
// contains "__" mis-splits. The proxy resolves this with server-side state
// (mcp_direct_catalog.go keeps byDisplayName AND byCanonical maps) that is not
// recoverable over the wire, and it WITHHOLDS both tools on a display-name
// collision rather than picking a winner — so a live listing can also under-
// count relative to GET /api/v1/tools. Reconciling a fleet's count against the
// REST catalog is the fix for that, and is deliberately left to the caller
// rather than smuggled in here.
package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/server"
)

// MCPToolsListMethod is the JSON-RPC method this primitive calls.
const MCPToolsListMethod = "tools/list"

// deferredDirectPlaceholderSchema mirrors internal/server's
// deferredDirectInputSchema byte for byte. Matching the exact bytes is what
// lets the guard say "this is Spec 102 deferral" rather than the vaguer "these
// schemas look empty" — and an actionable error names the config to change.
const deferredDirectPlaceholderSchema = `{"type":"object"}`

// maxToolsListPages bounds the pagination loop. A server that returns a
// cursor forever would otherwise hang a benchmark run with no output; the cap
// is far above any real fleet (a page is typically the whole catalog).
const maxToolsListPages = 512

// ErrStubToolSchemas is the guard's sentinel: a tools/list listing whose
// schemas cannot be priced. It is a sentinel rather than a formatted string so
// a caller (and a test) can assert the RULE — a stub-schema listing is refused,
// never returned — independently of the wording.
var ErrStubToolSchemas = errors.New("mcp tools/list served stub input schemas")

// ErrReplayFleetAmbiguous is the two-fleets refusal. Silently preferring one
// input over the other would make the report's fleet_id a lie: every figure is
// quoted for exactly one fleet shape, and the reader has no way to tell which.
var ErrReplayFleetAmbiguous = errors.New("replay takes exactly one fleet input")

// ToolsListPageFunc performs ONE tools/list round trip and returns the RAW
// JSON-RPC result object for that page (the `{"tools":[...],"nextCursor":...}`
// value, not the envelope).
//
// It is a function type for the reason RetrieveToolsFunc is (flipgate.go): the
// arithmetic that matters — identity mapping, prefix stripping, canonical-
// ization and above all the stub guard — must be testable with no proxy
// running, and a concrete client in the signature would pin every test to one.
// The raw json.RawMessage return is load-bearing; see the file header.
type ToolsListPageFunc func(ctx context.Context, cursor string) (json.RawMessage, error)

// toolsListPage is the decoded shape of one tools/list result. The schema
// stays raw all the way through.
type toolsListPage struct {
	Tools      []wireTool `json:"tools"`
	NextCursor string     `json:"nextCursor"`
}

// wireTool is one tools/list entry, decoded only as far as the fields that
// occupy an agent's context. Annotations, _meta, outputSchema and icons are
// deliberately not decoded: this primitive feeds bench.Tool, which models a
// definition as name + description + input schema, and inventing fields the
// arms cannot render would price a fiction.
type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// FetchToolsList performs a full, paginated MCP tools/list against one surface
// and maps the result onto []Tool with REAL schemas, or fails.
//
// surface is used only for diagnostics (e.g. "/mcp/all"); the transport is
// supplied by page. The returned order is the wire order, page by page, so two
// runs against an unchanged proxy produce identical fleets.
func FetchToolsList(ctx context.Context, surface string, page ToolsListPageFunc) ([]Tool, error) {
	if page == nil {
		return nil, fmt.Errorf("mcp tools/list %s: no transport supplied", surface)
	}

	var wire []wireTool
	cursor := ""
	seenCursor := map[string]bool{}
	for i := 0; ; i++ {
		if i >= maxToolsListPages {
			return nil, fmt.Errorf("mcp tools/list %s: stopped after %d pages — the server keeps returning a nextCursor", surface, maxToolsListPages)
		}
		raw, err := page(ctx, cursor)
		if err != nil {
			return nil, fmt.Errorf("mcp tools/list %s: %w", surface, err)
		}
		var decoded toolsListPage
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, fmt.Errorf("mcp tools/list %s: decode page %d: %w", surface, i, err)
		}
		wire = append(wire, decoded.Tools...)
		if decoded.NextCursor == "" {
			break
		}
		// A repeated cursor is a server bug that would otherwise duplicate the
		// whole catalog into the fleet and double its menu cost.
		if seenCursor[decoded.NextCursor] {
			return nil, fmt.Errorf("mcp tools/list %s: server repeated cursor %q — pagination would loop", surface, decoded.NextCursor)
		}
		seenCursor[decoded.NextCursor] = true
		cursor = decoded.NextCursor
	}

	if len(wire) == 0 {
		return nil, fmt.Errorf("mcp tools/list %s: the listing contains no tools — a zero-tool menu would be reported as a measurement of zero cost", surface)
	}

	tools := make([]Tool, 0, len(wire))
	seenID := make(map[string]bool, len(wire))
	for i := range wire {
		tl, err := toolFromWire(wire[i])
		if err != nil {
			return nil, fmt.Errorf("mcp tools/list %s: entry %d (%s): %w", surface, i, wire[i].Name, err)
		}
		// The direct catalog withholds BOTH tools on a display-name collision
		// rather than picking a winner, so a duplicate arriving here means
		// something upstream is wrong. Counting it twice would inflate the menu.
		if seenID[tl.ToolID] {
			return nil, fmt.Errorf("mcp tools/list %s: duplicate tool id %q — counting it twice would inflate the menu cost", surface, tl.ToolID)
		}
		seenID[tl.ToolID] = true
		tools = append(tools, tl)
	}

	if err := guardToolSchemas(surface, tools); err != nil {
		return nil, err
	}
	return tools, nil
}

// toolFromWire maps one tools/list entry onto a canonical bench.Tool.
//
// Identity follows the conventions the rest of bench already uses, so a live
// fleet joins by id against a frozen corpus and an activity export without a
// translation layer: an upstream tool is "<server>:<tool>" (matching
// LiveClient.FetchUpstreamTools and corpus_v2), and a tool with no "__"
// separator is one of mcpproxy's own, keyed "mcpproxy:<name>" exactly as
// ProxyToolsForMode keys it. A prefix-less name is NEVER given a server of ""
// dressed up as attribution.
func toolFromWire(w wireTool) (Tool, error) {
	schema, err := normalizeSchema(w.InputSchema)
	if err != nil {
		return Tool{}, err
	}

	serverName, toolName, ok := server.ParseDirectToolName(w.Name)
	if !ok {
		return Tool{
			ToolID:      "mcpproxy:" + w.Name,
			Name:        w.Name,
			Description: w.Description,
			Schema:      schema,
		}, nil
	}
	return Tool{
		ToolID:      serverName + ":" + toolName,
		Server:      serverName,
		Name:        toolName,
		Description: stripDirectServerPrefix(w.Description, serverName),
		Schema:      schema,
	}, nil
}

// stripDirectServerPrefix removes the "[server] " the direct surface prepends,
// and ONLY when it names this tool's own server — a description that merely
// opens with some other bracketed word is real text the agent pays for. See
// the file header for why stripping is the right call (the arms re-synthesize
// the prefix themselves) and for the 136-token measurement of what it costs.
// KNOWN CONSEQUENCE of the "__" mis-split documented in the file header: when a
// server name itself contains "__" (wire name "my__server__read"), the parsed
// server is "my", so this looks for "[my] " and leaves "[my__server] " in the
// description. The arms then re-synthesize their own prefix on top, and that
// tool is charged for the prefix TWICE.
//
// It is left rather than patched because a heuristic that guessed where the
// server name ends would mis-split a different set of names, and the cost is a
// few tokens on one tool rather than a wrong headline. The file header
// documents the mis-split's effect on attribution and counting; this is its
// effect on cost, recorded so nobody rediscovers it as a mystery discrepancy.
func stripDirectServerPrefix(description, serverName string) string {
	prefix := "[" + serverName + "] "
	return strings.TrimPrefix(description, prefix)
}

// schemaCensus counts one population's schemas by kind.
type schemaCensus struct {
	tools         int
	real          int
	placeholder   int // exactly the Spec 102 deferred placeholder bytes
	parameterless int // an explicit, honest "takes no arguments"
	example       string
}

func (c *schemaCensus) add(t Tool) {
	c.tools++
	switch {
	case len(t.Schema) > 0 && !isStubSchema(t.Schema):
		c.real++
	default:
		switch {
		case string(t.Schema) == deferredDirectPlaceholderSchema:
			c.placeholder++
		case isParameterlessSchema(t.Schema):
			// An honest parameter-less tool. list_allowed_directories,
			// read_graph and list_tables all legitimately take no arguments,
			// and they serve the SAME bytes on REST and MCP. Counting them as
			// evidence of stubbing would reject a small real fleet with a
			// misleading cause.
			c.parameterless++
		}
		if c.example == "" {
			c.example = fmt.Sprintf("%s -> %q", t.ToolID, string(t.Schema))
		}
	}
}

// isParameterlessSchema reports whether raw is an explicit "this tool takes no
// arguments" declaration rather than a placeholder standing in for one.
//
// The distinction is the difference between refusing a real fleet and admitting
// a stubbed one. A parameter-less tool declares its emptiness — an explicit
// properties object, usually alongside an empty required list. The Spec 102
// deferred placeholder and the old supervisor stub declare nothing at all.
func isParameterlessSchema(raw json.RawMessage) bool {
	var probe struct {
		Type       string                      `json:"type"`
		Properties *map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.Type == "object" && probe.Properties != nil && len(*probe.Properties) == 0
}

// guardToolSchemas refuses a listing whose schemas cannot be priced.
//
// See the file header for the full rationale. In short: a stub-schema fleet
// shrinks the savings denominator and therefore INFLATES the headline, so it
// must fail loudly rather than flow into a report. The check is per population
// because a per-tool rule would refuse every real fleet (parameter-less tools
// exist) and a whole-listing rule would pass the deferred case (describe_tool
// keeps a real schema next to 45 placeholders).
func guardToolSchemas(surface string, tools []Tool) error {
	var upstream, builtin schemaCensus
	for _, t := range tools {
		if t.Server == "" {
			builtin.add(t)
			continue
		}
		upstream.add(t)
	}

	for _, pop := range []struct {
		label  string
		census schemaCensus
	}{
		{"upstream", upstream},
		{"built-in", builtin},
	} {
		c := pop.census
		// An ABSENT population is not an empty one: /mcp lists no upstream
		// tools at all, and that is a fact about the surface, not a defect.
		if c.tools == 0 || c.real > 0 {
			continue
		}
		// AN EMPTY-PROPERTIES SCHEMA IS AMBIGUOUS BY SHAPE, and the error must
		// say so rather than blaming a bug that may not be present.
		// `{"type":"object","properties":{}}` is what a stubbed surface serves
		// AND what an honestly parameter-less tool serves —
		// filesystem:list_allowed_directories and memory:read_graph emit
		// exactly those bytes on both REST and MCP. No inspection separates
		// them.
		//
		// The population is still REFUSED, because the two failure directions
		// are not symmetric: admitting a stubbed fleet prices its schemas at
		// nothing and INFLATES the headline, which is the error class this
		// whole benchmark exists to prevent, while refusing an honest
		// parameter-less fleet costs its operator one clear message and a
		// documented way forward. So refuse, and name the ambiguity.
		//
		// The Spec 102 deferred placeholder is NOT ambiguous — it carries no
		// properties key at all — and gets its own, definite cause below.
		cause := "every schema declares no properties. That shape is AMBIGUOUS: it is what a stubbed " +
			"surface serves and equally what an honestly parameter-less tool serves, and nothing in the " +
			"schema separates them. This is refused rather than admitted because the two directions are " +
			"not symmetric — pricing stubbed schemas at nothing inflates the headline, while refusing a " +
			"genuinely parameter-less fleet costs you this message. If your fleet really is all " +
			"parameter-less, supply it as a frozen corpus (-corpus-v2) where the shape is a deliberate, " +
			"reviewable input rather than something read off a live surface"
		if c.placeholder > 0 {
			cause = fmt.Sprintf("%d of them are exactly the Spec 102 placeholder %s, which means the proxy is running with "+
				"direct_tool_response_mode: \"deferred\" — that surface folds the real schema into the description, so a fleet read "+
				"from it under-counts the direct menu by ~40%% and FLIPS THE SIGN of the direct_full <-> direct_deferred delta "+
				"(measured on a 45-tool fleet: +1300 tokens / 29.8%% becomes -806 / -32.2%%). Set direct_tool_response_mode to \"full\" "+
				"and re-run", c.placeholder, deferredDirectPlaceholderSchema)
		}
		return fmt.Errorf("%w: %s carried %d %s tools and not one real input schema (%s; first: %s). "+
			"Pricing them would shrink the naive baseline and INFLATE the reported savings, so this listing is refused rather than returned",
			ErrStubToolSchemas, surface, c.tools, pop.label, cause, c.example)
	}
	return nil
}

// FleetFromToolsList fetches a listing and wraps its UPSTREAM tools as a
// Corpus fit to be a replay fleet.
//
// The built-ins are dropped, and that is a modelling decision worth stating: a
// fleet is the upstream catalog. Replay prices mcpproxy's own tools per cell
// from ProxyToolsForMode, the frozen corpora contain upstream tools only, and
// folding describe_tool into the fleet would both double-count it in the
// retrieve cells and move the direct cells off the basis every frozen-corpus
// figure is quoted on. (replay.go already records the reverse gap: the direct
// menu omits describe_tool, which biases the direct delta percentage slightly
// upward. Keeping the fleet upstream-only keeps that ONE stated gap rather
// than adding a second, opposite one that depends on the fleet's source.)
func FleetFromToolsList(ctx context.Context, surface, fleetID string, page ToolsListPageFunc) (*Corpus, error) {
	tools, err := FetchToolsList(ctx, surface, page)
	if err != nil {
		return nil, err
	}
	upstream := make([]Tool, 0, len(tools))
	for _, t := range tools {
		if t.Server == "" {
			continue
		}
		upstream = append(upstream, t)
	}
	// A surface with no upstream tools is not a small fleet — it is the wrong
	// surface. Under the retrieve_tools and code_execution routing modes
	// tools/list returns mcpproxy's built-ins and nothing else, and reporting
	// those as a fleet would price the proxy's own menu as the upstream catalog.
	if len(upstream) == 0 {
		return nil, fmt.Errorf("mcp tools/list %s: the listing carried %d tools but none of them is an upstream tool — "+
			"only the direct surface (%s) enumerates the fleet; the retrieve_tools and code_execution surfaces list mcpproxy's own built-ins",
			surface, len(tools), EndpointDirect)
	}
	return &Corpus{Version: fleetID, Tools: upstream}, nil
}

// FleetEndpointURL builds the URL a live fleet is read from.
//
// The path comes from the direct_full mode cell rather than a literal, so the
// surface a fleet is captured from stays tied to the one cell that actually
// enumerates upstream tools; if the matrix ever remounts that surface, this
// follows it. The API key rides the ?apikey= query parameter, the same surface
// mcpEndpointURL uses, and never appears in any reported field.
func FleetEndpointURL(baseURL, apiKey string) (string, error) {
	cell, ok := CellByID(CellDirectFull)
	if !ok {
		return "", fmt.Errorf("mode cell %q is not in the matrix, so no fleet endpoint can be derived", CellDirectFull)
	}
	endpoint, err := cell.EndpointURL(baseURL)
	if err != nil {
		return "", err
	}
	if apiKey != "" {
		endpoint += "?apikey=" + url.QueryEscape(apiKey)
	}
	return endpoint, nil
}

// FleetSource supplies a replay fleet on demand.
//
// It is an interface for the reason EncodingArm is one (armrun.go): the seam
// must be satisfiable by a value a test writes inline, not only by a live
// client. It also keeps the "exactly one fleet input" rule (ResolveFleet) in
// package bench, where the fleet-is-mandatory rule already lives, instead of
// splitting the two halves of one rule across bench and the CLI.
type FleetSource interface {
	// FleetID names the fleet shape every figure is quoted for (IC-004). It
	// must never carry a credential: it is reported.
	FleetID() string
	// Fleet returns the tool definitions the menu cost is computed from.
	Fleet() (*Corpus, error)
}

// LiveFleetSource reads a fleet from a running proxy's direct MCP surface.
//
// It holds the run's context rather than taking one per call, so FleetSource
// stays a two-method seam a test can satisfy with a literal. The value is a
// one-shot adapter constructed at the call site for a single run, which is the
// case where a stored context is honest rather than a leak.
type LiveFleetSource struct {
	ctx      context.Context
	baseURL  string
	apiKey   string
	endpoint string
	fleetID  string
}

// NewLiveFleetSource builds a fleet source against a running proxy's base URL
// (e.g. "http://127.0.0.1:8080"). Endpoint resolution failures are deferred to
// Fleet(), so a mis-typed URL fails where every other fleet failure does.
func NewLiveFleetSource(ctx context.Context, baseURL, apiKey string) *LiveFleetSource {
	// The fleet id names the SURFACE, never the key: it is written into the
	// report.
	surface := EndpointDirect
	if cell, ok := CellByID(CellDirectFull); ok && cell.Endpoint != "" {
		surface = cell.Endpoint
	}
	return &LiveFleetSource{
		ctx:     ctx,
		baseURL: baseURL,
		apiKey:  apiKey,
		fleetID: "live:" + strings.TrimRight(baseURL, "/") + surface,
	}
}

// FleetID implements FleetSource.
func (s *LiveFleetSource) FleetID() string { return s.fleetID }

// Fleet implements FleetSource: one MCP session, one paginated tools/list, and
// the stub guard. A failure here is fatal to the run by design — falling back
// to a frozen corpus would answer a different question than the one asked.
func (s *LiveFleetSource) Fleet() (*Corpus, error) {
	endpoint := s.endpoint
	if endpoint == "" {
		resolved, err := FleetEndpointURL(s.baseURL, s.apiKey)
		if err != nil {
			return nil, err
		}
		endpoint = resolved
	}

	caller, err := NewMCPRetrieveCaller(s.ctx, endpoint)
	if err != nil {
		return nil, err
	}
	defer caller.Close()

	return FleetFromToolsList(s.ctx, EndpointDirect, s.fleetID, caller.ToolsListPage)
}
