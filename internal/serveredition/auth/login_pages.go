//go:build server

package auth

// The two login outcome pages of Spec 107 FR-024 / contracts/rest-endpoints.md
// §10: ONE generic 403 page for every refusal reason and ONE 503 page for
// every unavailability. Each carries only the request id, so an operator can
// find the closed reason in the server log (and, in PR-D, the auth_event
// line) while the page discloses nothing about why — not the reason, not the
// IdP's error text, never a claim value or a token.

import (
	"html/template"
	"net/http"
)

const (
	loginRefusedTitle     = "Sign-in was not permitted"
	loginUnavailableTitle = "Sign-in is temporarily unavailable"
)

var loginPageTemplate = template.Must(template.New("login-page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:#f6f7f9;color:#1f2933;margin:0;display:flex;min-height:100vh;align-items:center;justify-content:center}
main{background:#fff;border-radius:8px;box-shadow:0 1px 3px rgba(0,0,0,.12);padding:32px 40px;max-width:480px;width:calc(100% - 32px);box-sizing:border-box}
h1{font-size:20px;margin:0 0 12px}
p{margin:0 0 12px;line-height:1.5}
code{background:#eef0f3;border-radius:4px;padding:2px 6px;font-size:13px}
a{color:#2563eb}
</style>
</head>
<body>
<main>
<h1>{{.Title}}</h1>
<p>{{.Body}}</p>
<p>Reference: <code>ref {{.RequestID}}</code></p>
<p><a href="/api/v1/auth/login">Try again</a></p>
</main>
</body>
</html>
`))

type loginPageData struct {
	Title     string
	Body      string
	RequestID string
}

// writeLoginRefusedPage renders the generic 403 page.
func writeLoginRefusedPage(w http.ResponseWriter, requestID string) {
	writeLoginPage(w, http.StatusForbidden, loginPageData{
		Title:     loginRefusedTitle,
		Body:      "Your sign-in request could not be completed. If you believe this is a mistake, contact your administrator and quote the reference below.",
		RequestID: requestID,
	})
}

// writeLoginUnavailablePage renders the 503 page.
func writeLoginUnavailablePage(w http.ResponseWriter, requestID string) {
	writeLoginPage(w, http.StatusServiceUnavailable, loginPageData{
		Title:     loginUnavailableTitle,
		Body:      "Sign-in cannot be completed right now. Please try again in a few minutes; if the problem persists, contact your administrator and quote the reference below.",
		RequestID: requestID,
	})
}

func writeLoginPage(w http.ResponseWriter, status int, data loginPageData) {
	if data.RequestID == "" {
		data.RequestID = "-"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = loginPageTemplate.Execute(w, data)
}
