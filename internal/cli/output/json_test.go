package output

import (
	"encoding/json"
	"testing"
)

// T011: Unit test for JSONFormatter.Format()
func TestJSONFormatter_Format(t *testing.T) {
	f := &JSONFormatter{Indent: true}

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
				Name   string `json:"name"`
				Status string `json:"status"`
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
				// Verify result is valid JSON
				var parsed interface{}
				if err := json.Unmarshal([]byte(result), &parsed); err != nil {
					t.Errorf("Format() result is not valid JSON: %v", err)
				}
			}
		})
	}
}

// T011: Test indentation
func TestJSONFormatter_Format_Indentation(t *testing.T) {
	data := map[string]string{"key": "value"}

	// With indentation
	fIndent := &JSONFormatter{Indent: true}
	resultIndent, err := fIndent.Format(data)
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}
	if resultIndent == `{"key":"value"}` {
		t.Error("Expected indented output but got compact")
	}

	// Without indentation
	fCompact := &JSONFormatter{Indent: false}
	resultCompact, err := fCompact.Format(data)
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}
	if resultCompact != `{"key":"value"}` {
		t.Errorf("Expected compact output, got: %s", resultCompact)
	}
}

// T012: Unit test for JSONFormatter.FormatError()
func TestJSONFormatter_FormatError(t *testing.T) {
	f := &JSONFormatter{Indent: true}

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

	// Verify result is valid JSON
	var parsed map[string]interface{}
	if jsonErr := json.Unmarshal([]byte(result), &parsed); jsonErr != nil {
		t.Fatalf("FormatError() result is not valid JSON: %v", jsonErr)
	}

	// Verify required fields
	if parsed["code"] != "TEST_ERROR" {
		t.Errorf("Expected code 'TEST_ERROR', got: %v", parsed["code"])
	}
	if parsed["message"] != "Test error message" {
		t.Errorf("Expected message 'Test error message', got: %v", parsed["message"])
	}
	if parsed["guidance"] != "Try doing X instead" {
		t.Errorf("Expected guidance 'Try doing X instead', got: %v", parsed["guidance"])
	}
	if parsed["recovery_command"] != "mcpproxy test" {
		t.Errorf("Expected recovery_command 'mcpproxy test', got: %v", parsed["recovery_command"])
	}
}

// T013: Unit test for JSONFormatter.FormatTable()
func TestJSONFormatter_FormatTable(t *testing.T) {
	f := &JSONFormatter{Indent: true}

	headers := []string{"name", "status", "health"}
	rows := [][]string{
		{"server1", "enabled", "healthy"},
		{"server2", "disabled", "unhealthy"},
	}

	result, err := f.FormatTable(headers, rows)
	if err != nil {
		t.Fatalf("FormatTable() error = %v", err)
	}

	// Verify result is valid JSON array
	var parsed []map[string]string
	if jsonErr := json.Unmarshal([]byte(result), &parsed); jsonErr != nil {
		t.Fatalf("FormatTable() result is not valid JSON array: %v", jsonErr)
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

// T014: Unit test for empty array output (not null)
func TestJSONFormatter_EmptyArray(t *testing.T) {
	f := &JSONFormatter{Indent: true}

	// Empty slice should return [] not null
	result, err := f.Format([]string{})
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}

	// Should be "[]" not "null"
	if result != "[]" {
		t.Errorf("Expected '[]' for empty slice, got: %s", result)
	}

	// Empty table should return []
	resultTable, err := f.FormatTable([]string{"a", "b"}, [][]string{})
	if err != nil {
		t.Fatalf("FormatTable() error = %v", err)
	}
	if resultTable != "[]" {
		t.Errorf("Expected '[]' for empty table, got: %s", resultTable)
	}
}

// Test snake_case field names
func TestJSONFormatter_SnakeCase(t *testing.T) {
	f := &JSONFormatter{Indent: false}

	err := StructuredError{
		Code:            "TEST",
		Message:         "test",
		RecoveryCommand: "cmd",
	}

	result, formatErr := f.FormatError(err)
	if formatErr != nil {
		t.Fatalf("FormatError() error = %v", formatErr)
	}

	// Verify snake_case is used (recovery_command not recoveryCommand)
	if !contains(result, `"recovery_command"`) {
		t.Errorf("Expected snake_case 'recovery_command' in output: %s", result)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
