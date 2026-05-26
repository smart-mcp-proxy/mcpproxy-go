package hash

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

type toolHashContract struct {
	ServerName       string          `json:"server_name"`
	ToolName         string          `json:"tool_name"`
	Description      string          `json:"description"`
	InputSchema      json.RawMessage `json:"input_schema,omitempty"`
	OutputSchemaJSON json.RawMessage `json:"output_schema,omitempty"`
}

// ToolHash computes SHA-256 hash for tool change detection.
// Format: sha256(canonical JSON of serverName, toolName, description, input schema)
func ToolHash(serverName, toolName, description string, parametersSchema interface{}) (string, error) {
	return ToolHashWithOutputSchema(serverName, toolName, description, parametersSchema, "")
}

// ToolHashWithOutputSchema computes SHA-256 hash for the full tool contract.
// Output schema is included because it describes the data shape returned to the
// agent and therefore belongs to the human-approved tool contract.
// Format: sha256(canonical JSON of serverName, toolName, description, input schema, output schema)
func ToolHashWithOutputSchema(serverName, toolName, description string, parametersSchema interface{}, outputSchemaJSON string) (string, error) {
	inputSchema, err := canonicalSchemaFromInterface(parametersSchema)
	if err != nil {
		return "", fmt.Errorf("failed to marshal parameters schema: %w", err)
	}

	outputSchema, err := canonicalSchemaFromString(outputSchemaJSON)
	if err != nil {
		return "", fmt.Errorf("failed to marshal output schema: %w", err)
	}

	contract := toolHashContract{
		ServerName:       serverName,
		ToolName:         toolName,
		Description:      description,
		InputSchema:      inputSchema,
		OutputSchemaJSON: outputSchema,
	}

	contractBytes, err := json.Marshal(contract)
	if err != nil {
		return "", fmt.Errorf("failed to marshal tool hash contract: %w", err)
	}

	hasher := sha256.New()
	hasher.Write(contractBytes)
	hashBytes := hasher.Sum(nil)

	return hex.EncodeToString(hashBytes), nil
}

func canonicalSchemaFromInterface(schema interface{}) (json.RawMessage, error) {
	if schema == nil {
		return nil, nil
	}

	var raw json.RawMessage
	switch value := schema.(type) {
	case json.RawMessage:
		raw = value
	case []byte:
		raw = value
	case string:
		raw = []byte(value)
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		raw = data
	}

	return canonicalSchemaFromBytes(raw)
}

func canonicalSchemaFromString(schemaJSON string) (json.RawMessage, error) {
	if schemaJSON == "" {
		return nil, nil
	}
	return canonicalSchemaFromBytes([]byte(schemaJSON))
}

func canonicalSchemaFromBytes(schemaJSON []byte) (json.RawMessage, error) {
	if len(schemaJSON) == 0 {
		return nil, nil
	}

	var parsed interface{}
	if err := json.Unmarshal(schemaJSON, &parsed); err != nil {
		return nil, err
	}

	canonical, err := json.Marshal(parsed)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(canonical), nil
}

// NormalizeJSON parses s and re-serializes it with object keys sorted, so that
// semantically identical JSON with different key order or whitespace produces a
// stable, comparable string. Empty or non-JSON input is returned unchanged.
//
// This is the single canonical JSON normalizer shared by the upstream tool
// capture (internal/upstream/core) and the tool-approval hash
// (internal/runtime), so a schema hashes identically no matter which path
// observed it.
func NormalizeJSON(s string) string {
	if s == "" {
		return s
	}
	var parsed interface{}
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return s
	}
	normalized, err := json.Marshal(parsed)
	if err != nil {
		return s
	}
	return string(normalized)
}

// StringHash computes SHA-256 hash of a string
func StringHash(input string) string {
	hasher := sha256.New()
	hasher.Write([]byte(input))
	hashBytes := hasher.Sum(nil)
	return hex.EncodeToString(hashBytes)
}

// BytesHash computes SHA-256 hash of byte slice
func BytesHash(input []byte) string {
	hasher := sha256.New()
	hasher.Write(input)
	hashBytes := hasher.Sum(nil)
	return hex.EncodeToString(hashBytes)
}

// VerifyToolHash verifies if the current tool matches the stored hash
func VerifyToolHash(serverName, toolName, description string, parametersSchema interface{}, storedHash string) (bool, error) {
	currentHash, err := ToolHash(serverName, toolName, description, parametersSchema)
	if err != nil {
		return false, err
	}

	return currentHash == storedHash, nil
}

// ComputeToolHash computes a SHA256 hash for a tool (alias for ToolHash that doesn't return error)
func ComputeToolHash(serverName, toolName, description string, inputSchema interface{}) string {
	return ComputeToolHashWithOutputSchema(serverName, toolName, description, inputSchema, "")
}

// ComputeToolHashWithOutputSchema computes a SHA256 hash for a tool including output schema.
func ComputeToolHashWithOutputSchema(serverName, toolName, description string, inputSchema interface{}, outputSchemaJSON string) string {
	hash, err := ToolHashWithOutputSchema(serverName, toolName, description, inputSchema, outputSchemaJSON)
	if err != nil {
		// If hashing fails, return a default hash based on server and tool name
		fallback := StringHash(fmt.Sprintf("%s:%s", serverName, toolName))
		return fallback
	}
	return hash
}
