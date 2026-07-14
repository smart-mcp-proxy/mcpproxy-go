// Command bench runs the mcpproxy benchmark.
//
// Legacy offline mode (no profiler flags) scores the committed Spec 065
// frozen corpus for token reduction and writes the v1 JSON report plus a
// static HTML dashboard:
//
//	go run ./bench/cmd/bench [-corpus PATH] [-out DIR] [-encoding NAME]
//
// Profiler mode (Spec 083) measures deterministic encoding arms on frozen
// corpora and writes the v2 report + dashboard:
//
//	go run ./bench/cmd/bench -corpus-v2 specs/083-discovery-profiler/datasets/corpus_v2.tools.json \
//	  -arms all -out bench/results
//	go run ./bench/cmd/bench -toolret bench/results/cache/toolret -subset 250 -seed 42 \
//	  -arms baseline_json,compact_sig -out bench/results
//	go run ./bench/cmd/bench -livemcptool specs/083-discovery-profiler/datasets/livemcptool_snapshot \
//	  -arms baseline_json,compact_sig,tscg,toon_listing -out bench/results
//
// Live mode boots against a running proxy (see bench/docker-compose.yml) to
// add the exact-token comparison (full schemas), retrieval accuracy, search
// latency, and — Spec 083 US1 — the retrieve_tools RESPONSE cost over the
// real MCP protocol with break-even analysis:
//
//	go run ./bench/cmd/bench -live [-proxy URL] [-api-key KEY] [-golden PATH] \
//	  [-corpus-v2 PATH] [-expected-tools N]
//
// In live mode -corpus-v2 supplies the full-definition schemas for the naive
// full-menu count (joined to live tools by id — GET /api/v1/tools can serve
// stub schemas), and -expected-tools surfaces corpus drift (FR-021).
//
// A LAP lint artifact (`uvx --from lap-score==0.8.0 lap lint --json`) merges
// into either report via -lap-json. Reports land in bench/results/
// (gitignored — reports are never committed, per the Spec 065 CN-003 rule).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
	"github.com/smart-mcp-proxy/mcpproxy-go/bench/arms"
	"github.com/smart-mcp-proxy/mcpproxy-go/bench/corpusio"
)

// toonResultsArmName is the pseudo-arm selectable via -arms: fixture-driven
// (result payloads, not tool definitions), so it lives outside the registry.
const toonResultsArmName = "toon_results"

