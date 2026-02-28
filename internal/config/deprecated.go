package config

import (
	"encoding/json"
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
