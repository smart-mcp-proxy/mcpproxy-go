package hash

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolHash_Basic(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"param1": map[string]interface{}{
				"type": "string",
			},
		},
	}

	hash1, err := ToolHash("server1", "tool1", "A test tool", schema)
	require.NoError(t, err)
	assert.NotEmpty(t, hash1)

	// Same inputs should produce same hash
	hash2, err := ToolHash("server1", "tool1", "A test tool", schema)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)
}

func TestToolHash_DifferentServerName(t *testing.T) {
	schema := map[string]interface{}{"type": "object"}
	desc := "Test tool"

	hash1, err := ToolHash("server1", "tool1", desc, schema)
	require.NoError(t, err)

	hash2, err := ToolHash("server2", "tool1", desc, schema)
	require.NoError(t, err)

	assert.NotEqual(t, hash1, hash2, "Different server names should produce different hashes")
}

func TestToolHash_DifferentToolName(t *testing.T) {
	schema := map[string]interface{}{"type": "object"}
	desc := "Test tool"

	hash1, err := ToolHash("server1", "tool1", desc, schema)
	require.NoError(t, err)

	hash2, err := ToolHash("server1", "tool2", desc, schema)
	require.NoError(t, err)

	assert.NotEqual(t, hash1, hash2, "Different tool names should produce different hashes")
}

func TestToolHash_DifferentDescription(t *testing.T) {
	schema := map[string]interface{}{"type": "object"}

	hash1, err := ToolHash("server1", "tool1", "Description v1", schema)
	require.NoError(t, err)

	hash2, err := ToolHash("server1", "tool1", "Description v2 - modified", schema)
	require.NoError(t, err)

	assert.NotEqual(t, hash1, hash2, "Different descriptions should produce different hashes")
}

func TestToolHash_DifferentSchema(t *testing.T) {
	desc := "Test tool"
	schema1 := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"param1": map[string]interface{}{"type": "string"},
		},
	}
	schema2 := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"param1": map[string]interface{}{"type": "string"},
			"param2": map[string]interface{}{"type": "number"},
		},
	}

	hash1, err := ToolHash("server1", "tool1", desc, schema1)
	require.NoError(t, err)

	hash2, err := ToolHash("server1", "tool1", desc, schema2)
	require.NoError(t, err)

	assert.NotEqual(t, hash1, hash2, "Different schemas should produce different hashes")
}

func TestToolHash_NilSchema(t *testing.T) {
	hash1, err := ToolHash("server1", "tool1", "Test tool", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, hash1)

	// Empty schema should produce consistent hash
	hash2, err := ToolHash("server1", "tool1", "Test tool", nil)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)
}

func TestToolHash_EmptyDescription(t *testing.T) {
	schema := map[string]interface{}{"type": "object"}

	hash1, err := ToolHash("server1", "tool1", "", schema)
	require.NoError(t, err)
	assert.NotEmpty(t, hash1)

	hash2, err := ToolHash("server1", "tool1", "non-empty", schema)
	require.NoError(t, err)

	assert.NotEqual(t, hash1, hash2, "Empty vs non-empty description should differ")
}

func TestComputeToolHash_Basic(t *testing.T) {
	schema := map[string]interface{}{"type": "object"}

	hash := ComputeToolHash("server1", "tool1", "Test tool", schema)
	assert.NotEmpty(t, hash)

	// Should be consistent
	hash2 := ComputeToolHash("server1", "tool1", "Test tool", schema)
	assert.Equal(t, hash, hash2)
}

func TestComputeToolHash_FallbackOnMarshalError(t *testing.T) {
	// Create a value that cannot be marshaled to JSON (channel)
	// Note: Go's json.Marshal actually handles most types gracefully,
	// but functions and channels will fail
	invalidSchema := make(chan int)

	// Should not panic, should return fallback hash
	hash := ComputeToolHash("server1", "tool1", "desc", invalidSchema)
	assert.NotEmpty(t, hash, "Should return fallback hash on marshal error")
}

func TestComputeToolHash_DescriptionOnlyChange(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"arg": map[string]interface{}{"type": "string"},
		},
	}

	hash1 := ComputeToolHash("myserver", "my_tool", "Original description", schema)
	hash2 := ComputeToolHash("myserver", "my_tool", "Updated description with more details", schema)

	assert.NotEqual(t, hash1, hash2, "Description-only changes must produce different hashes")
}

func TestVerifyToolHash_Match(t *testing.T) {
	schema := map[string]interface{}{"type": "object"}
	desc := "Test tool"

	hash, err := ToolHash("server1", "tool1", desc, schema)
	require.NoError(t, err)

	matches, err := VerifyToolHash("server1", "tool1", desc, schema, hash)
	require.NoError(t, err)
	assert.True(t, matches, "Hash should match for same inputs")
}

func TestVerifyToolHash_NoMatch(t *testing.T) {
	schema := map[string]interface{}{"type": "object"}

	hash, err := ToolHash("server1", "tool1", "desc v1", schema)
	require.NoError(t, err)

	// Different description
	matches, err := VerifyToolHash("server1", "tool1", "desc v2", schema, hash)
	require.NoError(t, err)
	assert.False(t, matches, "Hash should not match for different description")
}

func TestStringHash(t *testing.T) {
	hash1 := StringHash("hello")
	hash2 := StringHash("hello")
	hash3 := StringHash("world")

	assert.Equal(t, hash1, hash2, "Same input should produce same hash")
	assert.NotEqual(t, hash1, hash3, "Different input should produce different hash")
	assert.Len(t, hash1, 64, "SHA-256 hex string should be 64 characters")
}

func TestBytesHash(t *testing.T) {
	hash1 := BytesHash([]byte("hello"))
	hash2 := BytesHash([]byte("hello"))
	hash3 := BytesHash([]byte("world"))

	assert.Equal(t, hash1, hash2, "Same input should produce same hash")
	assert.NotEqual(t, hash1, hash3, "Different input should produce different hash")
	assert.Len(t, hash1, 64, "SHA-256 hex string should be 64 characters")
}