func main() {
	corpusPath := flag.String("corpus", "specs/065-evaluation-foundation/datasets/corpus_v1.tools.json", "path to the frozen tool corpus snapshot (legacy v1 report)")
	outDir := flag.String("out", "bench/results", "output directory for reports")
	encoding := flag.String("encoding", bench.DefaultEncoding, "tiktoken encoding name")
	live := flag.Bool("live", false, "run the live benchmark against a running proxy (full schemas + accuracy + latency + response cost)")
	proxy := flag.String("proxy", "http://127.0.0.1:8092", "live proxy base URL")
	apiKey := flag.String("api-key", "eval-corpus-snapshot", "live proxy API key (X-API-Key)")
	goldenPath := flag.String("golden", "specs/065-evaluation-foundation/datasets/retrieval_golden_v1.json", "path to the retrieval golden set")

	// Spec 083 profiler flags (T025/T030/T032).
	armsFlag := flag.String("arms", "", "comma-separated encoding arms to run, or 'all' (enables profiler mode; see bench/arms)")
	corpusV2Path := flag.String("corpus-v2", "", "path to the Spec 083 schema-bearing frozen corpus (corpus_v2.tools.json); with -live it is the schema SOURCE joined to live tools by id (GET /api/v1/tools can serve stub schemas)")
	toolretDir := flag.String("toolret", "", "ToolRet cache directory from scripts/fetch-toolret.sh (bench/results/cache/toolret[/<revision>]) — runtime fetch, never committed")
	livemcptoolPath := flag.String("livemcptool", "", "LiveMCPTool committed snapshot directory (or its tools.json)")
	subset := flag.Int("subset", 250, "seeded ToolRet query-subset size (FR-014)")
	seed := flag.Int64("seed", 42, "seed for the deterministic query subset (FR-014)")
	lapJSON := flag.String("lap-json", "", "path to a LAP lint artifact (lap.json) to merge as the independent verdict")
	resultFixtures := flag.String("result-fixtures", "specs/083-discovery-profiler/datasets/result_fixtures_v1.json", "tool-result fixture set for the toon_results arm")
	expectedTools := flag.Int("expected-tools", 0, "live mode: expected upstream tool count (from the frozen corpus); a differing live catalog is surfaced as a corpus-drift warning (FR-021)")
	flag.Parse()

	if *live {
		// In live mode -corpus-v2 is the schema SOURCE for the naive
		// full-menu count (GET /api/v1/tools can serve stub schemas).
		runLive(liveOptions{
			proxy:         *proxy,
			apiKey:        *apiKey,
			goldenPath:    *goldenPath,
			outDir:        *outDir,
			lapPath:       *lapJSON,
			corpusV2Path:  *corpusV2Path,
			expectedTools: *expectedTools,
		})
		return
	}
	if *armsFlag != "" || *corpusV2Path != "" || *toolretDir != "" || *livemcptoolPath != "" {
		runProfiler(profilerOptions{
			encoding:        *encoding,
			outDir:          *outDir,
			armsCSV:         *armsFlag,
			corpusV2Path:    *corpusV2Path,
			toolretDir:      *toolretDir,
			livemcptoolPath: *livemcptoolPath,
			goldenPath:      *goldenPath,
			subset:          *subset,
			seed:            *seed,
			lapJSON:         *lapJSON,
			resultFixtures:  *resultFixtures,
		})
		return
	}
	runOffline(*corpusPath, *encoding, *outDir)
}

func runOffline(corpusPath, encoding, outDir string) {
	tk, err := bench.NewTokenizer(encoding)
	if err != nil {
		log.Fatalf("bench: %v", err)
	}
	corpus, err := bench.LoadCorpus(corpusPath)
	if err != nil {
		log.Fatalf("bench: %v", err)
	}

	report := bench.ComputeReport(tk, corpus)
	jsonPath, htmlPath, err := report.WriteReports(outDir)
	if err != nil {
		log.Fatalf("bench: %v", err)
	}

	fmt.Fprintf(os.Stdout, "mcpproxy token-reduction benchmark (corpus %s, %d tools, %s)\n", report.CorpusVersion, report.CorpusTools, report.Encoding)
	for _, m := range report.Modes {
		if m.Mode == bench.ModeBaseline {
			fmt.Fprintf(os.Stdout, "  %-16s %6d tokens (%d tools)  baseline\n", m.Mode, m.Tokens, m.ContextTools)
			continue
		}
		fmt.Fprintf(os.Stdout, "  %-16s %6d tokens (%d tools)  %.1f%% fewer tokens\n", m.Mode, m.Tokens, m.ContextTools, m.SavingsRatio*100)
	}
	fmt.Fprintf(os.Stdout, "wrote %s and %s\n", jsonPath, htmlPath)
}

// profilerOptions carries the Spec 083 offline profiler flag values.
type profilerOptions struct {
	encoding        string
	outDir          string
	armsCSV         string
	corpusV2Path    string
	toolretDir      string
	livemcptoolPath string
	goldenPath      string
	subset          int
	seed            int64
	lapJSON         string
	resultFixtures  string
}

