package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// DeprecatedField describes a deprecated configuration field.
type DeprecatedField struct {
	JSONKey     string `json:"json_key"`
	Message     string `json:"message"`
	Replacement string `json:"replacement,omitempty"`
}

// deprecatedFields is the list of known deprecated config keys.
var deprecatedFields = []DeprecatedField{
	{
		JSONKey:     "top_k",
		Message:     "top_k is deprecated and has no effect",
		Replacement: "Use tools_limit instead",
	},
	{
		JSONKey:     "enable_tray",
		Message:     "enable_tray is deprecated and has no effect",
		Replacement: "Remove from config (tray is managed by the tray application)",
	},
	{
		JSONKey:     "features",
		Message:     "features is deprecated and has no effect",
		Replacement: "Remove from config (all feature flags are unused)",
	},
}

// CheckDeprecatedFields reads the raw JSON config file and returns which
// deprecated keys are present. It does not validate or parse the full config.
func CheckDeprecatedFields(configPath string) []DeprecatedField {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}

	var found []DeprecatedField
	for _, df := range deprecatedFields {
		if _, exists := raw[df.JSONKey]; exists {
			found = append(found, df)
		}
	}
	return found
}

// CleanDeprecatedFields removes deprecated keys from the config file on disk.
// It creates a .bak backup before modifying the file. If no deprecated fields
// are found, the file is not touched and no backup is created.
// Returns the list of removed fields, or nil if nothing was changed.
func CleanDeprecatedFields(configPath string) ([]DeprecatedField, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil // unparseable config — leave it alone
	}

	// Find which deprecated keys are present.
	var found []DeprecatedField
	for _, df := range deprecatedFields {
		if _, exists := raw[df.JSONKey]; exists {
			found = append(found, df)
		}
	}
	if len(found) == 0 {
		return nil, nil
	}

	// Preserve original file permissions.
	info, err := os.Stat(configPath)
	if err != nil {
		return nil, fmt.Errorf("stat config: %w", err)
	}
	perm := info.Mode().Perm()

	// Create backup before modifying.
	backupPath := configPath + ".bak"
	if err := os.WriteFile(backupPath, data, perm); err != nil {
		return nil, fmt.Errorf("creating backup: %w", err)
	}

	// Remove deprecated keys.
	for _, df := range found {
		delete(raw, df.JSONKey)
	}

	// Marshal back with indentation.
	cleaned, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling cleaned config: %w", err)
	}
	cleaned = append(cleaned, '\n')

	// Write to temp file then rename for atomicity.
	tmpPath := configPath + ".tmp"
	if err := os.WriteFile(tmpPath, cleaned, perm); err != nil {
		return nil, fmt.Errorf("writing cleaned config: %w", err)
	}
	if err := os.Rename(tmpPath, configPath); err != nil {
		os.Remove(tmpPath) // best-effort cleanup
		return nil, fmt.Errorf("replacing config: %w", err)
	}

	return found, nil
}
