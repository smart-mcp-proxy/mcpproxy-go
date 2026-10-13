// Command breakevencurve computes the fleet-size break-even curve: at how many
// upstream tools (and servers) does the proxy stop costing more than it saves?
//
// Net(n) = B(n) - M - sum(r), where B(n) is the cumulative cost of n upstream
// tool definitions, M is mcpproxy's own advertised menu (fixed), and sum(r) is
// the measured cost of the session's discovery responses.
//
// B(n) depends on WHICH n tools: definition sizes span 145..6485 bytes on the
// reference snapshot, so a single ordering yields one arbitrary curve. This
// bootstraps over many random orderings and reports the median and a p10..p90
// band, so the answer is a distribution and reads as one.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sort"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
	"github.com/smart-mcp-proxy/mcpproxy-go/bench/corpusio"
)

func main() {
	corpusPath := flag.String("corpus", "specs/083-discovery-profiler/datasets/livemcptool_snapshot/tools.json", "livemcptool snapshot")
	trials := flag.Int("trials", 400, "bootstrap orderings")
	meanResp := flag.Float64("mean-response", 1704.3, "mean discovery response tokens")
	calls := flag.Int("calls", 3, "discovery calls per session")
	jsonOut := flag.String("json", "", "write curve data to this path")
	seed := flag.Int64("seed", 20260901, "rng seed (pinned for reproducibility)")
	flag.Parse()

	corpus, _, _, err := corpusio.LoadLiveMCPTool(*corpusPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}
	tk, err := bench.NewTokenizer("cl100k_base")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tokenizer:", err)
		os.Exit(1)
	}

	// Per-tool cost, computed once. BPE is not additive across concatenation,
	// so this is an approximation of B(n) by summation -- stated, not hidden.
	cost := make([]int, len(corpus.Tools))
	servers := make([]string, len(corpus.Tools))
	for i, tl := range corpus.Tools {
		cost[i] = tk.CountToolWithSchema(tl)
		servers[i] = tl.Server
	}
	total := 0
	for _, c := range cost {
		total += c
	}

	spend := *meanResp * float64(*calls)
	fmt.Printf("corpus            : %s (%d tools)\n", corpus.Version, len(corpus.Tools))
	fmt.Printf("B(all)            : %d tokens   mean %.1f/tool\n", total, float64(total)/float64(len(cost)))
	fmt.Printf("session spend     : %d calls x %.1f = %.0f tokens\n\n", *calls, *meanResp, spend)

	for _, mode := range []string{bench.ModeRetrieveTools, bench.ModeCodeExecution} {
		m := 0
		for _, tl := range bench.ProxyToolsForMode(mode) {
			m += tk.CountToolWithSchema(tl)
		}
		threshold := float64(m) + spend
		rng := rand.New(rand.NewSource(*seed))
		crossings := make([]int, 0, *trials)
		never := 0
		for t := 0; t < *trials; t++ {
			perm := rng.Perm(len(cost))
			run, hit := 0.0, -1
			for k, idx := range perm {
				run += float64(cost[idx])
				if run >= threshold {
					hit = k + 1
					break
				}
			}
			if hit < 0 {
				never++
				continue
			}
			crossings = append(crossings, hit)
		}
		sort.Ints(crossings)
		fmt.Printf("mode %-15s menu M = %d tokens\n", mode, m)
		fmt.Printf("  break-even threshold : %.0f tokens (M + session spend)\n", threshold)
		if never > 0 {
			fmt.Printf("  NEVER breaks even in %d/%d orderings (whole fleet is cheaper than the proxy)\n", never, *trials)
		}
		if len(crossings) > 0 {
			fmt.Printf("  break-even tools     : p10=%d  median=%d  p90=%d\n",
				crossings[len(crossings)/10], crossings[len(crossings)/2], crossings[len(crossings)*9/10])
		}
		fmt.Println()
	}

	if *jsonOut != "" {
		emitCurve(*jsonOut, tk, cost, servers, spend, *trials, *seed, corpus.Version)
	}

	// Servers are what a user actually adds or removes.
	byServer := map[string]int{}
	for i := range cost {
		byServer[servers[i]] += cost[i]
	}
	sizes := make([]int, 0, len(byServer))
	for _, v := range byServer {
		sizes = append(sizes, v)
	}
	sort.Ints(sizes)
	fmt.Printf("servers           : %d, median %d tokens each (p10=%d p90=%d)\n",
		len(sizes), sizes[len(sizes)/2], sizes[len(sizes)/10], sizes[len(sizes)*9/10])
}