// runProfiler is the Spec 083 offline profiler: encoding arms over the
// selected corpora into a v2 report + dashboard.
func runProfiler(opts profilerOptions) {
	tk, err := bench.NewTokenizer(opts.encoding)
	if err != nil {
		log.Fatalf("bench: %v", err)
	}
	if opts.corpusV2Path == "" && opts.toolretDir == "" && opts.livemcptoolPath == "" {
		log.Fatalf("bench: -arms needs at least one corpus flag (-corpus-v2, -toolret, or -livemcptool)")
	}

	armNames, wantToonResults := selectArmNames(opts.armsCSV, opts.resultFixtures)
	var sections []bench.OfflineSection
	report := &bench.ReportV2{}

	// corpus_v2: the schema-bearing frozen corpus, scored against the
	// in-house golden set (SC-003 gate corpus).
	if opts.corpusV2Path != "" {
		corpus, err := bench.LoadCorpusV2(opts.corpusV2Path)
		if err != nil {
			log.Fatalf("bench: %v", err)
		}
		golden := loadGoldenOrNil(opts.goldenPath)
		runArms, skipped := resolveArms(armNames, corpus.Version)
		sections = append(sections, bench.OfflineSection{
			Corpus: corpus,
			Descriptor: bench.CorpusDescriptor{
				ID: corpus.Version, Name: "corpus_v2", Version: corpus.Version,
				License:   "own capture of public no-auth reference-server metadata (repo-licensed)",
				Committed: true,
			},
			Golden:      golden,
			Arms:        runArms,
			SkippedArms: skipped,
		})
	}

	// ToolRet: runtime-fetched cache, seeded query subset, retrieval scoring
	// through the armindex path (T030).
	if opts.toolretDir != "" {
		cacheDir, err := resolveToolRetCacheDir(opts.toolretDir)
		if err != nil {
			log.Fatalf("bench: %v", err)
		}
		tr, err := corpusio.LoadToolRet(cacheDir)
		if err != nil {
			log.Fatalf("bench: %v", err)
		}
		goldenSubset, err := corpusio.SubsetQueries(tr.Golden, opts.seed, opts.subset)
		if err != nil {
			log.Fatalf("bench: %v", err)
		}
		corpusID := "toolret@" + tr.ToolsRevision
		runArms, skipped := resolveArms(armNames, corpusID)
		sections = append(sections, bench.OfflineSection{
			Corpus: tr.Corpus,
			Descriptor: bench.CorpusDescriptor{
				ID: corpusID, Name: "ToolRet", Version: tr.ToolsRevision,
				License:     "unstated upstream (runtime fetch only; never committed, FR-013)",
				Attribution: "mangopy/ToolRet-Tools + ToolRet-Queries (Hugging Face; tool-retrieval-benchmark, ACL 2025)",
				Committed:   false,
			},
			Golden:      goldenSubset,
			Arms:        runArms,
			SkippedArms: skipped,
		})
		report.Subset = &bench.SubsetInfo{Seed: int(opts.seed), Size: len(goldenSubset.Queries)}
		fmt.Fprintf(os.Stdout, "toolret: %d tools, %d/%d scoreable queries in subset (seed %d; upstream dropped %d, unscoreable %d)\n",
			len(tr.Corpus.Tools), len(goldenSubset.Queries), len(tr.Golden.Queries), opts.seed, tr.DroppedUpstream, tr.DroppedUnscoreable)
	}

	// LiveMCPTool: committed Apache-2.0 snapshot, token/scale only — its
	// relevance labels are not derivable (loader returns the explicit
	// absence reason; index-altering rows carry the explanatory note).
	if opts.livemcptoolPath != "" {
		snapshotPath := opts.livemcptoolPath
		if st, err := os.Stat(snapshotPath); err == nil && st.IsDir() {
			snapshotPath = filepath.Join(snapshotPath, "tools.json")
		}
		corpus, _, goldenAbsence, err := corpusio.LoadLiveMCPTool(snapshotPath)
		if err != nil {
			log.Fatalf("bench: %v", err)
		}
		fmt.Fprintf(os.Stdout, "livemcptool: %d tools; %s\n", len(corpus.Tools), goldenAbsence)
		runArms, skipped := resolveArms(armNames, corpus.Version)
		sections = append(sections, bench.OfflineSection{
			Corpus: corpus,
			Descriptor: bench.CorpusDescriptor{
				ID: corpus.Version, Name: "LiveMCPTool", Version: corpus.Version,
				License:     corpusio.LiveMCPToolLicense,
				Attribution: corpusio.LiveMCPToolAttribution,
				Committed:   true,
			},
			Golden:      nil,
			Arms:        runArms,
			SkippedArms: skipped,
		})
	}

	// toon_results: fixture-driven results-class rows (T038).
	if wantToonResults {
		fixtures, err := arms.LoadResultFixtures(opts.resultFixtures)
		if err != nil {
			log.Fatalf("bench: %v", err)
		}
		run, err := arms.RunToonResults(tk, fixtures)
		if err != nil {
			log.Fatalf("bench: %v", err)
		}
		sections = append(sections, bench.OfflineSection{
			Descriptor: bench.CorpusDescriptor{
				ID: fixtures.FixtureID, Name: "result_fixtures_v1 (tool-call outputs)",
				Version:   fixtures.FixtureID + "@" + fixtures.Captured,
				ToolCount: len(fixtures.Results),
				License:   "own capture of reference-server outputs (repo-licensed)",
				Committed: true,
			},
			ExtraArmRows: run.Rows,
		})
		fmt.Fprintf(os.Stdout, "toon_results: %.1f%% overall savings vs compact JSON (tabular %.1f%%, non-tabular %.1f%%)\n",
			run.Rows[1].SavingsVsBaselinePct, run.TabularSavingsPct(), run.NonTabularSavingsPct())
	}

	built, err := bench.BuildOfflineReportV2(tk, bench.GeneratedAtNow(), sections)
	if err != nil {
		log.Fatalf("bench: %v", err)
	}
	built.Subset = report.Subset
	mergeLap(built, opts.lapJSON, bench.InHouseProxyMenuTokens(tk))

	jsonPath, htmlPath, err := built.WriteReports(opts.outDir)
	if err != nil {
		log.Fatalf("bench: %v", err)
	}
	printArmRows(built)
	fmt.Fprintf(os.Stdout, "wrote %s and %s\n", jsonPath, htmlPath)
}

