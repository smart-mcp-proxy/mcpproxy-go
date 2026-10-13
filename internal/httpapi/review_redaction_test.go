package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// reviewRedactionController deliberately uses the same real agent-token and
// route harness as the scoped-list tests. The review payload is the canonical
// runtime shape; REST only serializes it, while CLI and MCP consume that same
// composer output (covered by their composer-parity tests).
type reviewRedactionController struct {
	scopeController
	review *internalRuntime.ServerReview
}

func (c *reviewRedactionController) GetReviewQueue(context.Context) (*internalRuntime.ReviewQueue, error) {
	return &internalRuntime.ReviewQueue{Count: 1, Servers: []internalRuntime.ReviewQueueRow{{Server: "alpha", Kind: "server_review", Quarantined: true}}}, nil
}

func (c *reviewRedactionController) GetServerReview(_ context.Context, name string) (*internalRuntime.ServerReview, error) {
	if name != "alpha" {
		return nil, internalRuntime.ErrReviewServerNotFound
	}
	return c.review, nil
}

func TestReviewReadsAlwaysRedactSecretsForAdminAndScopedCallers(t *testing.T) {
	const secret = "secret123"

	for _, tc := range []struct {
		name   string
		reveal bool
		caller string
	}{
		{name: "admin default", caller: scopeAdminAPIKey},
		{name: "scoped agent", caller: "agent"},
		{name: "admin opt-out affects list only", reveal: true, caller: scopeAdminAPIKey},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := scopeFixtureConfig(tc.reveal)
			cfg.Servers = nil
			server := contracts.Server{
				ID: "alpha", Name: "alpha", Protocol: "stdio", Quarantined: true,
				URL:     "https://example.test/mcp?api_key=" + secret,
				Command: "server --token " + secret,
				Args:    []string{"--token", secret},
				Env:     map[string]string{"TOKEN": secret},
				Headers: map[string]string{"Authorization": "Bearer " + secret},
			}
			redacted := server
			oauth.RedactServerSecretFields(&redacted)
			ctrl := &reviewRedactionController{
				scopeController: scopeController{cfg: cfg, servers: []contracts.Server{server}},
				review: &internalRuntime.ServerReview{Server: internalRuntime.ReviewServer{
					Name: redacted.Name, Transport: "stdio", URL: redacted.URL,
					Command: redacted.Command, Args: redacted.Args, Env: redacted.Env,
					Headers: redacted.Headers, Quarantined: true,
				}, Tools: []internalRuntime.ReviewTool{}},
			}
			srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})
			caller := tc.caller
			if caller == "agent" {
				caller = token
			}

			list := scopeGet(t, srv, "/api/v1/servers", caller)
			require.Equal(t, http.StatusOK, list.Code, list.Body.String())
			review := scopeGet(t, srv, "/api/v1/servers/alpha/review", caller)
			require.Equal(t, http.StatusOK, review.Code, review.Body.String())
			queue := scopeGet(t, srv, "/api/v1/review", caller)
			require.Equal(t, http.StatusOK, queue.Code, queue.Body.String())

			require.NotContains(t, review.Body.String(), secret)
			require.NotContains(t, queue.Body.String(), secret)
			var listEnvelope struct {
				Data struct {
					Servers []contracts.Server `json:"servers"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(list.Body.Bytes(), &listEnvelope))
			require.Len(t, listEnvelope.Data.Servers, 1)
			if tc.reveal && caller == scopeAdminAPIKey {
				require.Contains(t, list.Body.String(), secret, "admin list opt-out is intentional")
			} else {
				require.NotContains(t, list.Body.String(), secret)
				require.Equal(t, redacted.URL, listEnvelope.Data.Servers[0].URL)
				require.Equal(t, redacted.Command, listEnvelope.Data.Servers[0].Command)
				require.Equal(t, redacted.Args, listEnvelope.Data.Servers[0].Args)
				require.Equal(t, redacted.Env, listEnvelope.Data.Servers[0].Env)
				require.Equal(t, redacted.Headers, listEnvelope.Data.Servers[0].Headers)
			}
		})
	}
}
