package index

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
)

func TestBleveIndex_IndexAndSearch_DeFiLlamaTools(t *testing.T) {
	// Create test DeFiLlama tools based on the user's data
	defiLlamaTools := createTestDeFiLlamaTools()

	tests := []struct {
		name          string
		tools         []*config.ToolMetadata
		searchQuery   string
		expectedCount int
		expectedTools []string
	}{
		{
			name:          "Search for DeFiLlama TVL tools",
			tools:         defiLlamaTools,
			searchQuery:   "DeFiLlama chains TVL historical data",
			expectedCount: 5, // Should find multiple TVL-related tools
			expectedTools: []string{
				"defillama:List_all_protocols_on_defillama_along_with_their_tvl",
				"defillama:Get_historical_TVL_of_a_protocol_and_breakdowns_by_token",
				"defillama:Get_historical_TVL_excludes_liquid_staking_and_double_co",
				"defillama:Get_historical_TVL_excludes_liquid_staking_and_double_co_2",
				"defillama:Get_current_TVL_of_all_chains",
			},
		},
		{
			name:          "Search for protocol TVL",
			tools:         defiLlamaTools,
			searchQuery:   "protocol TVL",
			expectedCount: 3,
			expectedTools: []string{
				"defillama:Get_historical_TVL_of_a_protocol_and_breakdowns_by_token",
				"defillama:Simplified_endpoint_to_get_current_TVL_of_a_protocol",
				"defillama:List_all_protocols_on_defillama_along_with_their_tvl",
			},
		},
		{
			name:          "Search for historical data",
			tools:         defiLlamaTools,
			searchQuery:   "historical",
			expectedCount: 6, // Updated to match actual results - we have 6 tools with "historical"
			expectedTools: []string{
				"defillama:Get_historical_TVL_of_a_protocol_and_breakdowns_by_token",
				"defillama:Get_historical_TVL_excludes_liquid_staking_and_double_co",
				"defillama:Get_historical_TVL_excludes_liquid_staking_and_double_co_2",
				"defillama:Get_historical_prices_of_tokens_by_contract_address",
				"defillama:Get_historical_mcap_sum_of_all_stablecoins",
				"defillama:Get_historical_mcap_sum_of_all_stablecoins_in_a_chain",
			},
		},
		{
			name:          "Search for exact tool name",
			tools:         defiLlamaTools,
			searchQuery:   "List_all_protocols_on_defillama_along_with_their_tvl",
			expectedCount: 1,
			expectedTools: []string{
				"defillama:List_all_protocols_on_defillama_along_with_their_tvl",
			},
		},
		{
			name:          "Search for stablecoins",
			tools:         defiLlamaTools,
			searchQuery:   "stablecoins",
			expectedCount: 4,
			expectedTools: []string{
				"defillama:List_all_stablecoins_along_with_their_circulating_amount",
				"defillama:Get_historical_mcap_sum_of_all_stablecoins",
				"defillama:Get_historical_mcap_sum_of_all_stablecoins_in_a_chain",
				"defillama:Get_current_mcap_sum_of_all_stablecoins_on_each_chain",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary directory for test index
			tmpDir, err := os.MkdirTemp("", "bleve_test_*")
			require.NoError(t, err)
			defer os.RemoveAll(tmpDir)

			// Create logger
			logger := zap.NewNop()

			// Create index
			bleveIndex, err := NewBleveIndex(tmpDir, logger)
			require.NoError(t, err)
			defer bleveIndex.Close()

			// Index tools
			err = bleveIndex.BatchIndex(tt.tools)
			require.NoError(t, err)

			// Verify indexing worked
			docCount, err := bleveIndex.GetDocumentCount()
			require.NoError(t, err)
			assert.Equal(t, uint64(len(tt.tools)), docCount)

			// Search for tools
			results, err := bleveIndex.SearchTools(tt.searchQuery, 20)
			require.NoError(t, err)

			// Verify we found the expected number of results
			if tt.expectedCount > 0 {
				assert.GreaterOrEqual(t, len(results), tt.expectedCount,
					"Should find at least %d results for query '%s', but found %d",
					tt.expectedCount, tt.searchQuery, len(results))
			}

			// Verify specific tools are found
			foundTools := make(map[string]bool)
			for _, result := range results {
				foundTools[result.Tool.Name] = true
				t.Logf("Found tool: %s (score: %.2f)", result.Tool.Name, result.Score)
			}

			for _, expectedTool := range tt.expectedTools {
				assert.True(t, foundTools[expectedTool],
					"Expected tool '%s' not found in search results", expectedTool)
			}
		})
	}
}

