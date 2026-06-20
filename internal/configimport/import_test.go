package configimport

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func TestImport(t *testing.T) {
	now := time.Date(2026, 1, 17, 12, 0, 0, 0, time.UTC)

	t.Run("import_claude_desktop", func(t *testing.T) {
		content, err := os.ReadFile("testdata/claude_desktop.json")
		if err != nil {
			t.Fatalf("failed to read test file: %v", err)
		}

		result, err := Import(content, &ImportOptions{Now: now})
		if err != nil {
			t.Fatalf("Import() error = %v", err)
		}

		if result.Format != FormatClaudeDesktop {
			t.Errorf("Format = %v, want %v", result.Format, FormatClaudeDesktop)
		}
		if result.Summary.Imported != 3 {
			t.Errorf("Summary.Imported = %d, want 3", result.Summary.Imported)
		}
		if result.Summary.Total != 3 {
			t.Errorf("Summary.Total = %d, want 3", result.Summary.Total)
		}

		// All servers should be quarantined
		for _, s := range result.Imported {
			if !s.Server.Quarantined {
				t.Errorf("server %s should be quarantined", s.Server.Name)
			}
		}
	})

	t.Run("import_codex_toml", func(t *testing.T) {
		content, err := os.ReadFile("testdata/codex.toml")
		if err != nil {
			t.Fatalf("failed to read test file: %v", err)
		}

		result, err := Import(content, &ImportOptions{Now: now})
		if err != nil {
			t.Fatalf("Import() error = %v", err)
		}

		if result.Format != FormatCodex {
			t.Errorf("Format = %v, want %v", result.Format, FormatCodex)
		}
		if result.Summary.Imported != 5 {
			t.Errorf("Summary.Imported = %d, want 5", result.Summary.Imported)
		}
	})

	t.Run("import_with_format_hint", func(t *testing.T) {
		content, err := os.ReadFile("testdata/claude_desktop.json")
		if err != nil {
			t.Fatalf("failed to read test file: %v", err)
		}

		result, err := Import(content, &ImportOptions{
			FormatHint: FormatClaudeDesktop,
			Now:        now,
		})
		if err != nil {
			t.Fatalf("Import() error = %v", err)
		}

		if result.Format != FormatClaudeDesktop {
			t.Errorf("Format = %v, want %v", result.Format, FormatClaudeDesktop)
		}
	})

	t.Run("skip_existing_servers", func(t *testing.T) {
		content, err := os.ReadFile("testdata/claude_desktop.json")
		if err != nil {
			t.Fatalf("failed to read test file: %v", err)
		}

		result, err := Import(content, &ImportOptions{
			ExistingServers: []string{"github"},
			Now:             now,
		})
		if err != nil {
			t.Fatalf("Import() error = %v", err)
		}

		if result.Summary.Imported != 2 {
			t.Errorf("Summary.Imported = %d, want 2", result.Summary.Imported)
		}
		if result.Summary.Skipped != 1 {
			t.Errorf("Summary.Skipped = %d, want 1", result.Summary.Skipped)
		}

		// Check skipped reason
		for _, s := range result.Skipped {
			if s.Name == "github" {
				if s.Reason != "already_exists" {
					t.Errorf("skipped reason = %s, want already_exists", s.Reason)
				}
				return
			}
		}
		t.Error("github should be in skipped list")
	})

	t.Run("filter_servers", func(t *testing.T) {
		content, err := os.ReadFile("testdata/claude_desktop.json")
		if err != nil {
			t.Fatalf("failed to read test file: %v", err)
		}

		result, err := Import(content, &ImportOptions{
			ServerNames: []string{"github"},
			Now:         now,
		})
		if err != nil {
			t.Fatalf("Import() error = %v", err)
		}

		if result.Summary.Imported != 1 {
			t.Errorf("Summary.Imported = %d, want 1", result.Summary.Imported)
		}
		if result.Summary.Skipped != 2 {
			t.Errorf("Summary.Skipped = %d, want 2 (filtered out)", result.Summary.Skipped)
		}

		if result.Imported[0].Server.Name != "github" {
			t.Errorf("imported server name = %s, want github", result.Imported[0].Server.Name)
		}
	})

	t.Run("filter_nonexistent_server", func(t *testing.T) {
		content, err := os.ReadFile("testdata/claude_desktop.json")
		if err != nil {
			t.Fatalf("failed to read test file: %v", err)
		}

		result, err := Import(content, &ImportOptions{
			ServerNames: []string{"nonexistent"},
			Now:         now,
		})
		if err != nil {
			t.Fatalf("Import() error = %v", err)
		}

		// Should have warning about nonexistent server
		hasWarning := false
		for _, w := range result.Warnings {
			if w == "requested server 'nonexistent' not found in config" {
				hasWarning = true
				break
			}
		}
		if !hasWarning {
			t.Error("should have warning about nonexistent server")
		}
	})

	t.Run("invalid_json", func(t *testing.T) {
		content, _ := os.ReadFile("testdata/invalid.json")
		_, err := Import(content, nil)
		if err == nil {
			t.Error("Import() should return error for invalid JSON")
		}
	})

	t.Run("empty_servers", func(t *testing.T) {
		content, _ := os.ReadFile("testdata/empty.json")
		_, err := Import(content, nil)
		if err == nil {
			t.Error("Import() should return error for empty servers")
		}
	})

	t.Run("default_options", func(t *testing.T) {
		content, err := os.ReadFile("testdata/claude_desktop.json")
		if err != nil {
			t.Fatalf("failed to read test file: %v", err)
		}

		result, err := Import(content, nil)
		if err != nil {
			t.Fatalf("Import() error = %v", err)
		}

		if result.Summary.Imported != 3 {
			t.Errorf("Summary.Imported = %d, want 3", result.Summary.Imported)
		}

		// Created time should be set
		for _, s := range result.Imported {
			if s.Server.Created.IsZero() {
				t.Errorf("server %s should have Created time set", s.Server.Name)
			}
		}
	})
}

