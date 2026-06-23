// Command bench runs the mcpproxy benchmark.
//
// Default (offline) mode scores the committed Spec 065 frozen corpus for
// token reduction and writes a JSON report plus a static HTML dashboard:
//
//	go run ./bench/cmd/bench [-corpus PATH] [-out DIR] [-encoding NAME]
//
// Live mode boots against a running proxy (see bench/docker-compose.yml) to add
// the exact-token comparison (full schemas), retrieval accuracy (Recall@k / MRR
// / nDCG over the golden set), and search latency:
//
//	go run ./bench/cmd/bench -live [-proxy URL] [-api-key KEY] [-golden PATH]
//
// Reports land in bench/results/ (gitignored — reports are never committed, per
// the Spec 065 CN-003 repo rule).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
)

func main() {
	corpusPath := flag.String("corpus", "specs/065-evaluation-foundation/datasets/corpus_v1.tools.json", "path to the frozen tool corpus snapshot")
	outDir := flag.String("out", "bench/results", "output directory for reports")
	encoding := flag.String("encoding", bench.DefaultEncoding, "tiktoken encoding name")
	live := flag.Bool("live", false, "run the live benchmark against a running proxy (full schemas + accuracy + latency)")
	proxy := flag.String("proxy", "http://127.0.0.1:8092", "live proxy base URL")
	apiKey := flag.String("api-key", "eval-corpus-snapshot", "live proxy API key (X-API-Key)")
	goldenPath := flag.String("golden", "specs/065-evaluation-foundation/datasets/retrieval_golden_v1.json", "path to the retrieval golden set")
	flag.Parse()

	if *live {
		runLive(*proxy, *apiKey, *goldenPath, *outDir)
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

func runLive(proxy, apiKey, goldenPath, outDir string) {
	golden, err := bench.LoadGoldenSet(goldenPath)
	if err != nil {
		log.Fatalf("bench: %v", err)
	}
	client := bench.NewLiveClient(proxy, apiKey)
	report, err := bench.RunLive(context.Background(), client, golden)
	if err != nil {
		log.Fatalf("bench: %v", err)
	}
	jsonPath, err := report.WriteJSON(outDir)
	if err != nil {
		log.Fatalf("bench: %v", err)
	}

	fmt.Fprintf(os.Stdout, "mcpproxy LIVE benchmark (proxy %s, %s)\n", report.Proxy, report.Encoding)
	tr := report.Tokens
	fmt.Fprintf(os.Stdout, "  tokens: %d upstream tools, baseline %d tokens (with full schemas)\n", tr.UpstreamTools, tr.BaselineTokens)
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
	fmt.Fprintf(os.Stdout, "  latency (%d searches): p50=%.1fms p95=%.1fms p99=%.1fms max=%.1fms; load-all-tools=%.1fms\n",
		l.Samples, l.P50ms, l.P95ms, l.P99ms, l.MaxMs, l.LoadAllToolsMs)
	fmt.Fprintf(os.Stdout, "wrote %s\n", jsonPath)
}
