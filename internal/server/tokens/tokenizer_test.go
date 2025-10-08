package tokens

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewTokenizer(t *testing.T) {
	logger := zap.NewNop().Sugar()

	t.Run("default encoding", func(t *testing.T) {
		tokenizer, err := NewTokenizer("", logger, true)
		require.NoError(t, err)
		assert.Equal(t, DefaultEncoding, tokenizer.GetDefaultEncoding())
		assert.True(t, tokenizer.IsEnabled())
	})

	t.Run("custom encoding", func(t *testing.T) {
		tokenizer, err := NewTokenizer("o200k_base", logger, true)
		require.NoError(t, err)
		assert.Equal(t, "o200k_base", tokenizer.GetDefaultEncoding())
	})

	t.Run("invalid encoding", func(t *testing.T) {
		_, err := NewTokenizer("invalid_encoding", logger, true)
		assert.Error(t, err)
	})

	t.Run("disabled tokenizer", func(t *testing.T) {
		tokenizer, err := NewTokenizer("", logger, false)
		require.NoError(t, err)
		assert.False(t, tokenizer.IsEnabled())
	})
}

func TestCountTokens(t *testing.T) {
	logger := zap.NewNop().Sugar()
	tokenizer, err := NewTokenizer("cl100k_base", logger, true)
	require.NoError(t, err)

	t.Run("simple text", func(t *testing.T) {
		text := "Hello, world!"
		count, err := tokenizer.CountTokens(text)
		require.NoError(t, err)
		assert.Greater(t, count, 0)
		assert.Less(t, count, 10) // Should be around 3-4 tokens
	})

	t.Run("empty text", func(t *testing.T) {
		count, err := tokenizer.CountTokens("")
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("long text", func(t *testing.T) {
		text := "This is a longer piece of text that should result in more tokens being counted. " +
			"The tokenizer should handle this without any issues and return an accurate count."
		count, err := tokenizer.CountTokens(text)
		require.NoError(t, err)
		assert.Greater(t, count, 20)
	})

	t.Run("disabled tokenizer returns zero", func(t *testing.T) {
		disabledTokenizer, err := NewTokenizer("", logger, false)
		require.NoError(t, err)

		count, err := disabledTokenizer.CountTokens("Hello, world!")
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})
}

func TestCountTokensForModel(t *testing.T) {
	logger := zap.NewNop().Sugar()
	tokenizer, err := NewTokenizer("cl100k_base", logger, true)
	require.NoError(t, err)

	text := "Hello, world! How are you today?"

	t.Run("gpt-4", func(t *testing.T) {
		count, err := tokenizer.CountTokensForModel(text, "gpt-4")
		require.NoError(t, err)
		assert.Greater(t, count, 0)
	})

	t.Run("gpt-4o uses o200k_base", func(t *testing.T) {
		count, err := tokenizer.CountTokensForModel(text, "gpt-4o")
		require.NoError(t, err)
		assert.Greater(t, count, 0)
	})

	t.Run("claude model approximation", func(t *testing.T) {
		count, err := tokenizer.CountTokensForModel(text, "claude-3-5-sonnet")
		require.NoError(t, err)
		assert.Greater(t, count, 0)
		// Should use cl100k_base as approximation
	})

	t.Run("unknown model uses default", func(t *testing.T) {
		count, err := tokenizer.CountTokensForModel(text, "unknown-model-xyz")
		require.NoError(t, err)
		assert.Greater(t, count, 0)
	})
}

func TestCountTokensForEncoding(t *testing.T) {
	logger := zap.NewNop().Sugar()
	tokenizer, err := NewTokenizer("cl100k_base", logger, true)
	require.NoError(t, err)

	text := "Hello, world!"

	t.Run("cl100k_base encoding", func(t *testing.T) {
		count, err := tokenizer.CountTokensForEncoding(text, "cl100k_base")
		require.NoError(t, err)
		assert.Greater(t, count, 0)
	})

	t.Run("o200k_base encoding", func(t *testing.T) {
		count, err := tokenizer.CountTokensForEncoding(text, "o200k_base")
		require.NoError(t, err)
		assert.Greater(t, count, 0)
	})

	t.Run("p50k_base encoding", func(t *testing.T) {
		count, err := tokenizer.CountTokensForEncoding(text, "p50k_base")
		require.NoError(t, err)
		assert.Greater(t, count, 0)
	})

	t.Run("invalid encoding", func(t *testing.T) {
		_, err := tokenizer.CountTokensForEncoding(text, "invalid_encoding")
		assert.Error(t, err)
	})
}

func TestCountTokensInJSON(t *testing.T) {
	logger := zap.NewNop().Sugar()
	tokenizer, err := NewTokenizer("cl100k_base", logger, true)
	require.NoError(t, err)

	t.Run("simple object", func(t *testing.T) {
		data := map[string]interface{}{
			"message": "Hello, world!",
			"count":   42,
		}
		count, err := tokenizer.CountTokensInJSON(data)
		require.NoError(t, err)
		assert.Greater(t, count, 0)
	})

	t.Run("nested object", func(t *testing.T) {
		data := map[string]interface{}{
			"user": map[string]interface{}{
				"name":  "John Doe",
				"email": "john@example.com",
			},
			"metadata": map[string]interface{}{
				"timestamp": "2025-09-30T12:00:00Z",
				"version":   "1.0.0",
			},
		}
		count, err := tokenizer.CountTokensInJSON(data)
		require.NoError(t, err)
		assert.Greater(t, count, 10)
	})

	t.Run("array", func(t *testing.T) {
		data := []string{"apple", "banana", "cherry"}
		count, err := tokenizer.CountTokensInJSON(data)
		require.NoError(t, err)
		assert.Greater(t, count, 0)
	})
}

func TestCountTokensInJSONForModel(t *testing.T) {
	logger := zap.NewNop().Sugar()
	tokenizer, err := NewTokenizer("cl100k_base", logger, true)
	require.NoError(t, err)

	data := map[string]interface{}{
		"query": "What is the weather today?",
		"options": map[string]interface{}{
			"temperature": 0.7,
			"max_tokens":  100,
		},
	}

	t.Run("gpt-4 model", func(t *testing.T) {
		count, err := tokenizer.CountTokensInJSONForModel(data, "gpt-4")
		require.NoError(t, err)
		assert.Greater(t, count, 0)
	})

	t.Run("claude model", func(t *testing.T) {
		count, err := tokenizer.CountTokensInJSONForModel(data, "claude-3-opus")
		require.NoError(t, err)
		assert.Greater(t, count, 0)
	})
}

func TestEncodingCache(t *testing.T) {
	logger := zap.NewNop().Sugar()
	tokenizer, err := NewTokenizer("cl100k_base", logger, true)
	require.NoError(t, err)

	text := "Hello, world!"

	// First call should cache the encoding
	count1, err := tokenizer.CountTokensForEncoding(text, "o200k_base")
	require.NoError(t, err)

	// Second call should use cached encoding
	count2, err := tokenizer.CountTokensForEncoding(text, "o200k_base")
	require.NoError(t, err)

	assert.Equal(t, count1, count2)

	// Cache should contain the encoding
	assert.Contains(t, tokenizer.encodingCache, "o200k_base")
}

func TestSetEnabled(t *testing.T) {
	logger := zap.NewNop().Sugar()
	tokenizer, err := NewTokenizer("cl100k_base", logger, true)
	require.NoError(t, err)

	// Initially enabled
	count, err := tokenizer.CountTokens("Hello")
	require.NoError(t, err)
	assert.Greater(t, count, 0)

	// Disable
	tokenizer.SetEnabled(false)
	assert.False(t, tokenizer.IsEnabled())

	count, err = tokenizer.CountTokens("Hello")
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Re-enable
	tokenizer.SetEnabled(true)
	assert.True(t, tokenizer.IsEnabled())

	count, err = tokenizer.CountTokens("Hello")
	require.NoError(t, err)
	assert.Greater(t, count, 0)
}

func TestSetDefaultEncoding(t *testing.T) {
	logger := zap.NewNop().Sugar()
	tokenizer, err := NewTokenizer("cl100k_base", logger, true)
	require.NoError(t, err)

	t.Run("valid encoding", func(t *testing.T) {
		err := tokenizer.SetDefaultEncoding("o200k_base")
		require.NoError(t, err)
		assert.Equal(t, "o200k_base", tokenizer.GetDefaultEncoding())
	})

	t.Run("invalid encoding", func(t *testing.T) {
		err := tokenizer.SetDefaultEncoding("invalid_encoding")
		assert.Error(t, err)
		// Should keep old encoding
		assert.Equal(t, "o200k_base", tokenizer.GetDefaultEncoding())
	})
}

func TestGetEncodingForModel(t *testing.T) {
	tests := []struct {
		model    string
		encoding string
	}{
		{"gpt-4o", "o200k_base"},
		{"gpt-4", "cl100k_base"},
		{"gpt-3.5-turbo", "cl100k_base"},
		{"claude-3-5-sonnet", "cl100k_base"},
		{"code-davinci-002", "p50k_base"},
		{"text-davinci-003", "r50k_base"},
		{"unknown-model", DefaultEncoding},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			encoding := GetEncodingForModel(tt.model)
			assert.Equal(t, tt.encoding, encoding)
		})
	}
}

func TestIsClaudeModel(t *testing.T) {
	tests := []struct {
		model    string
		isClaude bool
	}{
		{"claude-3-5-sonnet", true},
		{"claude-3-opus", true},
		{"claude-2.1", true},
		{"gpt-4", false},
		{"gpt-3.5-turbo", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			result := IsClaudeModel(tt.model)
			assert.Equal(t, tt.isClaude, result)
		})
	}
}

func TestSupportedModels(t *testing.T) {
	models := SupportedModels()
	assert.Greater(t, len(models), 0)
	assert.Contains(t, models, "gpt-4")
	assert.Contains(t, models, "gpt-4o")
	assert.Contains(t, models, "claude-3-5-sonnet")
}

func TestSupportedEncodings(t *testing.T) {
	encodings := SupportedEncodings()
	assert.Equal(t, 4, len(encodings))
	assert.Contains(t, encodings, "o200k_base")
	assert.Contains(t, encodings, "cl100k_base")
	assert.Contains(t, encodings, "p50k_base")
	assert.Contains(t, encodings, "r50k_base")
}