func TestBleveIndex_SearchTokenization(t *testing.T) {
	// Test different tokenization scenarios
	tools := []*config.ToolMetadata{
		{
			Name:        "defillama:Get_current_TVL_of_all_chains",
			ServerName:  "defillama",
			Description: "Get current TVL of all chains",
			ParamsJSON:  `{"type":"object","properties":{},"required":[]}`,
			Hash:        "test_hash_1",
		},
		{
			Name:        "defillama:List_all_protocols_on_defillama_along_with_their_tvl",
			ServerName:  "defillama",
			Description: "List all protocols on defillama along with their tvl",
			ParamsJSON:  `{"type":"object","properties":{},"required":[]}`,
			Hash:        "test_hash_2",
		},
	}

	tmpDir, err := os.MkdirTemp("", "bleve_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	logger := zap.NewNop()
	bleveIndex, err := NewBleveIndex(tmpDir, logger)
	require.NoError(t, err)
	defer bleveIndex.Close()

	err = bleveIndex.BatchIndex(tools)
	require.NoError(t, err)

	tests := []struct {
		name        string
		query       string
		shouldFind  []string
		description string
	}{
		{
			name:        "Underscore tokenization",
			query:       "Get_current_TVL",
			shouldFind:  []string{"defillama:Get_current_TVL_of_all_chains"},
			description: "Should handle underscores in tool names",
		},
		{
			name:        "Partial word match",
			query:       "TVL",
			shouldFind:  []string{"defillama:Get_current_TVL_of_all_chains", "defillama:List_all_protocols_on_defillama_along_with_their_tvl"},
			description: "Should find tools containing TVL",
		},
		{
			name:        "Multi-word query",
			query:       "current TVL chains",
			shouldFind:  []string{"defillama:Get_current_TVL_of_all_chains"},
			description: "Should handle multi-word queries",
		},
		{
			name:        "Case insensitive",
			query:       "tvl",
			shouldFind:  []string{"defillama:Get_current_TVL_of_all_chains", "defillama:List_all_protocols_on_defillama_along_with_their_tvl"},
			description: "Should be case insensitive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := bleveIndex.SearchTools(tt.query, 20)
			require.NoError(t, err)

			foundTools := make(map[string]bool)
			for _, result := range results {
				foundTools[result.Tool.Name] = true
				t.Logf("Query '%s' found: %s (score: %.2f)", tt.query, result.Tool.Name, result.Score)
			}

			for _, expectedTool := range tt.shouldFind {
				assert.True(t, foundTools[expectedTool],
					"Query '%s' should find tool '%s' - %s", tt.query, expectedTool, tt.description)
			}
		})
	}
}

func TestBleveIndex_FieldMapping(t *testing.T) {
	// Test that all fields are properly indexed and searchable
	tool := &config.ToolMetadata{
		Name:        "defillama:Test_Tool_Name",
		ServerName:  "defillama",
		Description: "This is a test description with keywords like TVL and protocols",
		ParamsJSON:  `{"type":"object","properties":{"chain":{"type":"string","description":"chain slug"}}}`,
		Hash:        "test_hash",
	}

	tmpDir, err := os.MkdirTemp("", "bleve_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	logger := zap.NewNop()
	bleveIndex, err := NewBleveIndex(tmpDir, logger)
	require.NoError(t, err)
	defer bleveIndex.Close()

	err = bleveIndex.IndexTool(tool)
	require.NoError(t, err)

	tests := []struct {
		name        string
		query       string
		shouldFind  bool
		description string
	}{
		{
			name:        "Search by tool name",
			query:       "Test_Tool_Name",
			shouldFind:  true,
			description: "Should find tool by exact name",
		},
		{
			name:        "Search by server name",
			query:       "defillama",
			shouldFind:  true,
			description: "Should find tool by server name",
		},
		{
			name:        "Search by description",
			query:       "TVL protocols",
			shouldFind:  true,
			description: "Should find tool by description content",
		},
		{
			name:        "Search by params content",
			query:       "chain slug",
			shouldFind:  true,
			description: "Should find tool by parameters description",
		},
		{
			name:        "Search by partial name",
			query:       "Test_Tool",
			shouldFind:  true,
			description: "Should find tool by partial name match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := bleveIndex.SearchTools(tt.query, 10)
			require.NoError(t, err)

			found := len(results) > 0
			if tt.shouldFind {
				assert.True(t, found, "Query '%s' should find the tool - %s", tt.query, tt.description)
				if found {
					t.Logf("Query '%s' found: %s (score: %.2f)", tt.query, results[0].Tool.Name, results[0].Score)
				}
			} else {
				assert.False(t, found, "Query '%s' should not find the tool - %s", tt.query, tt.description)
			}
		})
	}
}

func TestBleveIndex_EmptyAndErrorCases(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bleve_test_*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	logger := zap.NewNop()
	bleveIndex, err := NewBleveIndex(tmpDir, logger)
	require.NoError(t, err)
	defer bleveIndex.Close()

	// Test empty query
	results, err := bleveIndex.SearchTools("", 10)
	assert.Error(t, err)
	assert.Nil(t, results)

	// Test search on empty index
	results, err = bleveIndex.SearchTools("test", 10)
	require.NoError(t, err)
	assert.Empty(t, results)

	// Test document count on empty index
	count, err := bleveIndex.GetDocumentCount()
	require.NoError(t, err)
	assert.Equal(t, uint64(0), count)
}

