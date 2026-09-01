package bench

// mcptools_test.go — Spec 103: the live MCP tools/list primitive.
//
// These tests hold six boundaries. Each one, crossed silently, produces a
// report whose menu cost is wrong in the direction that FLATTERS mcpproxy —
// which is the single error class this benchmark exists to prevent:
//
//  1. A listing whose input schemas are stubs is REFUSED, not returned. A
//     stub-schema fleet prices every upstream definition at a fraction of its
//     real size, so the naive baseline shrinks and the proxy's savings inflate.
//     The Spec 102 deferred placeholder is the live instance of this hazard.
//  2. A tool that genuinely takes no parameters is NOT a stub-schema listing.
//     Refusing those would make the guard useless on real fleets, so the rule
//     is per-population ("nothing real anywhere"), not per-tool.
//  3. Server attribution is recovered from the "<server>__<tool>" display name
//     via the production parser, and a name that carries no prefix is a
//     built-in — never a server called "".
//  4. The "[server] " description prefix the direct surface prepends is
//     stripped, because the arms re-synthesize each cell's rendering from the
//     canonical definition and direct_deferred prepends that prefix itself.
//  5. A replay takes exactly ONE fleet input. Two is an error, because
//     silently preferring one makes the report's fleet_id a lie.
//  6. A fleet input stays MANDATORY. This primitive adds a second way to
//     supply one; it does not make one optional.
//
// The transport is behind a ToolsListPageFunc seam (the bench/flipgate.go
// precedent), so every one of these runs with no proxy anywhere.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// wireEntry is a hand-built tools/list entry. It is assembled as raw JSON
// rather than through mcp.Tool on purpose: mcp-go's ToolInputSchema marshaller
// ALWAYS emits "properties" and "required", so round-tripping the Spec 102
// placeholder {"type":"object"} through it would rewrite it as
// {"properties":{},"required":[],"type":"object"} and erase the very
// distinction these tests assert on.
type wireEntry struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema string `json:"-"`
}

// staticPager returns a ToolsListPageFunc that serves the given entries as a
// single unpaginated page.
func staticPager(entries ...wireEntry) ToolsListPageFunc {
	return pagedPager([][]wireEntry{entries}...)
}

// pagedPager serves one page per slice, chaining them with nextCursor so the
// pagination loop is exercised for real.
func pagedPager(pages ...[]wireEntry) ToolsListPageFunc {
	byCursor := map[string]int{"": 0}
	for i := range pages {
		if i+1 < len(pages) {
			byCursor[fmt.Sprintf("cursor-%d", i+1)] = i + 1
		}
	}
	return func(_ context.Context, cursor string) (json.RawMessage, error) {
		idx, ok := byCursor[cursor]
		if !ok {
			return nil, fmt.Errorf("unknown cursor %q", cursor)
		}
		var parts []string
		for _, e := range pages[idx] {
			schema := e.InputSchema
			if schema == "" {
				schema = "null"
			}
			parts = append(parts, fmt.Sprintf(`{"name":%q,"description":%q,"inputSchema":%s}`,
				e.Name, e.Description, schema))
		}
		next := ""
		if idx+1 < len(pages) {
			next = fmt.Sprintf("cursor-%d", idx+1)
		}
		return json.RawMessage(fmt.Sprintf(`{"tools":[%s],"nextCursor":%q}`,
			strings.Join(parts, ","), next)), nil
	}
}

const realSchema = `{"type":"object","properties":{"repo_path":{"type":"string"}},"required":["repo_path"]}`

// paramlessSchema is what a genuinely parameter-less upstream tool serves —
// filesystem:list_allowed_directories, memory:read_graph and sqlite:list_tables
// all serve exactly these bytes on BOTH the REST and MCP surfaces.
const paramlessSchema = `{"properties":{},"required":[],"type":"object"}`

