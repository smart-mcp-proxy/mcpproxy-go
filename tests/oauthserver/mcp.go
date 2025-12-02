package oauthserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// MCP JSON-RPC types
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// MCP protocol types
type initializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    capabilities `json:"capabilities"`
	ServerInfo      serverInfo   `json:"serverInfo"`
}

type capabilities struct {
	Tools *toolsCapability `json:"tools,omitempty"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type toolsListResult struct {
	Tools []toolInfo `json:"tools"`
}

type toolInfo struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema inputSchema `json:"inputSchema"`
}

type inputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

type callToolResult struct {
	Content []contentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// handleMCP handles the MCP protocol endpoint with OAuth authentication
func (s *OAuthTestServer) handleMCP(w http.ResponseWriter, r *http.Request) {
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

	// Handle MCP request
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req jsonrpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendMCPError(w, nil, -32700, "Parse error", nil)
		return
	}

	// Route to appropriate handler
	switch req.Method {
	case "initialize":
		s.handleMCPInitialize(w, &req)
	case "initialized":
		s.handleMCPInitialized(w, &req)
	case "tools/list":
		s.handleMCPToolsList(w, &req)
	case "tools/call":
		s.handleMCPToolsCall(w, &req)
	default:
		s.sendMCPError(w, req.ID, -32601, "Method not found", nil)
	}
}

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

func (s *OAuthTestServer) sendMCPError(w http.ResponseWriter, id interface{}, code int, message string, data interface{}) {
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *OAuthTestServer) sendMCPResult(w http.ResponseWriter, id interface{}, result interface{}) {
	resp := jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *OAuthTestServer) handleMCPInitialize(w http.ResponseWriter, req *jsonrpcRequest) {
	result := initializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities: capabilities{
			Tools: &toolsCapability{
				ListChanged: false,
			},
		},
		ServerInfo: serverInfo{
			Name:    "oauth-test-mcp-server",
			Version: "1.0.0",
		},
	}
	s.sendMCPResult(w, req.ID, result)
}

func (s *OAuthTestServer) handleMCPInitialized(w http.ResponseWriter, req *jsonrpcRequest) {
	// notifications don't expect a response, but we send one for streamable-http
	s.sendMCPResult(w, req.ID, nil)
}

func (s *OAuthTestServer) handleMCPToolsList(w http.ResponseWriter, req *jsonrpcRequest) {
	result := toolsListResult{
		Tools: []toolInfo{
			{
				Name:        "echo",
				Description: "Echoes back the input message",
				InputSchema: inputSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"message": map[string]interface{}{
							"type":        "string",
							"description": "The message to echo",
						},
					},
					Required: []string{"message"},
				},
			},
			{
				Name:        "get_time",
				Description: "Returns the current server time",
				InputSchema: inputSchema{
					Type:       "object",
					Properties: map[string]interface{}{},
				},
			},
		},
	}
	s.sendMCPResult(w, req.ID, result)
}

func (s *OAuthTestServer) handleMCPToolsCall(w http.ResponseWriter, req *jsonrpcRequest) {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendMCPError(w, req.ID, -32602, "Invalid params", nil)
		return
	}

	var result callToolResult
	switch params.Name {
	case "echo":
		msg, _ := params.Arguments["message"].(string)
		result = callToolResult{
			Content: []contentItem{
				{Type: "text", Text: fmt.Sprintf("Echo: %s", msg)},
			},
		}
	case "get_time":
		result = callToolResult{
			Content: []contentItem{
				{Type: "text", Text: "Current time: 2024-01-01T00:00:00Z (test server)"},
			},
		}
	default:
		s.sendMCPError(w, req.ID, -32602, fmt.Sprintf("Unknown tool: %s", params.Name), nil)
		return
	}

	s.sendMCPResult(w, req.ID, result)
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
