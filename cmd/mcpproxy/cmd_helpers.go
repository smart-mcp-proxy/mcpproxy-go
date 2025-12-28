package main

// cmd_helpers.go provides type-safe helper functions for extracting fields
// from JSON-decoded map[string]interface{} responses from the MCPProxy API.
//
// These functions are used throughout the CLI commands to safely extract
// typed values from API responses, handling missing keys and type mismatches
// gracefully by returning zero values.

func getStringField(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getBoolField(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func getIntField(m map[string]interface{}, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	if v, ok := m[key].(int); ok {
		return v
	}
	return 0
}

func getArrayField(m map[string]interface{}, key string) []interface{} {
	if v, ok := m[key]; ok && v != nil {
		if arr, ok := v.([]interface{}); ok {
			return arr
		}
	}
	return nil
}

func getStringArrayField(m map[string]interface{}, key string) []string {
	if v, ok := m[key]; ok && v != nil {
		if arr, ok := v.([]interface{}); ok {
			result := make([]string, 0, len(arr))
			for _, item := range arr {
				if str, ok := item.(string); ok {
					result = append(result, str)
				}
			}
			return result
		}
	}
	return nil
}

func getMapField(m map[string]interface{}, key string) map[string]interface{} {
	if v, ok := m[key]; ok && v != nil {
		if mm, ok := v.(map[string]interface{}); ok {
			return mm
		}
	}
	return nil
}
