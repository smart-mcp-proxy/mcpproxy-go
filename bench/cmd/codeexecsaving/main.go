// Command codeexecsaving reports the RESPONSE-SIDE saving of code execution
// from a recorded activity export: what the sandbox's sub-call responses would
// have cost an agent that made those calls itself, against the script it sent
// and the single result it got back.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench/replaycorpus"
)

func main() {
	in := flag.String("in", "", "activity export JSONL (must live outside the repository)")
	// Default OFF, matching replaycorpus's zero value: bodies-on reads recorded
	// request and response CONTENT unmasked. With the sub-call byte counts now
	// emitted, bodies-off still yields a byte-basis figure.
	bodies := flag.Bool("bodies-unmasked", false, "read recorded bodies and count real TOKENS (reads unmasked content)")
	flag.Parse()
	if *in == "" {
		fmt.Fprintln(os.Stderr, "-in is required")
		os.Exit(2)
	}

	policy := replaycorpus.BodiesOff
	if *bodies {
		policy = replaycorpus.BodiesOnUnmasked
	}
	corpus, err := replaycorpus.LoadFile(*in, replaycorpus.Options{Bodies: policy})
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}
	rep := replaycorpus.CodeExecSavingsFor(corpus.Sessions)

	fmt.Printf("sessions %d  bodies=%v\n", len(corpus.Sessions), policy)
	for _, w := range corpus.Warnings {
		fmt.Println("  warning:", w)
	}
	bases := make([]string, 0, len(rep.Buckets))
	for b := range rep.Buckets {
		bases = append(bases, string(b))
	}
	sort.Strings(bases)
	if len(bases) == 0 {
		fmt.Println("\nno code_execution call produced a figure")
	}
	for _, b := range bases {
		k := rep.Buckets[replaycorpus.CostBasis(b)]
		unit := "bytes"
		if b == string(replaycorpus.CostMeasured) {
			unit = "tokens"
		}
		fmt.Printf("\n[%s] %d call(s), %d sub-call(s) — unit: %s\n", b, k.Calls, k.SubCalls, unit)
		fmt.Printf("  baseline (agent makes the sub-calls itself) : %d\n", k.Baseline)
		fmt.Printf("  proxy    (script sent + result returned)    : %d\n", k.Proxy)
		fmt.Printf("  saving                                      : %+d", k.Saving)
		if k.Baseline > 0 {
			fmt.Printf("  (%.1f%% of baseline)", 100*float64(k.Saving)/float64(k.Baseline))
		}
		fmt.Println()
	}
	for reason, n := range rep.Withheld {
		fmt.Printf("\nwithheld: %d call(s) — %s\n", n, reason)
	}
	if b, err := json.MarshalIndent(rep.Savings, "", " "); err == nil && len(rep.Savings) > 0 {
		fmt.Println("\nper-call detail:")
		fmt.Println(string(b))
	}
}
