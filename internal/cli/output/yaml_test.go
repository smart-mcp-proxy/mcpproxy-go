package output

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// T048: Unit test for YAMLFormatter.Format()
func TestYAMLFormatter_Format(t *testing.T) {
	f := &YAMLFormatter{}

	tests := []struct {
		name    string
		data    interface{}
		wantErr bool
	}{
		{
			name: "format slice of maps",
			data: []map[string]interface{}{
				{"name": "server1", "status": "healthy"},
				{"name": "server2", "status": "unhealthy"},
			},
			wantErr: false,
		},
		{
			name: "format struct",
			data: struct {
				Name   string `yaml:"name"`
				Status string `yaml:"status"`
			}{Name: "test", Status: "ok"},
			wantErr: false,
		},
		{
			name:    "format nil",
			data:    nil,
			wantErr: false,
		},
		{
			name:    "format empty slice",
			data:    []string{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := f.Format(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("Format() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				// Verify result is valid YAML
				var parsed interface{}
				if err := yaml.Unmarshal([]byte(result), &parsed); err != nil {
					t.Errorf("Format() result is not valid YAML: %v", err)
				}
			}
		})
	}
}

func TestYAMLFormatter_Format_FieldNames(t *testing.T) {
	f := &YAMLFormatter{}

	data := struct {
		ServerName   string `yaml:"server_name"`
		ToolCount    int    `yaml:"tool_count"`
		IsConnected  bool   `yaml:"is_connected"`
		ErrorMessage string `yaml:"error_message,omitempty"`
	}{
		ServerName:  "test-server",
		ToolCount:   5,
		IsConnected: true,
	}

	result, err := f.Format(data)
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}

	// Verify YAML field names are snake_case
	if !strings.Contains(result, "server_name:") {
		t.Error("Expected 'server_name:' in YAML output")
	}
	if !strings.Contains(result, "tool_count:") {
		t.Error("Expected 'tool_count:' in YAML output")
	}
	if !strings.Contains(result, "is_connected:") {
		t.Error("Expected 'is_connected:' in YAML output")
	}
}

// T049: Unit test for YAMLFormatter.FormatError()
func TestYAMLFormatter_FormatError(t *testing.T) {
	f := &YAMLFormatter{}

	err := StructuredError{
		Code:            "TEST_ERROR",
		Message:         "Test error message",
		Guidance:        "Try doing X instead",
		RecoveryCommand: "mcpproxy test",
		Context:         map[string]interface{}{"key": "value"},
	}

	result, formatErr := f.FormatError(err)
	if formatErr != nil {
		t.Fatalf("FormatError() error = %v", formatErr)
	}

	// Verify result is valid YAML
	var parsed map[string]interface{}
	if yamlErr := yaml.Unmarshal([]byte(result), &parsed); yamlErr != nil {
		t.Fatalf("FormatError() result is not valid YAML: %v", yamlErr)
	}

	// Verify required fields are present
	if parsed["code"] != "TEST_ERROR" {
		t.Errorf("Expected code 'TEST_ERROR', got: %v", parsed["code"])
	}
	if parsed["message"] != "Test error message" {
		t.Errorf("Expected message 'Test error message', got: %v", parsed["message"])
	}
}

func TestYAMLFormatter_FormatError_OmitEmpty(t *testing.T) {
	f := &YAMLFormatter{}

	// Error without optional fields
	err := StructuredError{
		Code:    "SIMPLE_ERROR",
		Message: "Simple error",
	}

	result, formatErr := f.FormatError(err)
	if formatErr != nil {
		t.Fatalf("FormatError() error = %v", formatErr)
	}

	// Optional fields should not appear when empty (due to omitempty)
	if strings.Contains(result, "guidance:") {
		t.Error("Empty guidance should be omitted")
	}
	if strings.Contains(result, "recovery_command:") {
		t.Error("Empty recovery_command should be omitted")
	}
	if strings.Contains(result, "context:") {
		t.Error("Empty context should be omitted")
	}
}

func TestYAMLFormatter_FormatTable(t *testing.T) {
	f := &YAMLFormatter{}

	headers := []string{"name", "status", "health"}
	rows := [][]string{
		{"server1", "enabled", "healthy"},
		{"server2", "disabled", "unhealthy"},
	}

	result, err := f.FormatTable(headers, rows)
	if err != nil {
		t.Fatalf("FormatTable() error = %v", err)
	}

	// Verify result is valid YAML
	var parsed []map[string]string
	if yamlErr := yaml.Unmarshal([]byte(result), &parsed); yamlErr != nil {
		t.Fatalf("FormatTable() result is not valid YAML: %v", yamlErr)
	}

	// Verify correct number of rows
	if len(parsed) != 2 {
		t.Errorf("Expected 2 rows, got: %d", len(parsed))
	}

	// Verify first row
	if parsed[0]["name"] != "server1" {
		t.Errorf("Expected name 'server1', got: %v", parsed[0]["name"])
	}
	if parsed[0]["status"] != "enabled" {
		t.Errorf("Expected status 'enabled', got: %v", parsed[0]["status"])
	}
}

func TestYAMLFormatter_FormatTable_Empty(t *testing.T) {
	f := &YAMLFormatter{}

	headers := []string{"a", "b"}
	rows := [][]string{}

	result, err := f.FormatTable(headers, rows)
	if err != nil {
		t.Fatalf("FormatTable() error = %v", err)
	}

	// Empty table should produce empty YAML array
	if result != "[]\n" {
		t.Errorf("Expected empty YAML array '[]\\n', got: %q", result)
	}
}

func TestYAMLFormatter_FormatTable_MismatchedColumns(t *testing.T) {
	f := &YAMLFormatter{}

	headers := []string{"a", "b", "c"}
	rows := [][]string{
		{"1", "2"}, // Missing column c
	}

	result, err := f.FormatTable(headers, rows)
	if err != nil {
		t.Fatalf("FormatTable() error = %v", err)
	}

	// Should handle gracefully, filling missing columns with empty strings
	var parsed []map[string]string
	if yamlErr := yaml.Unmarshal([]byte(result), &parsed); yamlErr != nil {
		t.Fatalf("FormatTable() result is not valid YAML: %v", yamlErr)
	}

	// Column c should be empty
	if parsed[0]["c"] != "" {
		t.Errorf("Expected empty string for missing column 'c', got: %q", parsed[0]["c"])
	}
}