// healthyDirectListing is the shape a full-mode /mcp/all listing has: real
// upstream schemas, "[server] " description prefixes, plus the describe_tool
// built-in the direct surface always registers alongside them.
func healthyDirectListing() ToolsListPageFunc {
	return staticPager(
		wireEntry{Name: "git__git_log", Description: "[git] Shows the commit logs", InputSchema: realSchema},
		wireEntry{Name: "sqlite__list_tables", Description: "[sqlite] List tables", InputSchema: paramlessSchema},
		wireEntry{Name: "describe_tool", Description: "Return full schemas", InputSchema: realSchema},
	)
}

// --- boundary 3 + 4: identity mapping -------------------------------------

func TestFetchToolsList_MapsUpstreamAndBuiltinIdentity(t *testing.T) {
	tools, err := FetchToolsList(context.Background(), "/mcp/all", healthyDirectListing())
	if err != nil {
		t.Fatalf("healthy listing must be accepted: %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("want 3 tools, got %d", len(tools))
	}

	up := tools[0]
	if up.Server != "git" || up.Name != "git_log" {
		t.Errorf("server attribution: want (git, git_log), got (%q, %q)", up.Server, up.Name)
	}
	if up.ToolID != "git:git_log" {
		t.Errorf("upstream tool id must be the canonical server:tool, got %q", up.ToolID)
	}
	// Boundary 4: the wire prefix is stripped, so the fleet carries the same
	// canonical description a frozen corpus or GET /api/v1/tools does.
	if up.Description != "Shows the commit logs" {
		t.Errorf("the [server] prefix must be stripped, got %q", up.Description)
	}

	builtin := tools[2]
	if builtin.Server != "" {
		t.Errorf("a name with no %q separator is a built-in, not a server called %q", "__", builtin.Server)
	}
	if builtin.ToolID != "mcpproxy:describe_tool" {
		t.Errorf("built-in id must match ProxyToolsForMode's convention, got %q", builtin.ToolID)
	}
	if builtin.Description != "Return full schemas" {
		t.Errorf("a built-in description carries no prefix to strip, got %q", builtin.Description)
	}
}

// The prefix is stripped only when it names THIS tool's server. A description
// that merely happens to open with a bracket keeps every byte.
func TestFetchToolsList_StripsOnlyItsOwnServerPrefix(t *testing.T) {
	tools, err := FetchToolsList(context.Background(), "/mcp/all", staticPager(
		wireEntry{Name: "git__git_log", Description: "[notes] see [git] elsewhere", InputSchema: realSchema},
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tools[0].Description != "[notes] see [git] elsewhere" {
		t.Errorf("a foreign bracket must survive verbatim, got %q", tools[0].Description)
	}
}

// Schemas are canonicalized at the ingestion boundary, exactly as
// LiveClient.FetchUpstreamTools does, so the bytes the tokenizer counts match
// what the arm renderers produce.
func TestFetchToolsList_CanonicalizesSchemasAtTheBoundary(t *testing.T) {
	tools, err := FetchToolsList(context.Background(), "/mcp/all", staticPager(
		wireEntry{Name: "git__git_log", Description: "d", InputSchema: `{ "required" : ["b"] , "type":"object", "properties":{"b":{"type":"string"}} }`},
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `{"properties":{"b":{"type":"string"}},"required":["b"],"type":"object"}`
	if string(tools[0].Schema) != want {
		t.Errorf("schema must be canonical:\n want %s\n got  %s", want, tools[0].Schema)
	}
}

// --- boundary 1: the stub guard -------------------------------------------

// The Spec 102 deferred direct surface serves {"type":"object"} for EVERY
// upstream tool while describe_tool keeps a real schema. A guard that only
// asked "does anything here have a schema" would wave this through and
// under-count the direct menu by ~39%.
func TestFetchToolsList_RefusesDeferredPlaceholderSchemas(t *testing.T) {
	_, err := FetchToolsList(context.Background(), "/mcp/all", staticPager(
		wireEntry{Name: "git__git_log", Description: "[git] Shows the commit logs\ngit_log(repo_path*:str)", InputSchema: `{"type":"object"}`},
		wireEntry{Name: "sqlite__list_tables", Description: "[sqlite] List tables\nlist_tables()", InputSchema: `{"type":"object"}`},
		wireEntry{Name: "describe_tool", Description: "Return full schemas", InputSchema: realSchema},
	))
	if err == nil {
		t.Fatal("a schema-deferred listing must be REFUSED: every upstream definition would be priced at a fraction of its real size")
	}
	if !errors.Is(err, ErrStubToolSchemas) {
		t.Fatalf("want ErrStubToolSchemas, got %v", err)
	}
	// The error must be actionable: name the surface, and name the config that
	// produced the placeholder.
	for _, want := range []string{"/mcp/all", "direct_tool_response_mode", "deferred", "upstream"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("stub error must mention %q; got: %v", want, err)
		}
	}
}

// The supervisor StateView stub (MCP-3167) is the other systematic source. It
// is fixed on the REST side today, but the guard is the thing that keeps a
// regression from silently re-inflating the headline.
func TestFetchToolsList_RefusesSupervisorStubSchemas(t *testing.T) {
	_, err := FetchToolsList(context.Background(), "/mcp/all", staticPager(
		wireEntry{Name: "git__git_log", Description: "[git] Shows the commit logs", InputSchema: `{"type":"object","properties":{}}`},
		wireEntry{Name: "fs__read_file", Description: "[fs] Read a file", InputSchema: `{"type":"object","properties":{}}`},
	))
	if !errors.Is(err, ErrStubToolSchemas) {
		t.Fatalf("want ErrStubToolSchemas for an all-stub upstream population, got %v", err)
	}
}

// A listing that carries no schemas at all is the same failure with the field
// missing rather than emptied.
func TestFetchToolsList_RefusesSchemalessListing(t *testing.T) {
	_, err := FetchToolsList(context.Background(), "/mcp/all", staticPager(
		wireEntry{Name: "git__git_log", Description: "[git] Shows the commit logs"},
		wireEntry{Name: "fs__read_file", Description: "[fs] Read a file"},
	))
	if !errors.Is(err, ErrStubToolSchemas) {
		t.Fatalf("want ErrStubToolSchemas for a schema-less listing, got %v", err)
	}
}

// The built-in population is guarded independently, so a stubbed /mcp listing
// is refused even though no upstream tool appears on that surface at all.
func TestFetchToolsList_GuardsTheBuiltinPopulationSeparately(t *testing.T) {
	_, err := FetchToolsList(context.Background(), "/mcp", staticPager(
		wireEntry{Name: "retrieve_tools", Description: "Search", InputSchema: `{"type":"object","properties":{}}`},
		wireEntry{Name: "list_registries", Description: "List", InputSchema: paramlessSchema},
	))
	if !errors.Is(err, ErrStubToolSchemas) {
		t.Fatalf("want ErrStubToolSchemas for an all-stub built-in listing, got %v", err)
	}
	if !strings.Contains(err.Error(), "built-in") {
		t.Errorf("the error must name the population it found empty; got: %v", err)
	}
}

// --- boundary 2: parameter-less tools are not stubs ------------------------

func TestFetchToolsList_ParameterlessToolsAreNotAStubListing(t *testing.T) {
	tools, err := FetchToolsList(context.Background(), "/mcp/all", healthyDirectListing())
	if err != nil {
		t.Fatalf("3 real + 1 parameter-less must be accepted, got: %v", err)
	}
	// The parameter-less schema is preserved rather than dropped: those bytes
	// are on the wire and the agent pays for them.
	if len(tools[1].Schema) == 0 {
		t.Error("a parameter-less tool's schema is real wire bytes and must be kept")
	}
}

// A /mcp listing of healthy built-ins passes with no upstream population at
// all — an absent population is not an empty one.
func TestFetchToolsList_BuiltinOnlyListingIsAccepted(t *testing.T) {
	tools, err := FetchToolsList(context.Background(), "/mcp", staticPager(
		wireEntry{Name: "retrieve_tools", Description: "Search", InputSchema: realSchema},
		wireEntry{Name: "list_registries", Description: "List", InputSchema: paramlessSchema},
	))
	if err != nil {
		t.Fatalf("a built-ins-only surface must be accepted: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("want 2 built-ins, got %d", len(tools))
	}
}

// --- listing hygiene -------------------------------------------------------

func TestFetchToolsList_FollowsPagination(t *testing.T) {
	tools, err := FetchToolsList(context.Background(), "/mcp/all", pagedPager(
		[]wireEntry{{Name: "git__git_log", Description: "[git] a", InputSchema: realSchema}},
		[]wireEntry{{Name: "fs__read_file", Description: "[fs] b", InputSchema: realSchema}},
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("nextCursor must be followed; want 2 tools, got %d", len(tools))
	}
	if tools[0].ToolID != "git:git_log" || tools[1].ToolID != "fs:read_file" {
		t.Errorf("page order must be preserved, got %q then %q", tools[0].ToolID, tools[1].ToolID)
	}
}

// A duplicate display name would be counted twice in the menu. The direct
// catalog withholds colliding names server-side, so a duplicate reaching bench
// means something upstream of it is wrong — refuse rather than double-count.
func TestFetchToolsList_RefusesDuplicateToolIDs(t *testing.T) {
	_, err := FetchToolsList(context.Background(), "/mcp/all", staticPager(
		wireEntry{Name: "git__git_log", Description: "[git] a", InputSchema: realSchema},
		wireEntry{Name: "git__git_log", Description: "[git] a", InputSchema: realSchema},
	))
	if err == nil || !strings.Contains(err.Error(), "git:git_log") {
		t.Fatalf("a duplicate tool id must be refused and named, got %v", err)
	}
}

func TestFetchToolsList_RefusesEmptyListing(t *testing.T) {
	if _, err := FetchToolsList(context.Background(), "/mcp/all", staticPager()); err == nil {
		t.Fatal("an empty listing must be an error: a zero-tool fleet would price a zero menu as a measurement")
	}
}

func TestFetchToolsList_PropagatesTransportFailure(t *testing.T) {
	boom := errors.New("connection refused")
	_, err := FetchToolsList(context.Background(), "/mcp/all",
		func(context.Context, string) (json.RawMessage, error) { return nil, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("transport failure must propagate, got %v", err)
	}
}

// --- fleet construction ----------------------------------------------------

// A FLEET is the upstream catalog. mcpproxy's own built-ins are priced per
// cell from ProxyToolsForMode, so folding them into the fleet would count them
// twice in the retrieve cells and move the direct cells off the basis every
// frozen-corpus figure is quoted on.
func TestFleetFromToolsList_DropsBuiltins(t *testing.T) {
	corpus, err := FleetFromToolsList(context.Background(), "/mcp/all", "live:test", healthyDirectListing())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(corpus.Tools) != 2 {
		t.Fatalf("describe_tool must not enter the fleet; want 2 upstream tools, got %d", len(corpus.Tools))
	}
	for _, tl := range corpus.Tools {
		if tl.Server == "" {
			t.Errorf("a fleet tool must carry a server, got %+v", tl)
		}
	}
	if corpus.Version != "live:test" {
		t.Errorf("fleet version must be the fleet id, got %q", corpus.Version)
	}
}

// Pointing the fleet fetch at a retrieve_tools or code_execution surface
// returns only built-ins. That is not a small fleet — it is the wrong surface,
// and reporting it as a fleet would price mcpproxy's own menu as the upstream
// catalog.
func TestFleetFromToolsList_RefusesASurfaceWithNoUpstreamTools(t *testing.T) {
	_, err := FleetFromToolsList(context.Background(), "/mcp", "live:test", staticPager(
		wireEntry{Name: "retrieve_tools", Description: "Search", InputSchema: realSchema},
	))
	if err == nil {
		t.Fatal("a built-ins-only surface is not a fleet and must be refused")
	}
	if !strings.Contains(err.Error(), EndpointDirect) {
		t.Errorf("the error must point at the surface that does enumerate the fleet (%s); got: %v", EndpointDirect, err)
	}
}

// --- boundaries 5 + 6: one fleet, and never zero ---------------------------

// fakeFleetSource is a structural FleetSource, the way replay_test.go's arms
// are structural EncodingArms.
type fakeFleetSource struct {
	id     string
	corpus *Corpus
	err    error
}

func (f *fakeFleetSource) FleetID() string         { return f.id }
func (f *fakeFleetSource) Fleet() (*Corpus, error) { return f.corpus, f.err }

func liveSource() *fakeFleetSource {
	return &fakeFleetSource{
		id: "live:127.0.0.1:18421/mcp/all",
		corpus: &Corpus{Version: "live:127.0.0.1:18421/mcp/all", Tools: []Tool{
			{ToolID: "git:git_log", Server: "git", Name: "git_log", Description: "d", Schema: json.RawMessage(realSchema)},
		}},
	}
}

// Boundary 5 — two fleets is ambiguous. Preferring one silently would make the
// report's fleet_id a lie about which definitions every figure was priced over.
func TestResolveFleet_TwoFleetInputsIsAnError(t *testing.T) {
	_, _, err := ResolveFleet(runnerCorpus(), "corpus_test@1", liveSource())
	if err == nil {
		t.Fatal("a frozen corpus AND a live proxy must be refused")
	}
	if !errors.Is(err, ErrReplayFleetAmbiguous) {
		t.Fatalf("want ErrReplayFleetAmbiguous, got %v", err)
	}
}

// Boundary 6 — the live source ADDS a way to supply a fleet; it does not make
// one optional.
func TestResolveFleet_NoFleetInputIsStillRequired(t *testing.T) {
	if _, _, err := ResolveFleet(nil, "", nil); !errors.Is(err, ErrReplayFleetRequired) {
		t.Fatalf("want ErrReplayFleetRequired, got %v", err)
	}
}

func TestResolveFleet_LiveSourceSuppliesFleetAndID(t *testing.T) {
	fleet, id, err := ResolveFleet(nil, "", liveSource())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fleet.Tools) != 1 || fleet.Tools[0].ToolID != "git:git_log" {
		t.Fatalf("live fleet not passed through: %+v", fleet)
	}
	if id != "live:127.0.0.1:18421/mcp/all" {
		t.Errorf("fleet id must name the live surface, got %q", id)
	}
}

// A live fetch that fails must fail the run. Falling back to a frozen corpus
// would answer a different question than the one that was asked.
func TestResolveFleet_LiveFetchFailureIsFatal(t *testing.T) {
	boom := errors.New("connection refused")
	if _, _, err := ResolveFleet(nil, "", &fakeFleetSource{id: "live:x", err: boom}); !errors.Is(err, boom) {
		t.Fatalf("a failed live fleet fetch must propagate, got %v", err)
	}
}

// An explicit frozen corpus keeps its own id, unchanged by this feature.
func TestResolveFleet_FrozenCorpusUnchanged(t *testing.T) {
	fleet, id, err := ResolveFleet(runnerCorpus(), "corpus_test@1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fleet == nil || id != "corpus_test@1" {
		t.Fatalf("frozen path regressed: fleet=%v id=%q", fleet, id)
	}
}

// --- endpoint construction -------------------------------------------------

// The fleet endpoint is derived from the direct_full mode cell rather than a
// hardcoded path, so the surface a fleet is read from stays tied to the one
// cell that enumerates it.
func TestFleetEndpointURL_UsesTheDirectCellAndHidesTheKey(t *testing.T) {
	got, err := FleetEndpointURL("http://127.0.0.1:18421/", "s3cret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, "http://127.0.0.1:18421"+EndpointDirect) {
		t.Errorf("fleet endpoint must be the direct surface, got %q", got)
	}

	// The key belongs on the wire, never in a report field.
	src := NewLiveFleetSource(context.Background(), "http://127.0.0.1:18421", "s3cret")
	if strings.Contains(src.FleetID(), "s3cret") {
		t.Errorf("the fleet id must never carry the API key, got %q", src.FleetID())
	}
	if !strings.Contains(src.FleetID(), EndpointDirect) {
		t.Errorf("the fleet id must name the surface it was read from, got %q", src.FleetID())
	}
}

// D6 — the API key must actually reach the URL.
//
// The previous coverage asserted only that the key is ABSENT from FleetID and
// that the path has the direct-cell prefix, so replacing the whole
// key-appending branch with `if false` left the suite green. An unauthenticated
// fetch against a proxy that requires a key fails at connect time rather than
// silently, but the assertion should hold the behaviour, not the accident.
func TestFleetEndpointURL_CarriesTheAPIKey(t *testing.T) {
	got, err := FleetEndpointURL("http://127.0.0.1:18421", "s3cr3t key/with+chars")
	if err != nil {
		t.Fatalf("FleetEndpointURL: %v", err)
	}
	if !strings.Contains(got, "apikey=") {
		t.Fatalf("the key must reach the query string, got %q", got)
	}
	if strings.Contains(got, "s3cr3t key/with+chars") {
		t.Errorf("the key must be URL-escaped, not interpolated raw: %q", got)
	}
	if !strings.Contains(got, url.QueryEscape("s3cr3t key/with+chars")) {
		t.Errorf("the escaped key must appear verbatim: %q", got)
	}

	// And no key means no query string at all — an empty apikey= would be a
	// different request from an unauthenticated one.
	bare, err := FleetEndpointURL("http://127.0.0.1:18421", "")
	if err != nil {
		t.Fatalf("FleetEndpointURL(bare): %v", err)
	}
	if strings.Contains(bare, "apikey") {
		t.Errorf("an empty key must not produce an apikey parameter, got %q", bare)
	}
}

// D7 — the repeated-cursor guard must be exercised.
//
// Disabling it left the suite green, yet a server that repeats a cursor would
// duplicate the whole catalog into the fleet and double its menu cost — an
// error that shows up as an implausibly large baseline rather than as a crash.
func TestFetchToolsList_RefusesARepeatedCursor(t *testing.T) {
	calls := 0
	looping := func(_ context.Context, _ string) (json.RawMessage, error) {
		calls++
		// Always hands back the SAME nextCursor.
		return json.RawMessage(`{"tools":[{"name":"git__git_log","description":"d",` +
			`"inputSchema":{"type":"object","properties":{"repo_path":{"type":"string"}}}}],` +
			`"nextCursor":"same-cursor"}`), nil
	}

	_, err := FetchToolsList(context.Background(), "/mcp/all", looping)
	if err == nil {
		t.Fatal("a repeated cursor must be refused — pagination would loop and duplicate the catalog")
	}
	if !strings.Contains(err.Error(), "repeated cursor") {
		t.Errorf("the error must name the cause; got %v", err)
	}
	if calls > 3 {
		t.Errorf("the guard should trip on the second sighting, not after %d pages", calls)
	}
}

// D7 — the nil-transport guard must be exercised too.
func TestFetchToolsList_NilTransportIsAnError(t *testing.T) {
	if _, err := FetchToolsList(context.Background(), "/mcp/all", nil); err == nil {
		t.Error("a nil page function must be an error, not a nil fleet")
	}
}