// loadGoldenOrNil loads the in-house golden set; an empty path means "score
// nothing" (nil golden — index-altering rows carry the explanatory note).
func loadGoldenOrNil(path string) *bench.GoldenSet {
	if path == "" {
		return nil
	}
	golden, err := bench.LoadGoldenSet(path)
	if err != nil {
		log.Fatalf("bench: %v", err)
	}
	return golden
}

// selectArmNames parses -arms: empty or "all" selects every registered arm
// plus toon_results when its fixture file exists; a CSV selects exactly the
// named arms (toon_results allowed as a pseudo-arm).
func selectArmNames(csv, fixturePath string) (names []string, wantToonResults bool) {
	if csv == "" || csv == "all" {
		if _, err := os.Stat(fixturePath); err == nil {
			wantToonResults = true
		}
		return arms.Names(), wantToonResults
	}
	seen := map[string]bool{}
	for _, raw := range strings.Split(csv, ",") {
		name := strings.TrimSpace(raw)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if name == toonResultsArmName {
			wantToonResults = true
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, wantToonResults
}

// resolveArms resolves registry arms, converting runtime absences into
// arm-level skip rows (contract rule 5) and failing hard on unknown names.
func resolveArms(names []string, corpusID string) ([]bench.EncodingArm, []bench.ArmResult) {
	var resolved []bench.EncodingArm
	var skipped []bench.ArmResult
	for _, name := range names {
		arm, err := arms.Resolve(name)
		if err != nil {
			if errors.Is(err, arms.ErrArmUnavailable) {
				fmt.Fprintf(os.Stderr, "bench: arm %q skipped: %v\n", name, err)
				skipped = append(skipped, bench.SkippedArmResult(name, corpusID, err.Error()))
				continue
			}
			log.Fatalf("bench: %v", err)
		}
		resolved = append(resolved, arm)
	}
	return resolved, skipped
}

// resolveToolRetCacheDir accepts either the revision directory itself (has
// tools.json) or its parent (bench/results/cache/toolret) containing exactly
// one revision subdirectory.
func resolveToolRetCacheDir(dir string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "tools.json")); err == nil {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("toolret cache dir %q unreadable — run scripts/fetch-toolret.sh first: %w", dir, err)
	}
	var candidates []string
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, e.Name(), "tools.json")); err == nil {
				candidates = append(candidates, e.Name())
			}
		}
	}
	sort.Strings(candidates)
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("toolret cache dir %q has no revision with tools.json — run scripts/fetch-toolret.sh first", dir)
	case 1:
		return filepath.Join(dir, candidates[0]), nil
	default:
		return "", fmt.Errorf("toolret cache dir %q has %d revisions (%s) — pass the revision directory explicitly", dir, len(candidates), strings.Join(candidates, ", "))
	}
}

