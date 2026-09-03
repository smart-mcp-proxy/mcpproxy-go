package hash

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
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

	// Same library-drift protection NormalizeJSON applies. This file holds TWO
	// normalizers over the same decoded schema, and they back different hashes:
	// NormalizeJSON feeds the Spec-032 approval hash, while this one feeds
	// ToolHash/ComputeToolHashWithOutputSchema, i.e. ToolMetadata.Hash written
	// at internal/upstream/core/client.go. Canonicalizing only one of them would
	// leave the other drifting across a library upgrade — the exact defect the
	// canonicalization exists to prevent, just on the other code path.
	if hasHoistedDefsWithDraft07Refs(parsed) {
		canonicalizeSchemaRefs(parsed)
	}

	canonical, err := json.Marshal(parsed)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(canonical), nil
}

// NormalizeJSON parses s and re-serializes it with object keys sorted, so that
// semantically identical JSON with different key order or whitespace produces a
// stable, comparable string. It also canonicalizes JSON Schema draft-07 local
// references (see canonicalizeSchemaRefs). Empty or non-JSON input is returned
// unchanged.
//
// This is the single canonical JSON normalizer shared by the upstream tool
// capture (internal/upstream/core) and the tool-approval hash
// (internal/runtime), so a schema hashes identically no matter which path
// observed it — or which version of the MCP library decoded it.
func NormalizeJSON(s string) string {
	if s == "" {
		return s
	}
	var parsed interface{}
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return s
	}
	if hasHoistedDefsWithDraft07Refs(parsed) {
		canonicalizeSchemaRefs(parsed)
	}
	normalized, err := json.Marshal(parsed)
	if err != nil {
		return s
	}
	return string(normalized)
}

// draft07RefPrefix is the local-definition pointer prefix used by JSON Schema
// draft-07. Drafts 2019-09 and later spell the same thing "#/$defs/".
const (
	draft07RefPrefix = "#/definitions/"
	modernRefPrefix  = "#/$defs/"
)

// hasHoistedDefsWithDraft07Refs reports whether a document carries the specific
// signature of a schema whose definitions were hoisted into "$defs" while its
// "$ref" pointers were left spelling the old location: a root "$defs" and no
// root "definitions".
//
// The narrowness is the point. A "$ref" of "#/definitions/X" is CORRECT in a
// document that still has a "definitions" block, and rewriting it there would
// point it at a member that does not exist — breaking Spec 056 output-schema
// validation, which consumes this same normalized JSON. Only the hoisted-yet-
// unrewritten combination is unambiguously the artifact described on
// canonicalizeSchemaRefs, so only that combination is touched.
func hasHoistedDefsWithDraft07Refs(root interface{}) bool {
	doc, ok := root.(map[string]interface{})
	if !ok {
		return false
	}
	if _, hasDefs := doc["$defs"]; !hasDefs {
		return false
	}
	if _, hasDefinitions := doc["definitions"]; hasDefinitions {
		return false
	}
	return true
}

// canonicalizeSchemaRefs rewrites draft-07 local refs ("#/definitions/X") to
// their modern spelling ("#/$defs/X") in place. Call it only when
// hasHoistedDefsWithDraft07Refs approves the document.
//
// This exists because a tool's approval hash is computed over the MCP library's
// DECODED input schema, not over the bytes the upstream sent
// (mcp.Tool.RawInputSchema is `json:"-"`, so the original is discarded on
// decode). Library versions disagree on the spelling they emit for one
// identical wire schema: mcp-go hoisted `definitions` into `$defs` for a long
// time while leaving the pointers dangling, and v1.0.0 began rewriting them.
// Hashing the decoder's output verbatim therefore lets a library upgrade change
// the hash of a tool nobody touched, which Spec 032 reports as a changed —
// potentially rug-pulled — tool.
//
// Only the exact local-definition prefix is rewritten. Self-references such as
// "#/properties/x", absolute URLs and already-modern pointers are left alone,
// because rewriting those would change what a schema means rather than how it
// is spelled.
func canonicalizeSchemaRefs(node interface{}) {
	switch v := node.(type) {
	case map[string]interface{}:
		if ref, ok := v["$ref"].(string); ok && strings.HasPrefix(ref, draft07RefPrefix) {
			v["$ref"] = modernRefPrefix + strings.TrimPrefix(ref, draft07RefPrefix)
		}
		for _, child := range v {
			canonicalizeSchemaRefs(child)
		}
	case []interface{}:
		for _, item := range v {
			canonicalizeSchemaRefs(item)
		}
	}
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
