package oauth

import (
	"bytes"
	"html/template"
)

const (
	callbackTitleSuccess = "Authorization Successful"
	callbackTitleFailed  = "Authorization Failed"
)

// callbackLogoSVG is the MCPProxy shield logo (assets/logo.svg with the
// editor metadata stripped), inlined so the page needs no network or asset
// route: the callback listener serves this single response only.
const callbackLogoSVG = template.HTML(`<svg class="logo" viewBox="0 0 120 120" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="MCPProxy logo">` +
	`<defs><radialGradient id="mcpproxy-shield" cx="28.966673" cy="23.640537" fx="28.966673" fy="23.640537" r="31.901502" ` +
	`gradientTransform="matrix(1.556145,2.4018829,-1.531152,0.99201115,13.533268,-94.722392)" gradientUnits="userSpaceOnUse">` +
	`<stop offset="0" stop-color="#ccffdd"/><stop offset="1" stop-color="#006644"/></radialGradient></defs>` +
	`<path d="M 61.076795,-0.12020106 25.669448,12.823274 V 38.71023 c 0,22.651084 14.64979,46.337652 35.407347,51.773913 ` +
	`C 81.834351,85.047882 96.484141,61.361314 96.484141,38.71023 V 12.823274 Z" fill="url(#mcpproxy-shield)"/>` +
	`<g fill="#005533"><ellipse cx="27.218142" cy="99.239571" rx="16.648861" ry="17.65192"/>` +
	`<ellipse cx="60.515858" cy="99.239571" rx="16.648861" ry="17.65192"/>` +
	`<ellipse cx="93.813583" cy="99.239571" rx="16.648861" ry="17.65192"/></g>` +
	`<g fill="#ffffff" font-family="Arial,Helvetica,sans-serif" font-size="17.1431px" font-weight="bold" text-anchor="middle" transform="scale(0.97117223,1.0296835)">` +
	`<text x="28.02607" y="103.23593">M</text><text x="62.31218" y="103.23593">C</text><text x="96.598289" y="103.23593">P</text></g></svg>`)

var callbackPageTemplate = template.Must(template.New("oauth-callback").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · MCPProxy</title>
<style>
:root{--bg:#f4f7f5;--card:#fff;--text:#1f2933;--muted:#5f6b76;--line:#e3e8e5;--brand:#006644;--ok:#0f8a4f;--fail:#c62828}
@media (prefers-color-scheme:dark){:root{--bg:#0f1512;--card:#18201c;--text:#e6ece8;--muted:#9aa7a0;--line:#2a3530;--brand:#4cc38a;--ok:#4cc38a;--fail:#ef6b6b}}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:16px;background:var(--bg);color:var(--text);font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif}
.card{width:100%;max-width:460px;background:var(--card);border:1px solid var(--line);border-top:4px solid var(--ok);border-radius:12px;padding:32px 32px 24px;text-align:center;box-shadow:0 4px 24px rgba(0,0,0,.06)}
.card.fail{border-top-color:var(--fail)}
.brand{display:inline-flex;align-items:center;gap:10px;margin-bottom:20px;color:var(--text);text-decoration:none;font-weight:600;font-size:18px}
.logo{width:44px;height:44px}
h1{font-size:22px;margin:0 0 10px}
.ok h1{color:var(--ok)}
.fail h1{color:var(--fail)}
p{margin:0;line-height:1.55;color:var(--muted);overflow-wrap:anywhere}
nav{margin-top:24px;padding-top:16px;border-top:1px solid var(--line);display:flex;flex-wrap:wrap;justify-content:center;gap:8px 20px;font-size:14px}
nav a{color:var(--brand);text-decoration:none}
nav a:hover{text-decoration:underline}
</style>
</head>
<body>
<main class="card {{if .Success}}ok{{else}}fail{{end}}">
<a class="brand" href="https://mcpproxy.app" target="_blank" rel="noopener noreferrer">{{.Logo}}<span>MCPProxy</span></a>
<h1>{{.Title}}</h1>
<p>{{.Message}}</p>
<nav>
<a href="https://mcpproxy.app" target="_blank" rel="noopener noreferrer">Website</a>
<a href="https://docs.mcpproxy.app/features/oauth-authentication" target="_blank" rel="noopener noreferrer">Documentation</a>
<a href="https://github.com/smart-mcp-proxy/mcpproxy-go" target="_blank" rel="noopener noreferrer">GitHub</a>
</nav>
</main>
</body>
</html>
`))

type callbackPageData struct {
	Title   string
	Message string
	Success bool
	Logo    template.HTML
}

// callbackPage renders the page the user's browser lands on. Success is only
// ever reported when the authorization code was actually delivered to the flow
// that asked for it (issue #975). Title and message are escaped: the failure
// message embeds the provider's error text. The page no longer closes itself:
// it carries the docs and project links, which an auto-close would hide.
func callbackPage(title, message string) string {
	var buf bytes.Buffer
	data := callbackPageData{
		Title:   title,
		Message: message,
		Success: title == callbackTitleSuccess,
		Logo:    callbackLogoSVG,
	}
	if err := callbackPageTemplate.Execute(&buf, data); err != nil {
		// Unreachable with a static template and string fields; never leave
		// the browser on a blank page.
		return "<h1>" + template.HTMLEscapeString(title) + "</h1><p>" + template.HTMLEscapeString(message) + "</p>"
	}
	return buf.String()
}