// Helper function to create test DeFiLlama tools based on user's data
func createTestDeFiLlamaTools() []*config.ToolMetadata {
	tools := []*config.ToolMetadata{
		{
			Name:        "defillama:List_all_protocols_on_defillama_along_with_their_tvl",
			ServerName:  "defillama",
			Description: "List all protocols on defillama along with their tvl",
			ParamsJSON:  `{"type":"object","properties":{},"required":[]}`,
			Hash:        "hash1",
			Created:     time.Now(),
			Updated:     time.Now(),
		},
		{
			Name:        "defillama:Get_historical_TVL_of_a_protocol_and_breakdowns_by_token",
			ServerName:  "defillama",
			Description: "Get historical TVL of a protocol and breakdowns by token and chain",
			ParamsJSON:  `{"type":"object","properties":{"protocol":{"type":"string","description":"protocol slug"}},"required":["protocol"]}`,
			Hash:        "hash2",
			Created:     time.Now(),
			Updated:     time.Now(),
		},
		{
			Name:        "defillama:Get_historical_TVL_excludes_liquid_staking_and_double_co",
			ServerName:  "defillama",
			Description: "Get historical TVL (excludes liquid staking and double counted tvl) of DeFi on all chains",
			ParamsJSON:  `{"type":"object","properties":{},"required":[]}`,
			Hash:        "hash3",
			Created:     time.Now(),
			Updated:     time.Now(),
		},
		{
			Name:        "defillama:Get_historical_TVL_excludes_liquid_staking_and_double_co_2",
			ServerName:  "defillama",
			Description: "Get historical TVL (excludes liquid staking and double counted tvl) of a chain",
			ParamsJSON:  `{"type":"object","properties":{"chain":{"type":"string","description":"chain slug"}},"required":["chain"]}`,
			Hash:        "hash4",
			Created:     time.Now(),
			Updated:     time.Now(),
		},
		{
			Name:        "defillama:Simplified_endpoint_to_get_current_TVL_of_a_protocol",
			ServerName:  "defillama",
			Description: "Simplified endpoint that only returns a number, the current TVL of a protocol",
			ParamsJSON:  `{"type":"object","properties":{"protocol":{"type":"string","description":"protocol slug"}},"required":["protocol"]}`,
			Hash:        "hash5",
			Created:     time.Now(),
			Updated:     time.Now(),
		},
		{
			Name:        "defillama:Get_current_TVL_of_all_chains",
			ServerName:  "defillama",
			Description: "Get current TVL of all chains",
			ParamsJSON:  `{"type":"object","properties":{},"required":[]}`,
			Hash:        "hash6",
			Created:     time.Now(),
			Updated:     time.Now(),
		},
		{
			Name:        "defillama:Get_current_prices_of_tokens_by_contract_address",
			ServerName:  "defillama",
			Description: "Get current prices of tokens by contract address. Supports pricing of exotic tokens through multiple methods including bridged tokens, LP tokens, and custom adapters.",
			ParamsJSON:  `{"type":"object","properties":{"coins":{"type":"string","description":"set of comma-separated tokens"}},"required":["coins"]}`,
			Hash:        "hash7",
			Created:     time.Now(),
			Updated:     time.Now(),
		},
		{
			Name:        "defillama:Get_historical_prices_of_tokens_by_contract_address",
			ServerName:  "defillama",
			Description: "Get historical prices of tokens by contract address at specific timestamp",
			ParamsJSON:  `{"type":"object","properties":{"coins":{"type":"string"},"timestamp":{"type":"number"}},"required":["coins","timestamp"]}`,
			Hash:        "hash8",
			Created:     time.Now(),
			Updated:     time.Now(),
		},
		{
			Name:        "defillama:List_all_stablecoins_along_with_their_circulating_amount",
			ServerName:  "defillama",
			Description: "List all stablecoins along with their circulating amounts",
			ParamsJSON:  `{"type":"object","properties":{"includePrices":{"type":"boolean"}},"required":[]}`,
			Hash:        "hash9",
			Created:     time.Now(),
			Updated:     time.Now(),
		},
		{
			Name:        "defillama:Get_historical_mcap_sum_of_all_stablecoins",
			ServerName:  "defillama",
			Description: "Get historical mcap sum of all stablecoins",
			ParamsJSON:  `{"type":"object","properties":{"stablecoin":{"type":"integer"}},"required":[]}`,
			Hash:        "hash10",
			Created:     time.Now(),
			Updated:     time.Now(),
		},
		{
			Name:        "defillama:Get_historical_mcap_sum_of_all_stablecoins_in_a_chain",
			ServerName:  "defillama",
			Description: "Get historical mcap sum of all stablecoins in a chain",
			ParamsJSON:  `{"type":"object","properties":{"chain":{"type":"string"},"stablecoin":{"type":"integer"}},"required":["chain"]}`,
			Hash:        "hash11",
			Created:     time.Now(),
			Updated:     time.Now(),
		},
		{
			Name:        "defillama:Get_current_mcap_sum_of_all_stablecoins_on_each_chain",
			ServerName:  "defillama",
			Description: "Get current mcap sum of all stablecoins on each chain",
			ParamsJSON:  `{"type":"object","properties":{},"required":[]}`,
			Hash:        "hash12",
			Created:     time.Now(),
			Updated:     time.Now(),
		},
	}

	return tools
}
