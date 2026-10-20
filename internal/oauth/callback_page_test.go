package oauth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The browser page shown after a sign-in carries the MCPProxy brand: logo,
// links to the website, the docs and the source repository.
func TestCallbackPage_Branding(t *testing.T) {
	for _, page := range []string{
		callbackPage(callbackTitleSuccess, "You can now close this window."),
		callbackPage(callbackTitleFailed, "Something went wrong."),
	} {
		assert.Contains(t, page, "<!doctype html>")
		assert.Contains(t, page, `<meta charset="utf-8">`)
		assert.Contains(t, page, "<svg", "inline MCPProxy logo")
		assert.Contains(t, page, `href="https://mcpproxy.app"`)
		assert.Contains(t, page, `href="https://docs.mcpproxy.app/features/oauth-authentication"`)
		assert.Contains(t, page, `href="https://github.com/smart-mcp-proxy/mcpproxy-go"`)
		assert.Contains(t, page, `rel="noopener noreferrer"`)
	}
}

func TestCallbackPage_OutcomeVariants(t *testing.T) {
	success := callbackPage(callbackTitleSuccess, "done")
	assert.Contains(t, success, "<h1>Authorization Successful</h1>")
	assert.Contains(t, success, `class="card ok"`)
	assert.NotContains(t, success, "window.close()", "the page stays open so its links are usable")

	failed := callbackPage(callbackTitleFailed, "nope")
	assert.Contains(t, failed, "<h1>Authorization Failed</h1>")
	assert.Contains(t, failed, `class="card fail"`)
	assert.NotContains(t, failed, "window.close()", "a failure page must stay open")
}

// Title and message carry upstream-controlled text (the provider's error and
// error_description), so they are escaped.
func TestCallbackPage_EscapesUntrustedText(t *testing.T) {
	page := callbackPage(callbackTitleFailed, `rejected (<script>alert(1)</script>)`)
	assert.NotContains(t, page, "<script>alert(1)</script>")
	assert.True(t, strings.Contains(page, "&lt;script&gt;alert(1)&lt;/script&gt;"))
}
