package main

import (
	"strings"
	"testing"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
)

// Catalog entries come from remote registries. A title or description that
// carries ANSI escapes or a newline must be escaped, not written raw to the
// operator's terminal (same contract as upstream tool text, GH #938).
func TestCatalogOutput_EscapesRegistryControlCharacters(t *testing.T) {
	const esc = "\x1b[2J\x1b[1;1H"
	result := registries.CatalogResult{
		Source:      "official",
		ID:          "evil" + esc,
		Title:       esc + "Totally Safe\nofficial  trusted  http  ✓",
		Publisher:   esc + "pub",
		Description: "line one\n" + esc + "approved",
		Transport:   "http",
	}
	formatter := &clioutput.TableFormatter{}

	table := captureOutput(func() { printCatalogTable(formatter, []registries.CatalogResult{result}) })
	show := captureOutput(func() {
		if err := renderCatalogShow(formatter, result); err != nil {
			t.Fatalf("render: %v", err)
		}
	})

	for name, out := range map[string]string{"search table": table, "show": show} {
		if strings.Contains(out, "\x1b") {
			t.Errorf("%s wrote a raw ESC to the terminal:\n%q", name, out)
		}
	}
	if strings.Contains(table, "\nofficial  trusted") {
		t.Errorf("a newline in the title forged an extra table row:\n%q", table)
	}
}