func TestPreview(t *testing.T) {
	content, err := os.ReadFile("testdata/claude_desktop.json")
	if err != nil {
		t.Fatalf("failed to read test file: %v", err)
	}

	result, err := Preview(content, nil)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}

	if result.Summary.Imported != 3 {
		t.Errorf("Summary.Imported = %d, want 3", result.Summary.Imported)
	}
}

func TestGetAvailableServerNames(t *testing.T) {
	content, err := os.ReadFile("testdata/claude_desktop.json")
	if err != nil {
		t.Fatalf("failed to read test file: %v", err)
	}

	names, err := GetAvailableServerNames(content, FormatUnknown)
	if err != nil {
		t.Fatalf("GetAvailableServerNames() error = %v", err)
	}

	if len(names) != 3 {
		t.Errorf("got %d names, want 3", len(names))
	}

	// Check that expected names are present
	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}

	expected := []string{"github", "filesystem", "memory"}
	for _, e := range expected {
		if !nameSet[e] {
			t.Errorf("expected server %s not found", e)
		}
	}
}

func TestImport_SkipQuarantine(t *testing.T) {
	now := time.Date(2026, 1, 17, 12, 0, 0, 0, time.UTC)

	t.Run("default_quarantined", func(t *testing.T) {
		content, err := os.ReadFile("testdata/claude_desktop.json")
		if err != nil {
			t.Fatalf("failed to read test file: %v", err)
		}

		result, err := Import(content, &ImportOptions{Now: now})
		if err != nil {
			t.Fatalf("Import() error = %v", err)
		}

		for _, s := range result.Imported {
			if !s.Server.Quarantined {
				t.Errorf("server %s should be quarantined by default", s.Server.Name)
			}
		}
	})

	t.Run("skip_quarantine", func(t *testing.T) {
		content, err := os.ReadFile("testdata/claude_desktop.json")
		if err != nil {
			t.Fatalf("failed to read test file: %v", err)
		}

		result, err := Import(content, &ImportOptions{
			SkipQuarantine: true,
			Now:            now,
		})
		if err != nil {
			t.Fatalf("Import() error = %v", err)
		}

		if result.Summary.Imported == 0 {
			t.Fatal("expected at least one imported server")
		}

		for _, s := range result.Imported {
			if s.Server.Quarantined {
				t.Errorf("server %s should NOT be quarantined when SkipQuarantine=true", s.Server.Name)
			}
		}
	})
}

