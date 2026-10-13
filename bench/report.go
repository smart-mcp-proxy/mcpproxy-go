package bench

import (
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strconv"
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
		// n0 renders a token count at full precision without exponent
		// notation — a headline cost printed as "4.1e+04" is unreadable.
		"n0": func(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) },
		// derefF unwraps an optional figure for display. The TEMPLATE must
		// branch on nil before calling it — a nil figure means "not measured",
		// and printing its zero is the silent-zero defect this guards against.
		"derefF": derefF,
		// costoutcome builds the FR-023 plot. The arithmetic lives in Go
		// (NewCostOutcomeView) so a degenerate axis cannot emit NaN into an
		// SVG attribute, where it would fail silently.
		"costoutcome": NewCostOutcomeView,
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
  /* .hl gives the two FR-018 headline figures — cost and completion —
     IDENTICAL weight and size, in the table and in the plot legend. Equal
     prominence is a layout property, so it is enforced by one shared class
     rather than by two hand-matched declarations that can drift apart. */
  .hl { font-weight: 600; font-size: 1.05rem; }
  .plot { max-width: 100%; height: auto; margin: 1rem 0; }
  .axis { stroke: #8886; stroke-width: 1; }
  .grid { stroke: #8883; stroke-width: 1; stroke-dasharray: 3 3; }
  .axlabel { font: 11px system-ui, sans-serif; fill: currentColor; opacity: .7; }
  .axtitle { font: 12px system-ui, sans-serif; fill: currentColor; opacity: .85; font-weight: 600; }
  .ptlabel { font: 11px system-ui, sans-serif; fill: currentColor; }
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

{{if .Replay}}
<h2>Replay &mdash; a recorded workload, recomputed{{with prov .Provenance "replay"}}<span class="badge badge-{{.}}">{{.}}</span>{{end}}</h2>
<div class="caveat">&#9888;&#65039; <b>{{.Replay.Counterfactual}}</b></div>
<p class="small">accounting source <code>{{.Replay.AccountingSource.Kind}}</code> &middot; <code>{{.Replay.AccountingSource.Identity}}</code> &middot; recorded bodies {{if .Replay.BodiesIncluded}}<span class="warn">ON &mdash; raw and unmasked (the activity export path does not mask)</span>{{else}}off (default posture){{end}} &middot; fleet shape <code>{{.Replay.FleetShape.ID}}</code>, {{.Replay.FleetShape.ToolCount}} tools{{if .Replay.FleetShape.MeanDefinitionTokens}}, mean {{f1 .Replay.FleetShape.MeanDefinitionTokens}} tokens per definition, p95 {{.Replay.FleetShape.P95DefinitionTokens}}{{end}}</p>

<h3>What did not count</h3>
<p><span class="stat"><b>{{.Replay.SessionsSupplied}}</b>sessions supplied</span><span class="stat"><b>{{.Replay.SessionsUsed}}</b>sessions used</span></p>
{{if .Replay.Exclusions}}
<table>
  <thead><tr><th>Reason</th><th>Count</th><th>Effect</th></tr></thead>
  <tbody>
  {{range .Replay.Exclusions}}
    <tr>
      <td><code>{{.Reason}}</code></td>
      <td>{{.Sessions}}</td>
      <td class="l">{{if eq .Reason "sensitive"}}sessions DROPPED &mdash; not priced at all{{else if eq .Reason "unreplayable"}}sessions DROPPED &mdash; a recorded tool is absent from the supplied fleet{{else if eq .Reason "bodies_missing"}}sessions flagged &mdash; still counted for call shape, but no response figure is derivable from them{{else if eq .Reason "truncated"}}sessions flagged &mdash; a stored response was cut, so its text describes more than the agent consumed{{else}}RECORDS dropped before reaching any unit of work (non-call activity, no tool name, or no work session){{end}}</td>
    </tr>
  {{end}}
  </tbody>
</table>
<p class="small">Read this table BEFORE the numbers below. The two DROPPED rows account exactly for sessions supplied minus sessions used; the flagged rows describe sessions that still contributed. The <code>unattributed</code> row counts RECORDS rather than sessions &mdash; those records never became a session, so no session count exists for them.</p>
{{else}}
<p class="small">No sessions were dropped and no records fell outside a unit of work.</p>
{{end}}
{{if .Replay.SensitiveFlagBestEffort}}
<p class="small">The sensitive-data flag is a best-effort REDUCER, never a guarantee: it is derived from detection metadata written asynchronously AFTER a record is persisted, so a freshly exported record may be sensitive and not yet flagged.</p>
{{end}}

<h3>Per-cell cost</h3>
<table>
  <thead><tr><th>Mode cell</th><th>Menu tokens</th><th>Recorded calls</th><th>Response tokens</th><th>Absolute complete-workload cost</th><th>Provenance</th></tr></thead>
  <tbody>
  {{range .Replay.Cells}}
    <tr>
      <td><code>{{.CellID}}</code></td>
      <td>{{.MenuTokens}}</td>
      <td>{{.Calls}}</td>
      <td>{{if .ResponseTokens}}{{.ResponseTokens}}{{else}}&mdash;{{end}}</td>
      <td class="l">{{if .AbsoluteWorkloadWithheld}}<span class="warn">withheld</span> &mdash; {{.WithheldReason}}{{else}}available{{end}}</td>
      <td><span class="badge badge-{{.Provenance}}">{{.Provenance}}</span></td>
    </tr>
  {{end}}
  </tbody>
</table>
{{if .Replay.DirectDelta}}
<h3>Cross-mode delta{{with .Replay.DirectDelta}}<span class="badge badge-{{.Provenance}}">{{.Provenance}}</span>{{end}}</h3>
{{with .Replay.DirectDelta}}
<p><span class="stat"><b>{{.DeltaTokens}}</b>tokens</span><span class="stat"><b>{{f1 .DeltaPct}}%</b>of the <code>{{.FromCellID}}</code> menu</span></p>
<p class="small">This is the honest bodies-off headline: <code>{{.FromCellID}}</code> and <code>{{.ToCellID}}</code> serve IDENTICAL call responses, so the response term cancels out of the difference and the delta survives without the response text. No such delta exists for the retrieve cells &mdash; their serialization changes the response body, which is exactly the text a bodies-off replay does not carry.</p>
{{end}}
{{end}}
<p class="small">Every figure above scores the recorded call shape against the SUPPLIED fleet &mdash; today's fleet, not the fleet as it stood when the sessions were recorded. It is internally valid across mode cells and is not a historical reconstruction. <code>generated_at</code> is pinned rather than stamped so two runs over the same inputs are byte-identical.</p>
{{end}}

{{if .AgentLoop}}
<h2>Cost versus outcome &mdash; which modes are worth their savings{{with prov .Provenance "agent_loop"}}<span class="badge badge-{{.}}">{{.}}</span>{{end}}</h2>
<p class="small">accounting source <code>{{.AgentLoop.AccountingSource.Kind}}</code> &middot; <code>{{.AgentLoop.AccountingSource.Identity}}</code>{{if .AgentLoop.AccountingSource.Model}} &middot; pinned model <code>{{.AgentLoop.AccountingSource.Model}}</code>{{end}}{{if .AgentLoop.Suite}} &middot; suite <code>{{.AgentLoop.Suite}}</code>{{if .AgentLoop.SuiteVersion}} <code>{{.AgentLoop.SuiteVersion}}</code>{{end}}{{end}} &middot; fleet shape <code>{{.AgentLoop.FleetShape.ID}}</code>, {{.AgentLoop.FleetShape.ToolCount}} tools</p>
<div class="caveat">&#9888;&#65039; <b>These figures come from provider-reported usage under the pinned model above.</b> They are NEVER summed with the tokenizer-counted figures elsewhere in this report: a cross-accounting-source aggregate is withheld with a stated reason rather than computed. Read this section against itself, not against the deterministic sections.</div>
<p class="small">The cheapest mode is not automatically the best mode. A mode can be cheap because the agent gave up, so cost is plotted AGAINST the share of tasks completed: <b>up and to the left is better</b> &mdash; more tasks finished, fewer tokens spent per finished task.</p>

{{with costoutcome .AgentLoop}}
{{if .HasPlot}}
<svg class="plot" viewBox="0 0 640 340" role="img" aria-label="Tokens per completed task plotted against completion rate, per mode cell">
  <title>Cost versus outcome per mode cell</title>
  {{range .CompletionTicks}}
  <line class="grid" x1="70" y1="{{f1 .Pos}}" x2="610" y2="{{f1 .Pos}}"></line>
  <text class="axlabel" x="62" y="{{f1 .Pos}}" text-anchor="end" dominant-baseline="middle">{{.Label}}</text>
  {{end}}
  {{range .CostTicks}}
  <text class="axlabel" x="{{f1 .Pos}}" y="308" text-anchor="middle">{{.Label}}</text>
  {{end}}
  <line class="axis" x1="70" y1="290" x2="610" y2="290"></line>
  <line class="axis" x1="70" y1="30" x2="70" y2="290"></line>
  <text class="axtitle" x="340" y="330" text-anchor="middle">Tokens per completed task (axis starts at zero)</text>
  <text class="axtitle" x="16" y="160" text-anchor="middle" transform="rotate(-90 16 160)">Completion rate</text>
  {{range .Points}}
  <circle cx="{{f1 .PlotX}}" cy="{{f1 .PlotY}}" r="7"
          fill="{{if not .Headline}}none{{else if .Regression}}#c0392b{{else}}#1a8f3c{{end}}"
          stroke="{{if .Regression}}#c0392b{{else}}#1a8f3c{{end}}" stroke-width="2"
          stroke-dasharray="{{if .Headline}}0{{else}}3 2{{end}}">
    <title>{{.CellID}}: {{n0 .TokensPerCompletedTask}} tokens per completed task, {{f1 .CompletionRatePct}}% completed, {{.Runs}} run(s), {{.Provenance}}</title>
  </circle>
  <text class="ptlabel" x="{{f1 .PlotX}}" y="{{f1 .PlotY}}" dx="{{f1 .LabelDX}}" dy="4" text-anchor="{{.LabelAnchor}}">{{.CellID}}</text>
  {{end}}
</svg>
<p class="small">Filled marker: a headline-eligible cell (FR-021 &mdash; at least four runs). Hollow dashed marker: measured but provisional, published without headline status. Red: a completion regression, which is never reported as a saving however cheap it is.</p>
{{else}}
<p class="small">No cell carries a plottable figure &mdash; see the exclusions below.</p>
{{end}}
{{if .Excluded}}
<p class="small">Not plotted (no honest coordinates &mdash; a withheld figure is not a zero): {{range .Excluded}}<code>{{.CellID}}</code> &mdash; {{.Reason}}. {{end}}</p>
{{end}}
{{end}}

<table>
  <thead><tr><th>Mode cell</th><th class="hl">Tokens per completed task</th><th class="hl">Completion rate</th><th>First-attempt success</th><th>Corrective retries</th><th>Infra retries</th><th>Runs &amp; spread</th><th>Verdict</th><th>Provenance</th></tr></thead>
  <tbody>
  {{range .AgentLoop.Cells}}
    <tr>
      <td><code>{{.CellID}}</code></td>
      {{if .Withheld}}
      <td colspan="2" class="l warn">withheld &mdash; {{.WithheldReason}}</td>
      {{else}}
      <td class="hl">{{n0 .TokensPerCompletedTask}}</td>
      <td class="hl {{if .Regression}}neg{{end}}">{{f1 .CompletionRatePct}}%</td>
      {{end}}
      {{if .Withheld}}<td class="l">&mdash;</td><td class="l">&mdash;</td><td class="l">&mdash;</td>{{else}}<td>{{if .FirstAttemptSuccessPct}}{{f1 (derefF .FirstAttemptSuccessPct)}}%{{else}}<span class="small">not measured</span>{{end}}</td>
      <td>{{.RetriesCorrective}}</td>
      <td>{{.RetriesInfrastructure}}</td>{{end}}
      <td>{{.Runs}} runs &middot; {{if .SpreadPct}}&plusmn;{{f1 (derefF .SpreadPct)}}%{{else}}<span class="small">spread undefined</span>{{end}}{{if .PartialRuns}}<div class="small">{{.PartialRuns}} partial run(s) excluded</div>{{end}}</td>
      <td class="l">{{if .Withheld}}&mdash;{{else if .Regression}}<span class="warn">REGRESSION</span> &mdash; completes fewer tasks than the baseline; not a saving{{else if not .Headline}}provisional &mdash; {{.Runs}} runs, below the four-run headline bar{{else}}headline{{end}}</td>
      <td>{{if .Withheld}}&mdash;{{else}}<span class="badge badge-{{.Provenance}}">{{.Provenance}}</span>{{end}}</td>
    </tr>
  {{end}}
  </tbody>
</table>
<p class="small">Cost and completion carry equal weight here by construction (FR-018): a cell is only cheaper in a way worth having if its completion rate holds. A cell badged <span class="badge badge-estimated">estimated</span> still rests on an assumed figure rather than a measurement, and is shown beside its measured neighbours rather than hidden behind one section-level badge (FR-013).</p>
{{end}}

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
  <thead><tr><th>Arm</th><th>Calls / session</th><th>Retry rate</th><th>Retry rate source</th><th>Runs</th><th>Estimated session tokens</th><th>Provenance</th></tr></thead>
  <tbody>
  {{range .SessionEstimates}}
    <tr><td><code>{{.Arm}}</code></td><td>{{.CallsPerSession}}</td><td>{{.RetryRate}}</td>
      <td><span class="badge badge-{{.RetryRateProvenance}}">{{.RetryRateProvenance}}</span></td>
      <td>{{if .MeasuredRuns}}{{.MeasuredRuns}}{{else}}&mdash;{{end}}</td>
      <td>{{.EstimatedTokens}}</td>
      <td><span class="badge badge-{{.Provenance}}">{{.Provenance}}</span></td></tr>
  {{end}}
  </tbody>
</table>
<p class="small">session_cost = proxy_menu + calls &times; mean_response(arm) &times; (1 + retry_rate(arm)).
A row badged <span class="badge badge-estimated">estimated</span> uses a literature-derived retry default
(research D8); one badged <span class="badge badge-measured">measured</span> uses an observed rate over the
run count shown. The per-row badge exists because a defaulted 0.0 and a measured 0.0 are the same number
and a different claim (FR-013).</p>
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

// ---------------------------------------------------------------------------
// FR-023 — the cost-versus-outcome view
// ---------------------------------------------------------------------------
//
// A table sorted by token cost answers "which mode is cheapest". That is the
// wrong question, and answering it well is actively misleading: the cheapest
// mode is often cheap because the agent gave up, retried into a dead end, or
// never finished the task at all. FR-023 therefore asks for cost plotted
// AGAINST completion outcome, so a reader sees which modes are worth their
// savings, and FR-018 requires completion rate to sit beside cost at equal
// prominence rather than in a footnote.
//
// Two axis decisions carry the honesty of the picture:
//
//   - The cost axis starts at ZERO. A truncated axis turns a 5% saving into a
//     visually dominant one; on a chart whose whole purpose is judging whether
//     a saving is worth its outcome, that is the failure mode to design out.
//   - The completion axis is FIXED at 0..100%, not scaled to the data. Scaling
//     it would make a 92%-vs-90% difference look like a chasm, and would make
//     two runs of the report incomparable at a glance.
//
// A cell whose figure was withheld (a cross-accounting-source aggregate, above
// all) is NOT plotted. It has no honest coordinates, and plotting it at the
// origin would read as "free, and never completes anything" — a claim nobody
// measured. It is named in an exclusion list beside the plot instead, the same
// silence-is-never-success posture the replay block's exclusion table takes.

// Plot geometry, in the SVG's own viewBox units. Kept as constants so the
// axis mapping used to place points is the same one the gridlines are drawn
// with — a plot whose points and axes disagree is worse than no plot.
const (
	costOutcomePlotLeft   = 70.0
	costOutcomePlotRight  = 610.0
	costOutcomePlotTop    = 30.0
	costOutcomePlotBottom = 290.0
)

// CostOutcomePoint is one mode cell positioned in the cost/outcome plane. It
// carries both the raw figures (so the table and the plot cannot drift apart)
// and the pre-computed SVG coordinates (so the template does no arithmetic).
type CostOutcomePoint struct {
	CellID string
	// TokensPerCompletedTask is the FR-018 cost headline, and
	// CompletionRatePct is its equal-prominence companion.
	TokensPerCompletedTask float64
	CompletionRatePct      float64
	// PlotX/PlotY are viewBox coordinates from the axis mappings below.
	PlotX float64
	PlotY float64
	// Headline is false when the cell has not met the FR-021 bar; such a
	// point is drawn hollow, present but not load-bearing.
	Headline bool
	// Regression marks a cell that completes materially fewer tasks than the
	// baseline. It is drawn in the warning colour and must never be described
	// as a saving (FR-019).
	Regression bool
	// Provenance is the cell's own row provenance (FR-013): a cell still
	// resting on an assumption is badged as such beside its measured
	// neighbours.
	Provenance string
	// Runs and SpreadPct travel with the point so the plot's tooltip can say
	// how much evidence stands behind it.
	Runs      int
	SpreadPct float64
	// LabelAnchor and LabelDX place the cell name clear of its marker. A
	// point in the right half of the plot is labelled to its LEFT, or a long
	// cell name on the expensive end of the axis runs off the viewBox and
	// the reader loses the identity of the most costly cell — the one they
	// most need to identify.
	LabelAnchor string
	LabelDX     float64
}

// CostOutcomeExclusion names a cell that could not be plotted, with the reason
// carried through verbatim. An unexplained absence is indistinguishable from
// an oversight.
type CostOutcomeExclusion struct {
	CellID string
	Reason string
}

// CostOutcomeTick is one labelled axis gridline, positioned in viewBox units.
type CostOutcomeTick struct {
	Label string
	Pos   float64
}

// CostOutcomeView is everything the dashboard needs to draw the FR-023 plot.
// It is built in Go rather than in the template so the axis arithmetic is
// unit-testable and so a degenerate input (one cell, all-zero costs, a nil
// block) cannot produce NaN coordinates inside an SVG attribute, where they
// would fail silently.
type CostOutcomeView struct {
	Points   []CostOutcomePoint
	Excluded []CostOutcomeExclusion
	// CostAxisMaxTokens is the zero-based cost axis maximum: the most
	// expensive plotted cell plus headroom, never less than the data.
	CostAxisMaxTokens float64
	// CompletionAxisMaxPct is fixed at 100 — see the block comment.
	CompletionAxisMaxPct float64
	CostTicks            []CostOutcomeTick
	CompletionTicks      []CostOutcomeTick
}

// PlotX maps a token cost onto the horizontal axis.
func (v CostOutcomeView) PlotX(tokens float64) float64 {
	if v.CostAxisMaxTokens <= 0 {
		return costOutcomePlotLeft
	}
	frac := tokens / v.CostAxisMaxTokens
	return costOutcomePlotLeft + frac*(costOutcomePlotRight-costOutcomePlotLeft)
}

// PlotY maps a completion rate onto the vertical axis (0% at the bottom).
func (v CostOutcomeView) PlotY(pct float64) float64 {
	if v.CompletionAxisMaxPct <= 0 {
		return costOutcomePlotBottom
	}
	frac := pct / v.CompletionAxisMaxPct
	return costOutcomePlotBottom - frac*(costOutcomePlotBottom-costOutcomePlotTop)
}

// HasPlot reports whether there is anything to draw. A block whose every cell
// was withheld still renders its exclusion list, but no empty axes.
func (v CostOutcomeView) HasPlot() bool { return len(v.Points) > 0 }

// NewCostOutcomeView builds the plot for one agent-loop block. A nil block
// yields an empty view rather than a panic, so the template can call this
// unconditionally.
func NewCostOutcomeView(b *AgentLoopBlock) CostOutcomeView {
	view := CostOutcomeView{CompletionAxisMaxPct: 100}
	if b == nil {
		return view
	}

	maxCost := 0.0
	for i := range b.Cells {
		cell := &b.Cells[i]
		if cell.Withheld {
			reason := cell.WithheldReason
			if reason == "" {
				reason = "figure withheld, no reason recorded"
			}
			view.Excluded = append(view.Excluded, CostOutcomeExclusion{CellID: cell.CellID, Reason: reason})
			continue
		}
		if cell.TokensPerCompletedTask > maxCost {
			maxCost = cell.TokensPerCompletedTask
		}
		view.Points = append(view.Points, CostOutcomePoint{
			CellID:                 cell.CellID,
			TokensPerCompletedTask: cell.TokensPerCompletedTask,
			CompletionRatePct:      cell.CompletionRatePct,
			Headline:               cell.Headline,
			Regression:             cell.Regression,
			Provenance:             cell.Provenance,
			Runs:                   cell.Runs,
			SpreadPct:              derefF(cell.SpreadPct),
		})
	}

	// Headroom so the rightmost marker is not clipped by the axis edge. The
	// floor of 1 keeps an all-zero (or single-cell-at-zero) input finite
	// rather than dividing by zero into NaN.
	view.CostAxisMaxTokens = maxCost * 1.1
	if view.CostAxisMaxTokens <= 0 {
		view.CostAxisMaxTokens = 1
	}

	midX := (costOutcomePlotLeft + costOutcomePlotRight) / 2
	for i := range view.Points {
		view.Points[i].PlotX = view.PlotX(view.Points[i].TokensPerCompletedTask)
		view.Points[i].PlotY = view.PlotY(view.Points[i].CompletionRatePct)
		if view.Points[i].PlotX > midX {
			view.Points[i].LabelAnchor, view.Points[i].LabelDX = "end", -12
		} else {
			view.Points[i].LabelAnchor, view.Points[i].LabelDX = "start", 12
		}
	}

	for _, frac := range []float64{0, 0.25, 0.5, 0.75, 1} {
		value := view.CostAxisMaxTokens * frac
		view.CostTicks = append(view.CostTicks, CostOutcomeTick{
			Label: shortTokenLabel(value),
			Pos:   view.PlotX(value),
		})
		pct := view.CompletionAxisMaxPct * frac
		view.CompletionTicks = append(view.CompletionTicks, CostOutcomeTick{
			Label: fmt.Sprintf("%.0f%%", pct),
			Pos:   view.PlotY(pct),
		})
	}
	return view
}

// shortTokenLabel renders an axis label compactly ("41k") without implying
// more precision than an axis tick carries.
func shortTokenLabel(v float64) string {
	switch {
	case v >= 1_000_000:
		return fmt.Sprintf("%.1fM", v/1_000_000)
	case v >= 1000:
		return fmt.Sprintf("%.0fk", v/1000)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

// derefF renders a nil "undefined" figure as zero for PLOTTING only. Callers
// that display a number must branch on nil instead: a plotted point at zero is
// a position, whereas a printed "0.0%" is a claim.
func derefF(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}
