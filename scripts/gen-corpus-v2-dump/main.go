// Command gen-corpus-v2-dump exports every indexed tool — with its FULL JSON
// input schema — from a mcpproxy Bleve index directory, as the raw material of
// the spec-083 schema-bearing frozen corpus (corpus_v2.tools.json).
//
// It is invoked by scripts/gen-corpus-v2.sh AFTER the snapshot proxy has been
// booted (to populate the index) and shut down (to release the index lock).
//
// Why read the index instead of GET /api/v1/tools: the REST endpoint serves
// tool schemas from the supervisor StateView, which currently stores a stub
// ({"type":"object","properties":{}} — internal/runtime/supervisor/supervisor.go
// has a literal "TODO: Parse ParamsJSON"), so every schema it returns is empty.
// The Bleve index is the authoritative store of what the production retrieval
// funnel actually ingests (ToolMetadata.ParamsJSON, set from the upstream
// tools/list response in internal/upstream/core) — which is exactly the text
// arm comparison must measure (research D4/D7).
//
// Output (stdout): a JSON array of {tool_id, server, tool, description,
// schema} sorted by tool_id. Canonical key ordering / final envelope assembly
// is done by the calling script (jq -S).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/index"
)

type corpusTool struct {
	ToolID      string          `json:"tool_id"`
	Server      string          `json:"server"`
	Tool        string          `json:"tool"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

func main() {
	dataDir := flag.String("data-dir", "", "mcpproxy data directory containing index.bleve/ (required)")
	flag.Parse()
	if *dataDir == "" {
		fmt.Fprintln(os.Stderr, "usage: gen-corpus-v2-dump -data-dir <dir>")
		os.Exit(2)
	}

	if err := run(*dataDir); err != nil {
		fmt.Fprintf(os.Stderr, "gen-corpus-v2-dump: %v\n", err)
		os.Exit(1)
	}
}

func run(dataDir string) error {
	mgr, err := index.NewManager(dataDir, zap.NewNop())
	if err != nil {
		return fmt.Errorf("open index under %q: %w", dataDir, err)
	}
	defer mgr.Close()

	servers, err := mgr.GetAllIndexedServerNames()
	if err != nil {
		return fmt.Errorf("list indexed servers: %w", err)
	}
	if len(servers) == 0 {
		return fmt.Errorf("index under %q contains no servers — was the snapshot proxy booted against this data dir?", dataDir)
	}
	sort.Strings(servers)

	var out []corpusTool
	for _, server := range servers {
		tools, terr := mgr.GetToolsByServer(server)
		if terr != nil {
			return fmt.Errorf("tools for server %q: %w", server, terr)
		}
		for _, t := range tools {
			// The index stores the full "server:tool" name; normalize to the
			// bare tool name for the corpus shape (matches corpus_v1).
			name := t.Name
			if idx := strings.Index(name, ":"); idx != -1 {
				name = name[idx+1:]
			}
			if t.ParamsJSON == "" || !json.Valid([]byte(t.ParamsJSON)) {
				return fmt.Errorf("tool %s:%s has empty/invalid params_json — corpus_v2 must be schema-bearing", server, name)
			}
			out = append(out, corpusTool{
				ToolID:      server + ":" + name,
				Server:      server,
				Tool:        name,
				Description: t.Description,
				Schema:      json.RawMessage(t.ParamsJSON),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ToolID < out[j].ToolID })

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(out)
}
