package output

import (
	"os"
	"testing"
)

// T021: Verify --json alias works same as -o json
func TestResolveFormat_JSONFlag(t *testing.T) {
	tests := []struct {
		name       string
		outputFlag string
		jsonFlag   bool
		want       string
	}{
		{
			name:       "json flag takes precedence",
			outputFlag: "",
			jsonFlag:   true,
			want:       "json",
		},
		{
			name:       "output flag works alone",
			outputFlag: "yaml",
			jsonFlag:   false,
			want:       "yaml",
		},
		{
			name:       "default is table",
			outputFlag: "",
			jsonFlag:   false,
			want:       "table",
		},
		{
			name:       "json flag overrides even when output is set", // mutual exclusivity is handled by Cobra
			outputFlag: "table",
			jsonFlag:   true,
			want:       "json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear env var to ensure test isolation
			os.Unsetenv("MCPPROXY_OUTPUT")

			got := ResolveFormat(tt.outputFlag, tt.jsonFlag)
			if got != tt.want {
				t.Errorf("ResolveFormat(%q, %v) = %q, want %q", tt.outputFlag, tt.jsonFlag, got, tt.want)
			}
		})
	}
}

// T022: Verify MCPPROXY_OUTPUT env var works
func TestResolveFormat_EnvVar(t *testing.T) {
	tests := []struct {
		name       string
		envValue   string
		outputFlag string
		jsonFlag   bool
		want       string
	}{
		{
			name:       "env var used when no flags",
			envValue:   "json",
			outputFlag: "",
			jsonFlag:   false,
			want:       "json",
		},
		{
			name:       "env var yaml",
			envValue:   "yaml",
			outputFlag: "",
			jsonFlag:   false,
			want:       "yaml",
		},
		{
			name:       "output flag overrides env var",
			envValue:   "json",
			outputFlag: "table",
			jsonFlag:   false,
			want:       "table",
		},
		{
			name:       "json flag overrides env var",
			envValue:   "yaml",
			outputFlag: "",
			jsonFlag:   true,
			want:       "json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				os.Setenv("MCPPROXY_OUTPUT", tt.envValue)
				defer os.Unsetenv("MCPPROXY_OUTPUT")
			} else {
				os.Unsetenv("MCPPROXY_OUTPUT")
			}

			got := ResolveFormat(tt.outputFlag, tt.jsonFlag)
			if got != tt.want {
				t.Errorf("ResolveFormat(%q, %v) with MCPPROXY_OUTPUT=%q = %q, want %q",
					tt.outputFlag, tt.jsonFlag, tt.envValue, got, tt.want)
			}
		})
	}
}

func TestNewFormatter(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		wantErr bool
	}{
		{"json format", "json", false},
		{"JSON uppercase", "JSON", false},
		{"yaml format", "yaml", false},
		{"table format", "table", false},
		{"empty default", "", false},
		{"invalid format", "invalid", true},
		{"csv not supported", "csv", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := NewFormatter(tt.format)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewFormatter(%q) error = %v, wantErr %v", tt.format, err, tt.wantErr)
				return
			}
			if !tt.wantErr && f == nil {
				t.Errorf("NewFormatter(%q) returned nil formatter", tt.format)
			}
		})
	}
}

func TestNewFormatter_JSONType(t *testing.T) {
	f, err := NewFormatter("json")
	if err != nil {
		t.Fatalf("NewFormatter(json) error = %v", err)
	}
	if _, ok := f.(*JSONFormatter); !ok {
		t.Errorf("NewFormatter(json) returned %T, want *JSONFormatter", f)
	}
}

func TestNewFormatter_YAMLType(t *testing.T) {
	f, err := NewFormatter("yaml")
	if err != nil {
		t.Fatalf("NewFormatter(yaml) error = %v", err)
	}
	if _, ok := f.(*YAMLFormatter); !ok {
		t.Errorf("NewFormatter(yaml) returned %T, want *YAMLFormatter", f)
	}
}

func TestNewFormatter_TableType(t *testing.T) {
	f, err := NewFormatter("table")
	if err != nil {
		t.Fatalf("NewFormatter(table) error = %v", err)
	}
	if _, ok := f.(*TableFormatter); !ok {
		t.Errorf("NewFormatter(table) returned %T, want *TableFormatter", f)
	}
}

func TestNewFormatter_NoColorSupport(t *testing.T) {
	// Test NO_COLOR env var is respected
	os.Setenv("NO_COLOR", "1")
	defer os.Unsetenv("NO_COLOR")

	f, err := NewFormatter("table")
	if err != nil {
		t.Fatalf("NewFormatter(table) error = %v", err)
	}

	tableFormatter, ok := f.(*TableFormatter)
	if !ok {
		t.Fatalf("NewFormatter(table) returned %T, want *TableFormatter", f)
	}

	if !tableFormatter.NoColor {
		t.Error("Expected NoColor to be true when NO_COLOR=1")
	}
}
