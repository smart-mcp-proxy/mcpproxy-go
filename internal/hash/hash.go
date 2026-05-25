package hash

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// ToolHash computes SHA-256 hash for tool change detection.
// Format: sha256(serverName + toolName + description + parametersSchemaJSON)
func ToolHash(serverName, toolName, description string, parametersSchema interface{}) (string, error) {
	return ToolHashWithOutputSchema(serverName, toolName, description, parametersSchema, "")
}

// ToolHashWithOutputSchema computes SHA-256 hash for the full tool contract.
// Output schema is included because it describes the data shape returned to the
// agent and therefore belongs to the human-approved tool contract.
// Format: sha256(serverName + toolName + description + parametersSchemaJSON + outputSchemaJSON)
func ToolHashWithOutputSchema(serverName, toolName, description string, parametersSchema interface{}, outputSchemaJSON string) (string, error) {
	// Serialize parameters schema to JSON for consistent hashing
	var schemaBytes []byte
	var err error

	if parametersSchema != nil {
		schemaBytes, err = json.Marshal(parametersSchema)
		if err != nil {
			return "", fmt.Errorf("failed to marshal parameters schema: %w", err)
		}
	}

	// Combine server name, tool name, description, input schema JSON, and output schema JSON
	combined := serverName + toolName + description + string(schemaBytes) + outputSchemaJSON

	// Compute SHA-256 hash
	hasher := sha256.New()
	hasher.Write([]byte(combined))
	hashBytes := hasher.Sum(nil)

	return hex.EncodeToString(hashBytes), nil
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