type curvePoint struct {
	N   int     `json:"n"`
	P10 float64 `json:"p10"`
	Med float64 `json:"median"`
	P90 float64 `json:"p90"`
}

// emitCurve writes net-token curves (B(n) - M - spend) per mode. Menus are
// listed with the value bench computes AND the deployed default, which differ
// because ProxyModeToolDefs pins EnableCodeExecution:true while the shipped
// default serves a disabled stub.
func emitCurve(path string, tk *bench.Tokenizer, cost []int, servers []string, spend float64, trials int, seed int64, version string) {
	rng := rand.New(rand.NewSource(seed))
	perms := make([][]int, trials)
	for t := range perms {
		perms[t] = rng.Perm(len(cost))
	}
	// Cumulative B(n) per ordering.
	cum := make([][]float64, trials)
	for t, perm := range perms {
		row := make([]float64, len(cost)+1)
		for k, idx := range perm {
			row[k+1] = row[k] + float64(cost[idx])
		}
		cum[t] = row
	}
	menus := map[string]int{}
	for _, mode := range []string{bench.ModeRetrieveTools, bench.ModeCodeExecution} {
		m := 0
		for _, tl := range bench.ProxyToolsForMode(mode) {
			m += tk.CountToolWithSchema(tl)
		}
		menus[mode] = m
	}
	out := map[string]interface{}{
		"corpus": version, "tools": len(cost), "spend": spend,
		"menus": menus, "trials": trials,
	}
	catalog := []curvePoint{}
	for n := 0; n <= len(cost); n++ {
		vals := make([]float64, trials)
		for t := range cum {
			vals[t] = cum[t][n]
		}
		sort.Float64s(vals)
		catalog = append(catalog, curvePoint{N: n, P10: vals[trials/10], Med: vals[trials/2], P90: vals[trials*9/10]})
	}
	out["tool_catalog_curve"] = catalog
	curves := map[string][]curvePoint{}
	step := 1
	for mode, m := range menus {
		pts := []curvePoint{}
		thr := float64(m) + spend
		for n := 0; n <= len(cost); n += step {
			vals := make([]float64, trials)
			for t := range cum {
				vals[t] = cum[t][n] - thr
			}
			sort.Float64s(vals)
			pts = append(pts, curvePoint{N: n,
				P10: vals[trials/10], Med: vals[trials/2], P90: vals[trials*9/10]})
		}
		curves[mode] = pts
	}
	out["curves"] = curves
	byServer := map[string]int{}
	for i := range cost {
		byServer[servers[i]] += cost[i]
	}
	// Bootstrap over SERVER orderings too: a user adds and removes servers,
	// not individual tools, and server sizes are far more skewed than tool
	// sizes (p10=152, p90=3184 on this snapshot).
	svcCost := make([]int, 0, len(byServer))
	for _, v := range byServer {
		svcCost = append(svcCost, v)
	}
	srng := rand.New(rand.NewSource(seed))
	scum := make([][]float64, trials)
	for t := range scum {
		perm := srng.Perm(len(svcCost))
		row := make([]float64, len(svcCost)+1)
		for k, idx := range perm {
			row[k+1] = row[k] + float64(svcCost[idx])
		}
		scum[t] = row
	}
	scurve := []curvePoint{}
	for n := 0; n <= len(svcCost); n++ {
		vals := make([]float64, trials)
		for t := range scum {
			vals[t] = scum[t][n]
		}
		sort.Float64s(vals)
		scurve = append(scurve, curvePoint{N: n, P10: vals[trials/10], Med: vals[trials/2], P90: vals[trials*9/10]})
	}
	out["server_catalog_curve"] = scurve
	out["servers"] = len(byServer)
	b, _ := json.MarshalIndent(out, "", " ")
	_ = os.WriteFile(path, b, 0o644)
	fmt.Println("curve written:", path)
}
