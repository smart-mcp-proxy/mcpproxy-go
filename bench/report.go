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
