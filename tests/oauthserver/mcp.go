package oauthserver

import (
	"context"
	"fmt"
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
