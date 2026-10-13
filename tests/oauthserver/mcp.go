package oauthserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// createMCPServer creates and configures the MCP server with tools
func (s *OAuthTestServer) createMCPServer() *mcpserver.MCPServer {
	mcpSrv := mcpserver.NewMCPServer(
		"oauth-test-mcp-server",
		"1.0.0",
		mcpserver.WithToolCapabilities(false),
	)

	// Register echo tool
	mcpSrv.AddTool(
		mcp.NewTool("echo",
			mcp.WithDescription("Echoes back the input message"),
			mcp.WithString("message",
				mcp.Required(),
				mcp.Description("The message to echo"),
			),
			// Read-only annotations: without them the MCP-spec default is
			// destructiveHint=true and mcpproxy's intent validation (Spec 018)
			// rejects the tool through call_tool_read (Spec 081 gate uses the
			// read path).
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, ok := request.Params.Arguments.(map[string]interface{})
			if !ok {
				return mcp.NewToolResultError("invalid arguments"), nil
			}
			msg, _ := args["message"].(string)
			return mcp.NewToolResultText(fmt.Sprintf("Echo: %s", msg)), nil
		},
	)

	// Register get_time tool
	mcpSrv.AddTool(
		mcp.NewTool("get_time",
			mcp.WithDescription("Returns the current server time"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithOpenWorldHintAnnotation(false),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(fmt.Sprintf("Current time: %s", time.Now().Format(time.RFC3339))), nil
		},
	)

	return mcpSrv
}

// oauthMiddleware wraps the MCP handler with OAuth authentication
func (s *OAuthTestServer) oauthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for rate limit injection BEFORE auth check
		if s.options.ErrorMode.MCPRateLimitCount > 0 {
			s.mu.Lock()
			s.mcpRateLimitHits++
			hits := s.mcpRateLimitHits
			s.mu.Unlock()

			if hits <= s.options.ErrorMode.MCPRateLimitCount {
				s.sendMCPRateLimited(w)
				return
			}
		}

		// Per-method authorisation (GH #1271): let the handshake through
		// anonymously so only tools/call trips the 401, like Google's ESF
		// frontend does for the Gmail MCP endpoint.
		if s.options.MCPPerMethodAuth && s.anonymousMCPMethod(r) {
			next.ServeHTTP(w, r)
			return
		}

		// Check for Bearer token authentication
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			s.sendMCPUnauthorized(w)
			return
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

		// Validate the JWT token
		if !s.validateAccessToken(tokenStr) {
			s.sendMCPUnauthorized(w)
			return
		}

		// Token is valid, proceed to MCP handler
		next.ServeHTTP(w, r)
	})
}

// anonymousMCPMethod reports whether the JSON-RPC request in r is one the
// per-method-auth mode serves without a token. It peeks at the body and puts
// it back so the MCP handler still sees it. Anything it cannot parse is
// treated as protected.
func (s *OAuthTestServer) anonymousMCPMethod(r *http.Request) bool {
	// The SSE stream itself (GET /sse) is part of the handshake.
	if r.Method == http.MethodGet {
		return true
	}
	if r.Method != http.MethodPost || r.Body == nil {
		return false
	}
	body, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return false
	}
	var rpc struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(body, &rpc) != nil {
		return false
	}
	switch {
	case rpc.Method == "initialize", rpc.Method == "ping", rpc.Method == "tools/list":
		return true
	case strings.HasPrefix(rpc.Method, "notifications/"):
		return true
	}
	return false
}

// validateAccessToken validates a JWT access token
func (s *OAuthTestServer) validateAccessToken(tokenStr string) bool {
	s.mu.RLock()
	keyRing := s.keyRing
	s.mu.RUnlock()

	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		// Verify signing method
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		// Return public key for verification
		return keyRing.GetPublicKey(), nil
	})

	if err != nil {
		return false
	}

	return token.Valid
}

// sendMCPUnauthorized sends a 401 response with WWW-Authenticate header
func (s *OAuthTestServer) sendMCPUnauthorized(w http.ResponseWriter) {
	// Build WWW-Authenticate header per RFC 9728
	wwwAuth := fmt.Sprintf(
		`Bearer realm="mcp-test", authorization_uri="%s/authorize", resource_metadata="%s/.well-known/oauth-protected-resource"`,
		s.issuerURL,
		s.issuerURL,
	)
	w.Header().Set("WWW-Authenticate", wwwAuth)
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"error": "unauthorized", "error_description": "Bearer token required"}`))
}

// sendMCPRateLimited sends a 429 response for rate limit testing
func (s *OAuthTestServer) sendMCPRateLimited(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")

	if s.options.ErrorMode.MCPRateLimitRetryAfter > 0 && !s.options.ErrorMode.MCPRateLimitUseResetAt {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", s.options.ErrorMode.MCPRateLimitRetryAfter))
	}

	w.WriteHeader(http.StatusTooManyRequests)

	if s.options.ErrorMode.MCPRateLimitUseResetAt {
		resetAt := time.Now().Add(time.Duration(s.options.ErrorMode.MCPRateLimitRetryAfter) * time.Second).Unix()
		fmt.Fprintf(w, `{"error": "rate_limited", "reset_at": %d}`, resetAt)
	} else {
		w.Write([]byte(`{"error": "rate_limited", "error_description": "Too many requests"}`))
	}
}
