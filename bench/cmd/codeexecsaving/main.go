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

// glossFor spells out a baseline mode, because "direct" alone is ambiguous in
// this codebase: config.RoutingModeDirect is also "direct" and means every
// upstream tool exposed THROUGH the proxy, where responses ARE cut at
// tool_response_limit — nearly the opposite of what it means here.
func glossFor(m replaycorpus.BaselineMode) string {
	if m == replaycorpus.BaselineProxy {
		return "agent calls through this proxy, responses cut at tool_response_limit"
	}
	return "agent talks to the upstream servers itself, no proxy in the path"
}

func main() {
	in := flag.String("in", "", "activity export JSONL (must live outside the repository)")
	inPrice := flag.Float64("in-price", 3.0, "USD per Mtok, input")
	outPrice := flag.Float64("out-price", 15.0, "USD per Mtok, output")
	cacheMult := flag.Float64("cache-mult", 0.1, "input multiplier for a cached prefix (1.0 = uncached)")
	// Default OFF, matching replaycorpus's zero value: bodies-on reads recorded
	// request and response CONTENT unmasked. With the sub-call byte counts now
	// emitted, bodies-off still yields a byte-basis figure.
	bodies := flag.Bool("bodies-unmasked", false, "read recorded bodies and count real TOKENS (reads unmasked content)")
	baseline := flag.String("baseline", "direct",
		"counterfactual the saving is measured against: "+
			"direct (agent talks to the upstream servers itself, no proxy in the path — nothing truncates) or "+
			"proxy (agent calls through this proxy's call_tool_* path — responses cut at tool_response_limit)")
	flag.Parse()
	if *in == "" {
		fmt.Fprintln(os.Stderr, "-in is required")
		os.Exit(2)
	}
	// Refused rather than defaulted: a typo'd -baseline that quietly produced
	// the OTHER figure is the failure the selector exists to remove.
	mode, err := replaycorpus.ParseBaselineMode(*baseline)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
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
	rep := replaycorpus.CodeExecSavingsFor(corpus.Sessions, mode)

	// The bare word "direct" collides with config.RoutingModeDirect, which is a
	// PROXIED mode that does truncate. Never print it unglossed.
	fmt.Printf("sessions %d  bodies=%v  baseline=%s (%s)\n", len(corpus.Sessions), policy, mode, glossFor(mode))
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
		baselineLabel := "baseline (agent calls the upstream servers itself)"
		if mode == replaycorpus.BaselineProxy {
			baselineLabel = "baseline (agent calls through this proxy, responses cut)"
		}
		fmt.Printf("\n[%s] %d call(s), %d sub-call(s) — unit: %s\n", b, k.Calls, k.SubCalls, unit)
		fmt.Printf("  %-55s: %d\n", baselineLabel, k.Baseline)
		fmt.Printf("  %-55s: %d\n", "proxy (script sent + result returned)", k.Proxy)
		fmt.Printf("  %-55s: %+d", "saving", k.Saving)
		if k.Baseline > 0 {
			fmt.Printf("  (%.1f%% of baseline)", 100*float64(k.Saving)/float64(k.Baseline))
		}
		fmt.Println()
		// A total that tolerates truncation must publish the magnitude, not
		// just the fact — see doc.go's third invariant.
		if k.TruncatedSubCalls > 0 {
			fmt.Printf("  NOTE: includes %d sub-call response(s) counted at their full pre-truncation\n", k.TruncatedSubCalls)
			fmt.Printf("        size (%d of the baseline). The recorded text was cut at 8KB; the byte\n", k.TruncatedBaseline)
			fmt.Println("        length was not. -baseline=proxy withholds these calls entirely.")
			// The whole-call figure, not just the component: proxy mode discards
			// the entire call, so this is what the published total would lose.
			fmt.Printf("        %d call(s), %d of the saving above, exist only because direct\n",
				k.TruncatedCalls, k.TruncatedCallSaving)
			fmt.Println("        mode tolerates truncation.")
		}
	}
	// Money, not just tokens: a call's ARGUMENTS are output (billed ~5x) and its
	// RESPONSE is input, so the token total is not proportional to cost. Priced
	// per-call and summed, since only measured calls can be priced at all.
	pricedTotal, priced, unpriceable := 0.0, 0, 0
	for _, sv := range rep.Savings {
		if usd, ok := sv.PricedSavingUSD(*inPrice, *outPrice, *cacheMult); ok {
			pricedTotal += usd
			priced++
		} else {
			unpriceable++
		}
	}
	if priced > 0 {
		fmt.Printf("\npriced over %d measured call(s) at $%.2f/$%.2f per Mtok, input x%.2f:\n",
			priced, *inPrice, *outPrice, *cacheMult)
		fmt.Printf("  net saving: %+.6f USD\n", pricedTotal)
		if pricedTotal < 0 {
			fmt.Println("  NOTE: negative in money. Code execution trades cheap input for expensive")
			fmt.Println("        output — the script is a fixed cost however few calls it replaces.")
		}
	}
	if unpriceable > 0 {
		fmt.Printf("  %d call(s) could not be priced (withheld, or byte estimates rather than tokens)\n", unpriceable)
	}

	for reason, n := range rep.Withheld {
		fmt.Printf("\nwithheld: %d call(s) — %s\n", n, reason)
	}
	// Under proxy, a reader seeing a pile of truncation withholds would
	// otherwise conclude nothing was measurable. Say how many the other
	// counterfactual recovers — without printing a second headline number,
	// because two savings figures on one screen is how the wrong one gets
	// quoted.
	if mode == replaycorpus.BaselineProxy && rep.Withheld[replaycorpus.ReasonTruncatedSubCallOverstates] > 0 {
		alt := replaycorpus.CodeExecSavingsFor(corpus.Sessions, replaycorpus.BaselineDirect)
		recovered := 0
		for _, sv := range alt.Savings {
			if sv.Basis != replaycorpus.CostUnavailable && sv.TruncatedSubCalls > 0 {
				recovered++
			}
		}
		fmt.Printf("  -baseline=direct counts %d of them (no proxy in the path, so nothing truncates)\n", recovered)
	}
	if len(rep.Withheld) > 0 {
		fmt.Println("\n(withheld reasons are not comparable across baselines: a call withheld for")
		fmt.Println(" truncation under proxy is re-tested under direct and may withhold for a")
		fmt.Println(" different reason.)")
	}
	if b, err := json.MarshalIndent(rep.Savings, "", " "); err == nil && len(rep.Savings) > 0 {
		fmt.Println("\nper-call detail:")
		fmt.Println(string(b))
	}
}
