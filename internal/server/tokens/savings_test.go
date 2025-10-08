package tokens

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewSavingsCalculator(t *testing.T) {
	logger := zap.NewNop().Sugar()

	t.Run("creates calculator with valid tokenizer", func(t *testing.T) {
		tokenizer, err := NewTokenizer("cl100k_base", logger, true)
		require.NoError(t, err)

		calc := NewSavingsCalculator(tokenizer, logger, "gpt-4")
		assert.NotNil(t, calc)
	})

	t.Run("creates calculator with nil tokenizer", func(t *testing.T) {
		calc := NewSavingsCalculator(nil, logger, "gpt-4")
		assert.NotNil(t, calc)
	})
}

func TestCalculateProxySavings(t *testing.T) {
	logger := zap.NewNop().Sugar()
	tokenizer, err := NewTokenizer("cl100k_base", logger, true)
	require.NoError(t, err)

	calc := NewSavingsCalculator(tokenizer, logger, "gpt-4")

	t.Run("calculates savings for single server", func(t *testing.T) {
		servers := []ServerToolInfo{
			{
				ServerName: "test-server",
				Tools: []ToolInfo{
					{
						Name:        "tool1",
						Description: "A test tool that does something",
						InputSchema: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"arg1": map[string]interface{}{"type": "string"},
							},
						},
					},
					{
						Name:        "tool2",
						Description: "Another test tool",
						InputSchema: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"arg1": map[string]interface{}{"type": "number"},
								"arg2": map[string]interface{}{"type": "boolean"},
							},
						},
					},
				},
			},
		}

		metrics, err := calc.CalculateProxySavings(servers, 5)
		require.NoError(t, err)
		require.NotNil(t, metrics)

		assert.Greater(t, metrics.TotalServerToolListSize, 0, "should have counted tokens in full tool list")
		assert.Greater(t, metrics.AverageQueryResultSize, 0, "should have typical query result size")
		// With only 2 tools and topK=5, we get all tools, so no savings
		assert.GreaterOrEqual(t, metrics.SavedTokens, 0, "savings should be non-negative")
		assert.GreaterOrEqual(t, metrics.SavedTokensPercentage, 0.0, "percentage should be non-negative")
		assert.Len(t, metrics.PerServerToolListSizes, 1, "should have one server")
		assert.Greater(t, metrics.PerServerToolListSizes["test-server"], 0, "server should have token count")
	})

	t.Run("calculates savings for multiple servers", func(t *testing.T) {
		servers := []ServerToolInfo{
			{
				ServerName: "server1",
				Tools: []ToolInfo{
					{
						Name:        "tool1",
						Description: "Test tool 1",
						InputSchema: map[string]interface{}{"type": "object"},
					},
					{
						Name:        "tool2",
						Description: "Test tool 2",
						InputSchema: map[string]interface{}{"type": "object"},
					},
				},
			},
			{
				ServerName: "server2",
				Tools: []ToolInfo{
					{
						Name:        "tool3",
						Description: "Test tool 3 with a longer description that contains more tokens",
						InputSchema: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"query":  map[string]interface{}{"type": "string"},
								"limit":  map[string]interface{}{"type": "number"},
								"offset": map[string]interface{}{"type": "number"},
							},
						},
					},
				},
			},
		}

		metrics, err := calc.CalculateProxySavings(servers, 2)
		require.NoError(t, err)
		require.NotNil(t, metrics)

		assert.Greater(t, metrics.TotalServerToolListSize, 0)
		assert.Greater(t, metrics.AverageQueryResultSize, 0)
		assert.Greater(t, metrics.SavedTokens, 0)
		assert.Greater(t, metrics.SavedTokensPercentage, 0.0)
		assert.Len(t, metrics.PerServerToolListSizes, 2)
		assert.Greater(t, metrics.PerServerToolListSizes["server1"], 0)
		assert.Greater(t, metrics.PerServerToolListSizes["server2"], 0)
	})

	t.Run("handles empty servers list", func(t *testing.T) {
		servers := []ServerToolInfo{}

		metrics, err := calc.CalculateProxySavings(servers, 5)
		require.NoError(t, err)
		require.NotNil(t, metrics)

		// Empty list still has minimal JSON wrapper tokens
		assert.GreaterOrEqual(t, metrics.TotalServerToolListSize, 0)
		assert.GreaterOrEqual(t, metrics.AverageQueryResultSize, 0)
		assert.GreaterOrEqual(t, metrics.SavedTokens, 0)
		assert.GreaterOrEqual(t, metrics.SavedTokensPercentage, 0.0)
	})

	t.Run("handles server with no tools", func(t *testing.T) {
		servers := []ServerToolInfo{
			{
				ServerName: "empty-server",
				Tools:      []ToolInfo{},
			},
		}

		metrics, err := calc.CalculateProxySavings(servers, 5)
		require.NoError(t, err)
		require.NotNil(t, metrics)

		// Server with no tools still has minimal JSON wrapper tokens
		assert.GreaterOrEqual(t, metrics.TotalServerToolListSize, 0)
		assert.GreaterOrEqual(t, metrics.AverageQueryResultSize, 0)
		assert.GreaterOrEqual(t, metrics.SavedTokens, 0)
		assert.GreaterOrEqual(t, metrics.SavedTokensPercentage, 0.0)
	})

	t.Run("calculates percentage correctly", func(t *testing.T) {
		servers := []ServerToolInfo{
			{
				ServerName: "test-server",
				Tools: []ToolInfo{
					{Name: "tool1", Description: "Tool 1", InputSchema: map[string]interface{}{"type": "object"}},
					{Name: "tool2", Description: "Tool 2", InputSchema: map[string]interface{}{"type": "object"}},
					{Name: "tool3", Description: "Tool 3", InputSchema: map[string]interface{}{"type": "object"}},
					{Name: "tool4", Description: "Tool 4", InputSchema: map[string]interface{}{"type": "object"}},
					{Name: "tool5", Description: "Tool 5", InputSchema: map[string]interface{}{"type": "object"}},
				},
			},
		}

		metrics, err := calc.CalculateProxySavings(servers, 2)
		require.NoError(t, err)

		// With topK=2, we should be returning ~40% of tokens (2/5 tools)
		// So savings should be roughly 60%
		assert.InDelta(t, 60.0, metrics.SavedTokensPercentage, 20.0, "percentage should be reasonable")
		assert.Equal(t, metrics.TotalServerToolListSize-metrics.AverageQueryResultSize, metrics.SavedTokens)
	})
}

