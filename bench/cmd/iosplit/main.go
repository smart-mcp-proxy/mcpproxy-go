// Command iosplit reports code execution's cost split by BILLING DIRECTION.
//
// Every other figure in this harness sums a tool call's arguments and its
// response into one number. They are not the same kind of token: the arguments
// are text the MODEL GENERATED (output, billed ~5x input) and the response is
// text it reads (input, billed 1x, or 0.1x cached). Summing them prices a 5x
// asymmetry at 1x.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
)

type row struct {
	ToolName  string          `json:"tool_name"`
	RequestID string          `json:"request_id"`
	ParentID  string          `json:"parent_id"`
	Arguments json.RawMessage `json:"arguments"`
	Response  json.RawMessage `json:"response"`
}

func text(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

func main() {
	in := flag.String("in", "", "activity export JSONL")
	inPrice := flag.Float64("in-price", 3.0, "USD per Mtok, input")
	outPrice := flag.Float64("out-price", 15.0, "USD per Mtok, output")
	cacheMult := flag.Float64("cache-mult", 0.1, "cached-input multiplier")
	flag.Parse()

	f, err := os.Open(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	tk, err := bench.NewTokenizer("cl100k_base")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var rows []row
	dec := json.NewDecoder(f)
	for dec.More() {
		var r row
		if err := dec.Decode(&r); err != nil {
			break
		}
		rows = append(rows, r)
	}

	kids := map[string][]row{}
	for _, r := range rows {
		if r.ParentID != "" {
			kids[r.ParentID] = append(kids[r.ParentID], r)
		}
	}
	type out struct{ subs, bOut, bIn, cOut, cIn int }
	var results []out
	for _, p := range rows {
		if p.ToolName != "code_execution" {
			continue
		}
		subs := kids[p.RequestID]
		if len(subs) == 0 {
			continue
		}
		o := out{subs: len(subs), cOut: tk.Count(text(p.Arguments)), cIn: tk.Count(text(p.Response))}
		for _, s := range subs {
			o.bOut += tk.Count(text(s.Arguments))
			o.bIn += tk.Count(text(s.Response))
		}
		results = append(results, o)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].subs < results[j].subs })

	usd := func(in, outTok int, cached bool) float64 {
		m := 1.0
		if cached {
			m = *cacheMult
		}
		return (float64(in)*(*inPrice)*m + float64(outTok)*(*outPrice)) / 1e6
	}

	fmt.Printf("%-5s | %-17s | %-17s | %-9s | %s\n",
		"subs", "OUTPUT (generated)", "INPUT (read)", "tok saved", "USD saved (uncached / cached input)")
	fmt.Printf("%-5s | %-8s %-8s | %-8s %-8s |           |\n", "", "base", "codeex", "base", "codeex")
	fmt.Println("---------------------------------------------------------------------------------------------")
	for _, r := range results {
		tokSaved := (r.bOut + r.bIn) - (r.cOut + r.cIn)
		u := usd(r.bIn, r.bOut, false) - usd(r.cIn, r.cOut, false)
		c := usd(r.bIn, r.bOut, true) - usd(r.cIn, r.cOut, true)
		fmt.Printf("%-5d | %-8d %-8d | %-8d %-8d | %+9d | %+.6f / %+.6f\n",
			r.subs, r.bOut, r.cOut, r.bIn, r.cIn, tokSaved, u, c)
	}
}
