package bench

import (
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
)

// WriteJSON writes the report as indented JSON to path.
func (r *Report) WriteJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}
	return nil
}

// WriteHTML renders the report as a self-contained static dashboard. The output
// is a single file with no external assets so it can be published as-is to a
// static host (CI release-tag publishing is tracked as a follow-up).
func (r *Report) WriteHTML(path string) error {
	tmpl, err := template.New("dashboard").Funcs(template.FuncMap{
		"pct": func(f float64) string { return fmt.Sprintf("%.1f%%", f*100) },
	}).Parse(dashboardHTML)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %q: %w", path, err)
	}
	defer f.Close()
	if err := tmpl.Execute(f, r); err != nil {
		return fmt.Errorf("render dashboard: %w", err)
	}
	return nil
}

// WriteReports writes both report.json and dashboard.html into dir.
func (r *Report) WriteReports(dir string) (jsonPath, htmlPath string, err error) {
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("mkdir %q: %w", dir, err)
	}
	jsonPath = filepath.Join(dir, "report.json")
	htmlPath = filepath.Join(dir, "dashboard.html")
	if err = r.WriteJSON(jsonPath); err != nil {
		return "", "", err
	}
	if err = r.WriteHTML(htmlPath); err != nil {
		return "", "", err
	}
	return jsonPath, htmlPath, nil
}

// WriteHTML renders the v2 report as a self-contained static dashboard
// (Spec 083 T035, FR-017/018, SC-005): arms table, corpora table,
// response-cost percentiles, break-even, session estimates, LAP row,
// provenance badges on every headline section, and the tokenizer caveat
// banner. Single file, inline CSS only, no external resource loads —
// bench/report_test.go asserts self-containment.
func (r *ReportV2) WriteHTML(path string) error {
	tmpl, err := template.New("dashboardV2").Funcs(template.FuncMap{
		"f1":  func(f float64) string { return fmt.Sprintf("%.1f", f) },
		"pc1": func(f float64) string { return fmt.Sprintf("%.1f%%", f) },
		// prov looks a section's provenance label up for badge rendering;
		// missing keys render no badge rather than a wrong one.
		"prov": func(m map[string]string, key string) string { return m[key] },
	}).Parse(dashboardV2HTML)
	if err != nil {
		return fmt.Errorf("parse v2 template: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %q: %w", path, err)
	}
	defer f.Close()
	if err := tmpl.Execute(f, r); err != nil {
		return fmt.Errorf("render v2 dashboard: %w", err)
	}
	return nil
}

// WriteReports writes report.json and dashboard.html for a v2 run into dir.
func (r *ReportV2) WriteReports(dir string) (jsonPath, htmlPath string, err error) {
	jsonPath, err = r.WriteJSON(dir)
	if err != nil {
		return "", "", err
	}
	htmlPath = filepath.Join(dir, "dashboard.html")
	if err = r.WriteHTML(htmlPath); err != nil {
		return "", "", err
	}
	return jsonPath, htmlPath, nil
}

const dashboardHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>mcpproxy benchmark — token reduction</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 16px/1.5 system-ui, sans-serif; max-width: 880px; margin: 2rem auto; padding: 0 1rem; }
  h1 { margin-bottom: .25rem; }
  .sub { opacity: .7; margin-top: 0; }
  table { border-collapse: collapse; width: 100%; margin: 1.5rem 0; }
  th, td { padding: .6rem .8rem; text-align: right; border-bottom: 1px solid #8884; }
  th:first-child, td:first-child { text-align: left; }
  .save { font-weight: 600; color: #1a8f3c; }
  code { background: #8881; padding: .1rem .35rem; border-radius: 4px; }
  .notes { font-size: .9rem; opacity: .8; }
  .notes li { margin: .3rem 0; }
</style>
</head>
<body>
<h1>mcpproxy benchmark</h1>
<p class="sub">Token cost of loading tools into an agent's context, by routing mode.</p>
<p>Corpus <code>{{.CorpusVersion}}</code> &middot; {{.CorpusTools}} tools &middot; encoding <code>{{.Encoding}}</code></p>
<table>
  <thead>
    <tr><th>Mode</th><th>Tools in context</th><th>Context tokens</th><th>Savings vs. baseline</th></tr>
  </thead>
  <tbody>
  {{range .Modes}}
    <tr>
      <td><code>{{.Mode}}</code></td>
      <td>{{.ContextTools}}</td>
      <td>{{.Tokens}}</td>
      <td class="save">{{if eq .Mode "baseline"}}&mdash;{{else}}{{pct .SavingsRatio}}{{end}}</td>
    </tr>
  {{end}}
  </tbody>
</table>
<h2>Methodology notes</h2>
<ul class="notes">
{{range .Notes}}<li>{{.}}</li>{{end}}
</ul>
</body>
</html>
`

// dashboardV2HTML is the Spec 083 dashboard (research D12). Sections render
// conditionally on their data being present; provenance badges come from the
// report's Provenance map (SC-005); all styling is inline (FR-018).
const dashboardV2HTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>mcpproxy discovery-effectiveness profiler</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 15px/1.5 system-ui, sans-serif; max-width: 1080px; margin: 2rem auto; padding: 0 1rem; }
  h1 { margin-bottom: .25rem; }
  h2 { margin-top: 2rem; }
  .sub { opacity: .7; margin-top: 0; }
  table { border-collapse: collapse; width: 100%; margin: 1rem 0; }
  th, td { padding: .45rem .6rem; text-align: right; border-bottom: 1px solid #8884; vertical-align: top; }
  th:first-child, td:first-child { text-align: left; }
  td.l { text-align: left; }
  .save { font-weight: 600; color: #1a8f3c; }
  .neg { font-weight: 600; color: #c0392b; }
  code { background: #8881; padding: .1rem .35rem; border-radius: 4px; }
  .caveat { background: #f5a62333; border: 1px solid #f5a623; border-radius: 6px; padding: .6rem .9rem; margin: 1rem 0; font-size: .9rem; }
  .badge { display: inline-block; font-size: .72rem; font-weight: 600; text-transform: uppercase; letter-spacing: .04em; border-radius: 999px; padding: .1rem .55rem; margin-left: .4rem; vertical-align: middle; }
  .badge-measured { background: #1a8f3c22; color: #1a8f3c; border: 1px solid #1a8f3c66; }
  .badge-computed { background: #2d6cdf22; color: #2d6cdf; border: 1px solid #2d6cdf66; }
  .badge-estimated { background: #b3560022; color: #b35600; border: 1px solid #b3560066; }
  .stat { display: inline-block; margin: .3rem 1.6rem .3rem 0; }
  .stat b { display: block; font-size: 1.4rem; }
  .notes, .small { font-size: .85rem; opacity: .8; }
  .warn { color: #c0392b; font-weight: 600; }
</style>
</head>
<body>
<h1>mcpproxy discovery-effectiveness profiler</h1>
<p class="sub">Token cost, encoding arms, retrieval quality, and break-even for proxy-mediated tool discovery.</p>
<p class="small">Report v{{.ReportVersion}} &middot; generated {{.GeneratedAt}} &middot; tokenizer <code>{{.Tokenizer.Name}}</code>{{if .Proxy}}{{if .Proxy.Version}} &middot; proxy <code>{{.Proxy.Version}}</code>{{end}}{{if .Proxy.RoutingMode}} &middot; routing <code>{{.Proxy.RoutingMode}}</code>{{end}}{{if .Proxy.ToolsLimit}} &middot; tools_limit {{.Proxy.ToolsLimit}}{{end}}{{if .Proxy.ToolCount}} &middot; {{.Proxy.ToolCount}} upstream tools{{end}}{{end}}{{if .Subset}} &middot; query subset: size {{.Subset.Size}}, seed {{.Subset.Seed}}{{end}}</p>
<div class="caveat">&#9888;&#65039; <b>Tokenizer caveat:</b> {{.Tokenizer.Caveat}}</div>
{{if .Proxy}}{{if and .Proxy.ExpectedToolCount (ne .Proxy.ToolCount .Proxy.ExpectedToolCount)}}
<p class="warn">&#9888;&#65039; Corpus drift: the live proxy served {{.Proxy.ToolCount}} tools but the frozen corpus documents {{.Proxy.ExpectedToolCount}} (FR-021). Measured numbers describe the live catalog, not the frozen corpus.</p>
{{end}}{{end}}
<p class="small">Provenance badges: <span class="badge badge-measured">measured</span> observed over the real protocol/corpus &middot; <span class="badge badge-computed">computed</span> arithmetic over measured inputs &middot; <span class="badge badge-estimated">estimated</span> model with documented assumptions.</p>

{{if .Corpora}}
<h2>Corpora{{with prov .Provenance "corpora"}}<span class="badge badge-{{.}}">{{.}}</span>{{end}}</h2>
<table>
  <thead><tr><th>Corpus</th><th>Version</th><th>Tools</th><th>License</th><th>Committed</th><th>Degenerate descriptions</th></tr></thead>
  <tbody>
  {{range .Corpora}}
    <tr>
      <td><code>{{.ID}}</code>{{if .Attribution}}<div class="small">{{.Attribution}}</div>{{end}}</td>
      <td class="l">{{.Version}}</td>
      <td>{{.ToolCount}}</td>
      <td class="l">{{.License}}</td>
      <td>{{if .Committed}}yes{{else}}no (runtime fetch){{end}}</td>
      <td>{{if .DegenerateDescriptions}}{{.DegenerateDescriptions.Count}}{{else}}&mdash;{{end}}</td>
    </tr>
  {{end}}
  </tbody>
</table>
{{end}}

{{if .Arms}}
<h2>Encoding arms{{with prov .Provenance "arms"}}<span class="badge badge-{{.}}">{{.}}</span>{{end}}</h2>
<table>
  <thead><tr><th>Arm</th><th>Corpus</th><th>Class</th><th>Total tokens</th><th>Mean/tool</th><th>p95</th><th>Savings vs baseline</th><th>Skipped tools</th><th>Recall@5</th><th>MRR</th></tr></thead>
  <tbody>
  {{range .Arms}}
    <tr>
      <td><code>{{.Arm}}</code>{{if .LowerBound}}<div class="small">lower-bound estimate (descriptions rewritten/elided)</div>{{end}}</td>
      <td class="l"><code>{{.CorpusID}}</code></td>
      <td class="l">{{if .PayloadClass}}{{.PayloadClass}}{{if .FixtureID}}<div class="small">{{.FixtureID}} &middot; {{if .TabularCount}}{{.TabularCount}} tabular{{end}}{{if .NonTabularCount}} / {{.NonTabularCount}} non-tabular{{end}}</div>{{end}}{{else}}&mdash;{{end}}</td>
      {{if .Skipped}}
      <td colspan="7" class="l warn">SKIPPED: {{.SkipReason}}</td>
      {{else}}
      <td>{{.TotalTokens}}</td>
      <td>{{f1 .MeanTokens}}</td>
      <td>{{.P95Tokens}}</td>
      <td class="{{if lt .SavingsVsBaselinePct 0.0}}neg{{else}}save{{end}}">{{pc1 .SavingsVsBaselinePct}}</td>
      <td>{{.SkippedTools}}</td>
      {{if .Quality}}{{if .Quality.MetricNote}}{{if eq .Quality.RecallAt5 0.0}}<td colspan="2" class="l small">{{.Quality.MetricNote}}</td>{{else}}<td>{{f1 .Quality.RecallAt5}}</td><td>{{f1 .Quality.MRR}}</td>{{end}}{{else}}<td>{{f1 .Quality.RecallAt5}}</td><td>{{f1 .Quality.MRR}}</td>{{end}}{{else}}<td colspan="2" class="small">quality-neutral (rendering only)</td>{{end}}
      {{end}}
    </tr>
  {{end}}
  </tbody>
</table>
{{end}}

{{if .ResponseCost}}
<h2>retrieve_tools response cost{{with prov .Provenance "response_cost"}}<span class="badge badge-{{.}}">{{.}}</span>{{end}}</h2>
<div>
  <span class="stat"><b>{{.ResponseCost.P50}}</b>p50 tokens</span>
  <span class="stat"><b>{{.ResponseCost.P95}}</b>p95 tokens</span>
  <span class="stat"><b>{{.ResponseCost.Max}}</b>max tokens</span>
  <span class="stat"><b>{{f1 .ResponseCost.Mean}}</b>mean tokens</span>
</div>
{{if .ResponseCost.PerQuery}}
<table>
  <thead><tr><th>Query</th><th>Total</th><th>Results</th><th>Latency ms</th><th>input_schemas</th><th>descriptions</th><th>usage_instructions</th><th>metadata</th><th>other</th></tr></thead>
  <tbody>
  {{range .ResponseCost.PerQuery}}
    <tr>
      <td><code>{{.QueryID}}</code></td>
      <td>{{.TotalTokens}}</td>
      <td>{{.ResultCount}}</td>
      <td>{{f1 .LatencyMs}}</td>
      <td>{{index .Components "input_schemas"}}</td>
      <td>{{index .Components "descriptions"}}</td>
      <td>{{index .Components "usage_instructions"}}</td>
      <td>{{index .Components "metadata"}}</td>
      <td>{{index .Components "other"}}</td>
    </tr>
  {{end}}
  </tbody>
</table>
<p class="small">Component buckets are span-attributed over the exact wire bytes; per-query components sum EXACTLY to the total (FR-002).</p>
{{end}}
{{end}}

{{if .BreakEven}}
<h2>Break-even{{with prov .Provenance "break_even"}}<span class="badge badge-{{.}}">{{.}}</span>{{end}}</h2>
<p>naive full menu <b>{{.BreakEven.NaiveFullMenuTokens}}</b> tokens &middot; proxy menu <b>{{.BreakEven.ProxyMenuTokens}}</b> tokens &middot; mean discovery response <b>{{f1 .BreakEven.MeanResponseTokens}}</b> tokens</p>
{{if .BreakEven.NoBreakEven}}
<p class="warn">no break-even: the proxy menu already costs at least as much as the naive full menu — there are no menu savings to amortize.</p>
{{else}}
<p><span class="stat"><b>{{f1 .BreakEven.BreakEvenCalls}}</b>discovery calls to break even</span></p>
<p class="small">break_even_calls = (naive_full_menu_tokens &minus; proxy_menu_tokens) / mean_response_tokens (FR-003); below this many retrieve_tools calls per session the proxy is strictly cheaper.</p>
{{end}}
{{end}}

{{if .SessionEstimates}}
<h2>Session cost estimates{{with prov .Provenance "session_estimates"}}<span class="badge badge-{{.}}">{{.}}</span>{{end}}</h2>
<table>
  <thead><tr><th>Arm</th><th>Calls / session</th><th>Retry rate</th><th>Estimated session tokens</th></tr></thead>
  <tbody>
  {{range .SessionEstimates}}
    <tr><td><code>{{.Arm}}</code></td><td>{{.CallsPerSession}}</td><td>{{.RetryRate}}</td><td>{{.EstimatedTokens}}</td></tr>
  {{end}}
  </tbody>
</table>
<p class="small">session_cost = proxy_menu + calls &times; mean_response(arm) &times; (1 + retry_rate(arm)); retry rates are literature-derived defaults (research D8), so these rows are estimates, not measurements.</p>
{{end}}

{{if .Latency}}
<h2>Latency{{with prov .Provenance "latency"}}<span class="badge badge-{{.}}">{{.}}</span>{{end}}</h2>
<h3>REST search (<code>GET /api/v1/index/search</code>)</h3>
<div>
  <span class="stat"><b>{{f1 .Latency.P50Ms}}</b>p50 ms</span>
  <span class="stat"><b>{{f1 .Latency.P95Ms}}</b>p95 ms</span>
  <span class="stat"><b>{{f1 .Latency.P99Ms}}</b>p99 ms</span>
  <span class="stat"><b>{{f1 .Latency.MaxMs}}</b>max ms</span>
</div>
{{if .Latency.MCPDiscovery}}
<h3>MCP discovery (<code>retrieve_tools</code> over the MCP protocol)</h3>
<div>
  <span class="stat"><b>{{f1 .Latency.MCPDiscovery.P50Ms}}</b>p50 ms</span>
  <span class="stat"><b>{{f1 .Latency.MCPDiscovery.P95Ms}}</b>p95 ms</span>
  <span class="stat"><b>{{f1 .Latency.MCPDiscovery.P99Ms}}</b>p99 ms</span>
  <span class="stat"><b>{{f1 .Latency.MCPDiscovery.MaxMs}}</b>max ms</span>
</div>
{{end}}
<p class="small">Client-measured (FR-023): the server-side timing field is a stub. The two surfaces are measured separately and never conflated.</p>
{{end}}

{{if .Lap}}
<h2>LAP independent verdict{{with prov .Provenance "lap"}}<span class="badge badge-{{.}}">{{.}}</span>{{end}}</h2>
{{if .Lap.Executed}}
<p>lap-score <code>{{.Lap.Version}}</code> &middot; grade <b>{{.Lap.Grade}}</b> &middot; LAP menu tokens <b>{{.Lap.MenuTokens}}</b>{{if .Lap.InHouseMenuTokens}} &middot; in-house count <b>{{.Lap.InHouseMenuTokens}}</b> &middot; divergence {{f1 .Lap.DivergencePct}}%{{if gt .Lap.DivergencePct 15.0}} <span class="warn">exceeds &plusmn;15% tolerance</span>{{else if lt .Lap.DivergencePct -15.0}} <span class="warn">exceeds &plusmn;15% tolerance</span>{{end}}{{end}}</p>
{{if .Lap.ArtifactPath}}<p class="small">artifact: <code>{{.Lap.ArtifactPath}}</code></p>{{end}}
{{else}}
<p class="warn">LAP not executed: {{.Lap.SkipReason}}</p>
{{end}}
{{end}}
</body>
</html>
`
