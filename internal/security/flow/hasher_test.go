package flow

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashContent_ProducesCorrectLength(t *testing.T) {
	hash := HashContent("hello world")
	assert.Len(t, hash, 32, "hash should be 32 hex chars (128 bits)")
}

func TestHashContent_Deterministic(t *testing.T) {
	h1 := HashContent("test content")
	h2 := HashContent("test content")
	assert.Equal(t, h1, h2, "same input should produce same hash")
}

func TestHashContent_DifferentInputs(t *testing.T) {
	h1 := HashContent("input A")
	h2 := HashContent("input B")
	assert.NotEqual(t, h1, h2, "different inputs should produce different hashes")
}

func TestHashContent_HexCharacters(t *testing.T) {
	hash := HashContent("some data")
	for _, ch := range hash {
		assert.True(t, (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f'),
			"hash should contain only lowercase hex characters, got %c", ch)
	}
}

func TestHashContent_EmptyString(t *testing.T) {
	hash := HashContent("")
	assert.Len(t, hash, 32, "empty string should still produce 32 char hash")
}

func TestHashContentNormalized_CaseInsensitive(t *testing.T) {
	h1 := HashContentNormalized("Hello World")
	h2 := HashContentNormalized("hello world")
	assert.Equal(t, h1, h2, "normalized hash should be case-insensitive")
}

func TestHashContentNormalized_TrimWhitespace(t *testing.T) {
	h1 := HashContentNormalized("hello world")
	h2 := HashContentNormalized("  hello world  ")
	assert.Equal(t, h1, h2, "normalized hash should trim whitespace")
}

func TestHashContentNormalized_BothNormalizations(t *testing.T) {
	h1 := HashContentNormalized("Hello World")
	h2 := HashContentNormalized("  hello world\n")
	assert.Equal(t, h1, h2, "normalized hash should handle both case and whitespace")
}

func TestHashContentNormalized_PreservesInternalSpaces(t *testing.T) {
	h1 := HashContentNormalized("hello   world")
	h2 := HashContentNormalized("hello world")
	assert.NotEqual(t, h1, h2, "normalized hash should preserve internal whitespace differences")
}

func TestExtractFieldHashes_JSONStrings(t *testing.T) {
	input := `{"name": "a short value", "description": "This is a long enough description that should be hashed separately"}`
	minLength := 20

	hashes := ExtractFieldHashes(input, minLength)

	// "a short value" is 13 chars, should be skipped
	// "This is a long enough description that should be hashed separately" is 66 chars, should be included
	require.NotEmpty(t, hashes, "should extract at least one field hash")

	// The full content hash should not be the only entry
	fullHash := HashContent(input)
	hasFieldHash := false
	for h := range hashes {
		if h != fullHash {
			hasFieldHash = true
			break
		}
	}
	assert.True(t, hasFieldHash, "should have per-field hashes in addition to any full hash")
}

func TestExtractFieldHashes_SkipsShortStrings(t *testing.T) {
	input := `{"a": "short", "b": "tiny", "c": "no"}`
	minLength := 20

	hashes := ExtractFieldHashes(input, minLength)
	assert.Empty(t, hashes, "should not extract hashes for strings shorter than minLength")
}

func TestExtractFieldHashes_NonJSON(t *testing.T) {
	input := "This is plain text content that is long enough to be hashed"
	minLength := 20

	hashes := ExtractFieldHashes(input, minLength)
	// For non-JSON, should return the full content hash if long enough
	assert.NotEmpty(t, hashes, "non-JSON content should still produce a hash if long enough")
}

func TestExtractFieldHashes_NonJSONShort(t *testing.T) {
	input := "too short"
	minLength := 20

	hashes := ExtractFieldHashes(input, minLength)
	assert.Empty(t, hashes, "short non-JSON content should produce no hashes")
}

func TestExtractFieldHashes_NestedJSON(t *testing.T) {
	input := `{
		"outer": {
			"inner_field": "This is a nested string value that is definitely long enough to hash"
		}
	}`
	minLength := 20

	hashes := ExtractFieldHashes(input, minLength)
	require.NotEmpty(t, hashes, "should extract hashes from nested JSON fields")

	// Verify the inner string value was hashed
	innerHash := HashContent("This is a nested string value that is definitely long enough to hash")
	_, found := hashes[innerHash]
	assert.True(t, found, "should find hash of nested string value")
}

func TestExtractFieldHashes_ArrayElements(t *testing.T) {
	input := `{
		"items": [
			"Short",
			"This is an array element long enough to be hashed individually"
		]
	}`
	minLength := 20

	hashes := ExtractFieldHashes(input, minLength)
	require.NotEmpty(t, hashes, "should extract hashes from array string elements")

	longElemHash := HashContent("This is an array element long enough to be hashed individually")
	_, found := hashes[longElemHash]
	assert.True(t, found, "should find hash of long array element")
}

func TestExtractFieldHashes_MultipleLongFields(t *testing.T) {
	input := `{
		"field1": "This is the first long enough field value for hashing",
		"field2": "This is the second long enough field value for hashing"
	}`
	minLength := 20

	hashes := ExtractFieldHashes(input, minLength)
	assert.GreaterOrEqual(t, len(hashes), 2, "should extract hashes for each long field")
}

func TestExtractFieldHashes_NumbersAndBooleans(t *testing.T) {
	input := `{"count": 42, "flag": true, "text": "This is a long enough text field for testing"}`
	minLength := 20

	hashes := ExtractFieldHashes(input, minLength)
	// Only string fields should be hashed, not numbers or booleans
	// The one qualifying string should produce exactly one hash
	assert.Len(t, hashes, 1, "should only hash string fields")
}

func TestNormalizedHashingCatchesReformattedData(t *testing.T) {
	// Scenario: data is read from one tool, then pasted into another with
	// slight formatting changes (whitespace, case)
	original := "Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.secret_token_here"
	reformatted := "  bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.secret_token_here  "

	h1 := HashContentNormalized(original)
	h2 := HashContentNormalized(reformatted)
	assert.Equal(t, h1, h2, "normalized hashing should match reformatted data")
}

func TestExtractFieldHashes_LargeJSON(t *testing.T) {
	// Build a JSON object with many fields
	obj := make(map[string]string)
	for i := 0; i < 100; i++ {
		obj["field_"+string(rune('a'+i%26))+strings.Repeat("x", i)] = strings.Repeat("value_", 5) + strings.Repeat("x", i)
	}
	data, err := json.Marshal(obj)
	require.NoError(t, err)

	hashes := ExtractFieldHashes(string(data), 20)
	// Should process without panic or error
	assert.NotNil(t, hashes)
}

func TestHashContent_IsSHA256Truncated(t *testing.T) {
	// Verify it's a proper SHA256 truncation (first 16 bytes = 32 hex chars)
	hash := HashContent("test")
	// SHA256 of "test" = 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08
	// Truncated to 32 chars = 9f86d081884c7d659a2feaa0c55ad015
	assert.Equal(t, "9f86d081884c7d659a2feaa0c55ad015", hash)
}