func TestImport_DuplicateWithinSameImport(t *testing.T) {
	// Create a config with duplicate names that would result after sanitization
	content := []byte(`{
		"mcpServers": {
			"server-a": {
				"command": "cmd1"
			},
			"server-b": {
				"command": "cmd2"
			}
		}
	}`)

	result, err := Import(content, nil)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	// Both should be imported since they have different names
	if result.Summary.Imported != 2 {
		t.Errorf("Summary.Imported = %d, want 2", result.Summary.Imported)
	}
}

// TestImport_FilterMatchesSanitizedName reproduces the two-step preview -> import
// flow used by the Web UI for a server whose name needs sanitizing (a space).
// The preview surfaces the sanitized name ("Figma_Desktop"), so the actual
// import call carries that sanitized name in ServerNames. The filter must match
// it; previously it compared against the un-sanitized "Figma Desktop" and the
// server was silently dropped ("0 servers added").
func TestImport_FilterMatchesSanitizedName(t *testing.T) {
	now := time.Date(2026, 1, 17, 12, 0, 0, 0, time.UTC)
	content := []byte(`{
		"mcpServers": {
			"Figma Desktop": {
				"url": "http://127.0.0.1:3845/mcp"
			}
		}
	}`)

	// Step 1: preview (no filter) — this is what the UI shows as "Will import 1".
	preview, err := Import(content, &ImportOptions{Now: now})
	if err != nil {
		t.Fatalf("preview Import() error = %v", err)
	}
	if preview.Summary.Imported != 1 {
		t.Fatalf("preview Summary.Imported = %d, want 1", preview.Summary.Imported)
	}
	previewedName := preview.Imported[0].Server.Name
	if previewedName != "Figma_Desktop" {
		t.Fatalf("previewed server name = %q, want %q", previewedName, "Figma_Desktop")
	}

	// OriginalName must carry the raw source name (the httpapi rename map keys
	// off it), not the sanitized name.
	if got := preview.Imported[0].OriginalName; got != "Figma Desktop" {
		t.Errorf("preview OriginalName = %q, want %q", got, "Figma Desktop")
	}

	// Step 2a: Web UI path — filter by the sanitized name the UI selected.
	// Step 2b: CLI path — filter by the raw source name passed to `--server`.
	// Both must import the server.
	for _, tc := range []struct {
		name   string
		filter string
	}{
		{"webui_sanitized_name", previewedName}, // "Figma_Desktop"
		{"cli_raw_name", "Figma Desktop"},       // verbatim --server value
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Import(content, &ImportOptions{
				ServerNames: []string{tc.filter},
				Now:         now,
			})
			if err != nil {
				t.Fatalf("Import() error = %v", err)
			}

			if result.Summary.Imported != 1 {
				t.Errorf("Summary.Imported = %d, want 1 (regression: server dropped despite filter match on %q)", result.Summary.Imported, tc.filter)
			}
			if result.Summary.Skipped != 0 {
				t.Errorf("Summary.Skipped = %d, want 0", result.Summary.Skipped)
			}
			// No spurious "requested server not found" warning.
			for _, w := range result.Warnings {
				if w == fmt.Sprintf("requested server '%s' not found in config", tc.filter) {
					t.Errorf("unexpected not-found warning for filter %q", tc.filter)
				}
			}
			if len(result.Imported) != 1 {
				t.Fatalf("len(Imported) = %d, want 1", len(result.Imported))
			}
			if result.Imported[0].Server.Name != "Figma_Desktop" {
				t.Errorf("imported server name = %q, want %q", result.Imported[0].Server.Name, "Figma_Desktop")
			}
			if result.Imported[0].OriginalName != "Figma Desktop" {
				t.Errorf("imported OriginalName = %q, want %q", result.Imported[0].OriginalName, "Figma Desktop")
			}
			if result.Imported[0].Server.URL != "http://127.0.0.1:3845/mcp" {
				t.Errorf("imported server URL = %q, want the Figma URL", result.Imported[0].Server.URL)
			}
		})
	}
}
