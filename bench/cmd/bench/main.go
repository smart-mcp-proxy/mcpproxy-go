// Command bench runs the mcpproxy token-reduction benchmark over a frozen tool
// corpus and writes a JSON report plus a static HTML dashboard.
//
// Usage:
//
//	go run ./bench/cmd/bench [-corpus PATH] [-out DIR] [-encoding NAME]
//
// With no flags it scores the committed Spec 065 frozen corpus and writes the
// reports to bench/results/ (gitignored — reports are never committed, per the
// Spec 065 CN-003 repo rule).
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
)

func main() {
	corpusPath := flag.String("corpus", "specs/065-evaluation-foundation/datasets/corpus_v1.tools.json", "path to the frozen tool corpus snapshot")
	outDir := flag.String("out", "bench/results", "output directory for report.json and dashboard.html")
	encoding := flag.String("encoding", bench.DefaultEncoding, "tiktoken encoding name")
	flag.Parse()

	tk, err := bench.NewTokenizer(*encoding)
	if err != nil {
		log.Fatalf("bench: %v", err)
	}
	corpus, err := bench.LoadCorpus(*corpusPath)
	if err != nil {
		log.Fatalf("bench: %v", err)
	}

	report := bench.ComputeReport(tk, corpus)
	jsonPath, htmlPath, err := report.WriteReports(*outDir)
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
