package main

import (
	"testing"
)

func TestGetStringField(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		key      string
		expected string
	}{
		{
			name:     "valid string",
			input:    map[string]interface{}{"name": "test-server"},
			key:      "name",
			expected: "test-server",
		},
		{
			name:     "missing key",
			input:    map[string]interface{}{"other": "value"},
			key:      "name",
			expected: "",
		},
		{
			name:     "nil map",
			input:    nil,
			key:      "name",
			expected: "",
		},
		{
			name:     "wrong type - int",
			input:    map[string]interface{}{"name": 123},
			key:      "name",
			expected: "",
		},
		{
			name:     "wrong type - bool",
			input:    map[string]interface{}{"name": true},
			key:      "name",
			expected: "",
		},
		{
			name:     "empty string",
			input:    map[string]interface{}{"name": ""},
			key:      "name",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getStringField(tt.input, tt.key)
			if result != tt.expected {
				t.Errorf("getStringField() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetBoolField(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		key      string
		expected bool
	}{
		{
			name:     "true value",
			input:    map[string]interface{}{"enabled": true},
			key:      "enabled",
			expected: true,
		},
		{
			name:     "false value",
			input:    map[string]interface{}{"enabled": false},
			key:      "enabled",
			expected: false,
		},
		{
			name:     "missing key",
			input:    map[string]interface{}{"other": true},
			key:      "enabled",
			expected: false,
		},
		{
			name:     "nil map",
			input:    nil,
			key:      "enabled",
			expected: false,
		},
		{
			name:     "wrong type - string",
			input:    map[string]interface{}{"enabled": "true"},
			key:      "enabled",
			expected: false,
		},
		{
			name:     "wrong type - int",
			input:    map[string]interface{}{"enabled": 1},
			key:      "enabled",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getBoolField(tt.input, tt.key)
			if result != tt.expected {
				t.Errorf("getBoolField() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetIntField(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		key      string
		expected int
	}{
		{
			name:     "float64 value",
			input:    map[string]interface{}{"count": float64(42)},
			key:      "count",
			expected: 42,
		},
		{
			name:     "int value",
			input:    map[string]interface{}{"count": 42},
			key:      "count",
			expected: 42,
		},
		{
			name:     "zero value",
			input:    map[string]interface{}{"count": 0},
			key:      "count",
			expected: 0,
		},
		{
			name:     "missing key",
			input:    map[string]interface{}{"other": 42},
			key:      "count",
			expected: 0,
		},
		{
			name:     "nil map",
			input:    nil,
			key:      "count",
			expected: 0,
		},
		{
			name:     "wrong type - string",
			input:    map[string]interface{}{"count": "42"},
			key:      "count",
			expected: 0,
		},
		{
			name:     "wrong type - bool",
			input:    map[string]interface{}{"count": true},
			key:      "count",
			expected: 0,
		},
		{
			name:     "large float64",
			input:    map[string]interface{}{"count": float64(999999)},
			key:      "count",
			expected: 999999,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getIntField(tt.input, tt.key)
			if result != tt.expected {
				t.Errorf("getIntField() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetArrayField(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		key      string
		expected []interface{}
	}{
		{
			name:     "valid array",
			input:    map[string]interface{}{"items": []interface{}{"a", "b", "c"}},
			key:      "items",
			expected: []interface{}{"a", "b", "c"},
		},
		{
			name:     "empty array",
			input:    map[string]interface{}{"items": []interface{}{}},
			key:      "items",
			expected: []interface{}{},
		},
		{
			name:     "missing key",
			input:    map[string]interface{}{"other": []interface{}{"a"}},
			key:      "items",
			expected: nil,
		},
		{
			name:     "nil map",
			input:    nil,
			key:      "items",
			expected: nil,
		},
		{
			name:     "nil value",
			input:    map[string]interface{}{"items": nil},
			key:      "items",
			expected: nil,
		},
		{
			name:     "wrong type - string",
			input:    map[string]interface{}{"items": "not an array"},
			key:      "items",
			expected: nil,
		},
		{
			name:     "mixed types in array",
			input:    map[string]interface{}{"items": []interface{}{"string", 42, true}},
			key:      "items",
			expected: []interface{}{"string", 42, true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getArrayField(tt.input, tt.key)
			if tt.expected == nil && result != nil {
				t.Errorf("getArrayField() = %v, want nil", result)
			} else if tt.expected != nil && result == nil {
				t.Errorf("getArrayField() = nil, want %v", tt.expected)
			} else if tt.expected != nil && result != nil {
				if len(result) != len(tt.expected) {
					t.Errorf("getArrayField() length = %v, want %v", len(result), len(tt.expected))
				}
			}
		})
	}
}

func TestGetStringArrayField(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		key      string
		expected []string
	}{
		{
			name:     "valid string array",
			input:    map[string]interface{}{"servers": []interface{}{"srv1", "srv2", "srv3"}},
			key:      "servers",
			expected: []string{"srv1", "srv2", "srv3"},
		},
		{
			name:     "empty array",
			input:    map[string]interface{}{"servers": []interface{}{}},
			key:      "servers",
			expected: []string{},
		},
		{
			name:     "missing key",
			input:    map[string]interface{}{"other": []interface{}{"srv1"}},
			key:      "servers",
			expected: nil,
		},
		{
			name:     "nil map",
			input:    nil,
			key:      "servers",
			expected: nil,
		},
		{
			name:     "nil value",
			input:    map[string]interface{}{"servers": nil},
			key:      "servers",
			expected: nil,
		},
		{
			name:     "wrong type - string",
			input:    map[string]interface{}{"servers": "not an array"},
			key:      "servers",
			expected: nil,
		},
		{
			name:     "mixed types - filters non-strings",
			input:    map[string]interface{}{"servers": []interface{}{"srv1", 42, "srv2", true}},
			key:      "servers",
			expected: []string{"srv1", "srv2"},
		},
		{
			name:     "all non-string types",
			input:    map[string]interface{}{"servers": []interface{}{42, true, 3.14}},
			key:      "servers",
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getStringArrayField(tt.input, tt.key)
			if tt.expected == nil && result != nil {
				t.Errorf("getStringArrayField() = %v, want nil", result)
			} else if tt.expected != nil && result == nil {
				t.Errorf("getStringArrayField() = nil, want %v", tt.expected)
			} else if tt.expected != nil && result != nil {
				if len(result) != len(tt.expected) {
					t.Errorf("getStringArrayField() length = %v, want %v", len(result), len(tt.expected))
				} else {
					for i := range result {
						if result[i] != tt.expected[i] {
							t.Errorf("getStringArrayField()[%d] = %v, want %v", i, result[i], tt.expected[i])
						}
					}
				}
			}
		})
	}
}