// mergeLap parses a LAP artifact (T032), compares its menu-token count with
// the in-house count of the same surface (FR-016), and attaches the verdict.
// An empty path attaches nothing; a broken artifact attaches a skip verdict
// (never fails the run, FR-015).
func mergeLap(report *bench.ReportV2, lapPath string, inHouseMenuTokens int) {
	if lapPath == "" {
		return
	}
	verdict := bench.ParseLapJSON(lapPath)
	divergence, warn := verdict.Compare(inHouseMenuTokens)
	report.Lap = &verdict
	report.Provenance["lap"] = bench.ProvenanceMeasured
	if !verdict.Executed {
		fmt.Fprintf(os.Stderr, "bench: LAP verdict skipped: %s\n", verdict.SkipReason)
		return
	}
	fmt.Fprintf(os.Stdout, "lap: grade %s, menu %d tokens vs in-house %d (divergence %.1f%%)\n",
		verdict.Grade, verdict.MenuTokens, inHouseMenuTokens, divergence)
	if warn {
		fmt.Fprintf(os.Stderr, "bench: WARNING: LAP menu-token divergence %.1f%% exceeds ±%.0f%% tolerance (FR-016; non-blocking)\n",
			divergence, bench.LapDivergenceTolerancePct)
	}
}

// printArmRows summarizes the arm rows on stdout.
func printArmRows(r *bench.ReportV2) {
	fmt.Fprintf(os.Stdout, "mcpproxy discovery profiler (%s): %d arm rows over %d corpora\n",
		r.Tokenizer.Name, len(r.Arms), len(r.Corpora))
	for _, row := range r.Arms {
		if row.Skipped {
			fmt.Fprintf(os.Stdout, "  %-14s %-28s SKIPPED: %s\n", row.Arm, row.CorpusID, row.SkipReason)
			continue
		}
		quality := ""
		if row.Quality != nil && row.Quality.RecallAt5 > 0 {
			quality = fmt.Sprintf("  recall@5=%.3f mrr=%.3f", row.Quality.RecallAt5, row.Quality.MRR)
		}
		fmt.Fprintf(os.Stdout, "  %-14s %-28s %8d tokens  %6.1f%% savings  (%d skipped tools)%s\n",
			row.Arm, row.CorpusID, row.TotalTokens, row.SavingsVsBaselinePct, row.SkippedTools, quality)
	}
}

// liveOptions carries the live-mode flag values.
type liveOptions struct {
	proxy         string
	apiKey        string
	goldenPath    string
	outDir        string
	lapPath       string
	corpusV2Path  string
	expectedTools int
}

