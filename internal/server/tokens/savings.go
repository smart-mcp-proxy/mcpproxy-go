package tokens

import (
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
)

// ServerToolInfo represents tool information for a single server
type ServerToolInfo struct {
	ServerName string
	ToolCount  int
	Tools      []ToolInfo
}

// ToolInfo represents a single tool's information
type ToolInfo struct {
	Name        string
	Description string
	InputSchema map[string]interface{}
}

// SavingsCalculator calculates token savings from using MCPProxy
type SavingsCalculator struct {
	tokenizer Tokenizer
	logger    *zap.SugaredLogger
	model     string
}

// TokenSavingsMetrics represents token savings data
type TokenSavingsMetrics struct {
	TotalServerToolListSize int                `json:"total_server_tool_list_size"` // All upstream tools combined (tokens)
	AverageQueryResultSize  int                `json:"average_query_result_size"`   // Typical retrieve_tools output (tokens)
	SavedTokens             int                `json:"saved_tokens"`                 // Difference
	SavedTokensPercentage   float64            `json:"saved_tokens_percentage"`     // Percentage saved
	PerServerToolListSizes  map[string]int     `json:"per_server_tool_list_sizes"`  // Token size per server
}

// NewSavingsCalculator creates a new token savings calculator
func NewSavingsCalculator(tokenizer Tokenizer, logger *zap.SugaredLogger, model string) *SavingsCalculator {
	return &SavingsCalculator{
		tokenizer: tokenizer,
		logger:    logger,
		model:     model,
	}
}

// CalculateProxySavings calculates token savings from using MCPProxy vs listing all tools
func (sc *SavingsCalculator) CalculateProxySavings(
	servers []ServerToolInfo,
	topK int,
) (*TokenSavingsMetrics, error) {
	if sc.tokenizer == nil {
		return nil, fmt.Errorf("tokenizer not available")
	}

	metrics := &TokenSavingsMetrics{
		PerServerToolListSizes: make(map[string]int),
	}

	// Calculate total token size for all upstream tools
	totalTokens := 0
	for _, server := range servers {
		serverTokens, err := sc.calculateServerToolListSize(server)
		if err != nil {
			sc.logger.Warnf("Failed to calculate tokens for server %s: %v", server.ServerName, err)
			continue
		}
		metrics.PerServerToolListSizes[server.ServerName] = serverTokens
		totalTokens += serverTokens
	}
	metrics.TotalServerToolListSize = totalTokens

	// Calculate average query result size (typical retrieve_tools output)
	// This simulates returning topK tools with their full schemas
	avgQuerySize, err := sc.estimateQueryResultSize(servers, topK)
	if err != nil {
		sc.logger.Warnf("Failed to estimate query result size: %v", err)
		avgQuerySize = 0
	}
	metrics.AverageQueryResultSize = avgQuerySize

	// Calculate savings
	if totalTokens > 0 {
		metrics.SavedTokens = totalTokens - avgQuerySize
		if metrics.SavedTokens < 0 {
			metrics.SavedTokens = 0
		}
		metrics.SavedTokensPercentage = float64(metrics.SavedTokens) / float64(totalTokens) * 100.0
	}

	return metrics, nil
}

// calculateServerToolListSize calculates the token size for a server's full tool list
func (sc *SavingsCalculator) calculateServerToolListSize(server ServerToolInfo) (int, error) {
	// Create a representation of the ListTools response
	toolsResponse := make([]map[string]interface{}, 0, len(server.Tools))
	for _, tool := range server.Tools {
		toolData := map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": tool.InputSchema,
		}
		toolsResponse = append(toolsResponse, toolData)
	}

	// Serialize to JSON and count tokens
	jsonData, err := json.Marshal(map[string]interface{}{
		"tools": toolsResponse,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to marshal tools: %w", err)
	}

	tokens, err := sc.tokenizer.CountTokensForModel(string(jsonData), sc.model)
	if err != nil {
		return 0, fmt.Errorf("failed to count tokens: %w", err)
	}

	return tokens, nil
}

// estimateQueryResultSize estimates the token size of a typical retrieve_tools query result
func (sc *SavingsCalculator) estimateQueryResultSize(servers []ServerToolInfo, topK int) (int, error) {
	// Collect all tools across servers
	allTools := []ToolInfo{}
	for _, server := range servers {
		allTools = append(allTools, server.Tools...)
	}

	// If topK is larger than total tools, use all tools
	if topK > len(allTools) {
		topK = len(allTools)
	}

	// Take first topK tools as a sample (in real usage, these would be BM25-ranked)
	sampleTools := allTools[:topK]

	// Create response similar to retrieve_tools output
	toolsResponse := make([]map[string]interface{}, 0, len(sampleTools))
	for _, tool := range sampleTools {
		toolData := map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": tool.InputSchema,
		}
		toolsResponse = append(toolsResponse, toolData)
	}

	// Serialize and count tokens
	jsonData, err := json.Marshal(map[string]interface{}{
		"tools": toolsResponse,
		"query": "example query",
		"total": len(sampleTools),
	})
	if err != nil {
		return 0, fmt.Errorf("failed to marshal query result: %w", err)
	}

	tokens, err := sc.tokenizer.CountTokensForModel(string(jsonData), sc.model)
	if err != nil {
		return 0, fmt.Errorf("failed to count tokens: %w", err)
	}

	return tokens, nil
}

// CalculateToolListTokens calculates tokens for a single server's tool list
func (sc *SavingsCalculator) CalculateToolListTokens(tools []ToolInfo) (int, error) {
	if sc.tokenizer == nil {
		return 0, fmt.Errorf("tokenizer not available")
	}

	server := ServerToolInfo{
		ServerName: "temp",
		ToolCount:  len(tools),
		Tools:      tools,
	}

	return sc.calculateServerToolListSize(server)
}