func TestSavingsWithDisabledTokenizer(t *testing.T) {
	logger := zap.NewNop().Sugar()
	// Create a disabled tokenizer
	tokenizer, err := NewTokenizer("cl100k_base", logger, false)
	require.NoError(t, err)

	calc := NewSavingsCalculator(tokenizer, logger, "gpt-4")

	servers := []ServerToolInfo{
		{
			ServerName: "test-server",
			Tools: []ToolInfo{
				{Name: "tool1", Description: "Test", InputSchema: map[string]interface{}{"type": "object"}},
			},
		},
	}

	metrics, err := calc.CalculateProxySavings(servers, 5)
	require.NoError(t, err)

	// Disabled tokenizer should return 0 tokens everywhere
	assert.Equal(t, 0, metrics.TotalServerToolListSize)
	assert.Equal(t, 0, metrics.AverageQueryResultSize)
	assert.Equal(t, 0, metrics.SavedTokens)
	assert.Equal(t, 0.0, metrics.SavedTokensPercentage)
}

func TestSavingsWithLargeToolList(t *testing.T) {
	logger := zap.NewNop().Sugar()
	tokenizer, err := NewTokenizer("cl100k_base", logger, true)
	require.NoError(t, err)

	calc := NewSavingsCalculator(tokenizer, logger, "gpt-4")

	// Create a server with many tools
	tools := make([]ToolInfo, 50)
	for i := 0; i < 50; i++ {
		tools[i] = ToolInfo{
			Name:        "tool_" + string(rune(i)),
			Description: "This is a test tool with a moderately long description to simulate real-world usage",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"param1": map[string]interface{}{"type": "string", "description": "A parameter"},
					"param2": map[string]interface{}{"type": "number", "description": "Another parameter"},
					"param3": map[string]interface{}{"type": "boolean", "description": "Yet another parameter"},
				},
			},
		}
	}

	servers := []ServerToolInfo{
		{
			ServerName: "large-server",
			Tools:      tools,
		},
	}

	metrics, err := calc.CalculateProxySavings(servers, 10)
	require.NoError(t, err)

	// With 50 tools and topK=10, savings should be substantial
	assert.Greater(t, metrics.SavedTokens, 1000, "should save significant tokens with large tool list")
	assert.Greater(t, metrics.SavedTokensPercentage, 70.0, "should save >70% with topK=10 out of 50")
	assert.Less(t, metrics.AverageQueryResultSize, metrics.TotalServerToolListSize, "query result should be smaller than full list")
}