func runLive(opts liveOptions) {
	golden, err := bench.LoadGoldenSet(opts.goldenPath)
	if err != nil {
		log.Fatalf("bench: %v", err)
	}
	client := bench.NewLiveClient(opts.proxy, opts.apiKey)
	report, err := bench.RunLive(context.Background(), client, golden, bench.LiveRunOptions{
		CorpusV2Path:      opts.corpusV2Path,
		ExpectedToolCount: opts.expectedTools,
	})
	if err != nil {
		log.Fatalf("bench: %v", err)
	}
	jsonPath, err := report.WriteJSON(opts.outDir)
	if err != nil {
		log.Fatalf("bench: %v", err)
	}

	fmt.Fprintf(os.Stdout, "mcpproxy LIVE benchmark (proxy %s, %s)\n", report.Proxy, report.Encoding)
	tr := report.Tokens
	fmt.Fprintf(os.Stdout, "  tokens: %d upstream tools, baseline %d tokens (schema source: %s)\n", tr.UpstreamTools, tr.BaselineTokens, tr.SchemaSource)
	if pi := report.ProxyInfo; pi != nil && pi.ExpectedToolCount > 0 && pi.ToolCount != pi.ExpectedToolCount {
		fmt.Fprintf(os.Stderr, "bench: WARNING: corpus drift — live catalog has %d tools, expected %d (FR-021; the report and dashboard surface this)\n",
			pi.ToolCount, pi.ExpectedToolCount)
	}
	for _, m := range tr.Modes {
		if m.Mode == bench.ModeBaseline {
			continue
		}
		if tr.AuthoritativeHeadline {
			fmt.Fprintf(os.Stdout, "    %-16s %6d tokens  %.1f%% fewer\n", m.Mode, m.Tokens, m.SavingsRatio*100)
		} else {
			fmt.Fprintf(os.Stdout, "    %-16s %6d tokens  (savings withheld — see notes)\n", m.Mode, m.Tokens)
		}
	}
	if !tr.AuthoritativeHeadline {
		for _, n := range tr.Notes {
			fmt.Fprintf(os.Stdout, "  NOTE: %s\n", n)
		}
	}
	r := report.Retrieval
	fmt.Fprintf(os.Stdout, "  accuracy (%d queries): Recall@1=%.3f Recall@5=%.3f MRR=%.3f nDCG@10=%.3f MAP=%.3f\n",
		r.QueryCount, r.Metrics.RecallAt[1], r.Metrics.RecallAt[5], r.Metrics.MRR, r.Metrics.NDCGAt10, r.Metrics.MAP)
	l := report.Latency
	fmt.Fprintf(os.Stdout, "  REST search latency (%d GET /api/v1/index/search calls): p50=%.1fms p95=%.1fms p99=%.1fms max=%.1fms; load-all-tools=%.1fms\n",
		l.Samples, l.P50ms, l.P95ms, l.P99ms, l.MaxMs, l.LoadAllToolsMs)
	if md := report.MCPDiscoveryLatency; md != nil {
		fmt.Fprintf(os.Stdout, "  MCP discovery latency (retrieve_tools calls): p50=%.1fms p95=%.1fms p99=%.1fms max=%.1fms\n",
			md.P50Ms, md.P95Ms, md.P99Ms, md.MaxMs)
	}

	// Spec 083 US1: response cost + break-even over the real MCP protocol.
	if rc := report.ResponseCost; rc != nil {
		fmt.Fprintf(os.Stdout, "  response cost (%d MCP retrieve_tools calls): p50=%d p95=%d max=%d mean=%.1f tokens\n",
			len(rc.PerQuery), rc.P50, rc.P95, rc.Max, rc.Mean)
	}
	if report.ResponseCostNote != "" {
		fmt.Fprintf(os.Stdout, "  NOTE: %s\n", report.ResponseCostNote)
	}
	if be := report.BreakEven; be != nil {
		if be.NoBreakEven {
			fmt.Fprintf(os.Stdout, "  break-even: none (proxy menu %d >= naive full menu %d tokens)\n", be.ProxyMenuTokens, be.NaiveFullMenuTokens)
		} else {
			fmt.Fprintf(os.Stdout, "  break-even: %.1f discovery calls (naive %d - proxy menu %d over mean response %.1f)\n",
				be.BreakEvenCalls, be.NaiveFullMenuTokens, be.ProxyMenuTokens, be.MeanResponseTokens)
		}
	}
	fmt.Fprintf(os.Stdout, "wrote %s\n", jsonPath)

	// Versioned v2 report + dashboard for the live run (research D12), with
	// the LAP verdict merged when an artifact was provided (T032). The
	// in-house comparison base is the measured retrieve_tools menu.
	r2 := report.ToReportV2(bench.GeneratedAtNow())
	inHouseMenu := 0
	for _, m := range tr.Modes {
		if m.Mode == bench.ModeRetrieveTools {
			inHouseMenu = m.Tokens
		}
	}
	mergeLap(r2, opts.lapPath, inHouseMenu)
	jsonV2Path, htmlPath, err := r2.WriteReports(opts.outDir)
	if err != nil {
		log.Fatalf("bench: %v", err)
	}
	fmt.Fprintf(os.Stdout, "wrote %s and %s\n", jsonV2Path, htmlPath)
}
