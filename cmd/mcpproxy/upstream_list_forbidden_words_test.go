package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Spec 109-m SC-003 (T146, M7): for every health status that is not `ready`
// (usable=false), the `upstream list` STATUS column never reads healthy,
// online or connected, even when the legacy fields still say so (level
// "healthy", a free-text summary starting "Connected"). The Web card, detail
// header, macOS row and tray first line have the same check in their own test
// suites; this is the Go CLI renderer.

var p109ForbiddenForUnusable = regexp.MustCompile(`(?i)\b(healthy|online|connected)\b`)

func TestUpstreamListForbiddenWords(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "health", "testdata", "status_fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		StatusOrder []string `json:"status_order"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}

	checked := 0
	for _, status := range fixtures.StatusOrder {
		if status == "ready" {
			continue
		}
		checked++
		// The worst case: a legacy severity of "healthy" and a summary that
		// still says connected, which is exactly what a renderer that prints
		// `level` or `summary` would leak.
		rows := upstreamServerRows([]map[string]interface{}{{
			"name":       "srv-" + status,
			"protocol":   "http",
			"tool_count": float64(5),
			"health": healthFixture(status, false, []interface{}{}, map[string]interface{}{
				"level":   "healthy",
				"summary": "Connected (5 tools)",
			}),
		}})
		if len(rows) != 1 {
			t.Fatalf("status %s: expected one row, got %d", status, len(rows))
		}
		statusCell := rows[0][4]
		if p109ForbiddenForUnusable.MatchString(statusCell) {
			t.Errorf("status %s renders STATUS %q: unusable rows must never read healthy, online or connected", status, statusCell)
		}
	}
	if checked != len(fixtures.StatusOrder)-1 {
		t.Fatalf("expected every non-ready status to be checked, got %d of %d", checked, len(fixtures.StatusOrder)-1)
	}
}

// TestUpstreamListForbiddenWordsBites is the mutation check: a renderer that
// printed `level` or the connected summary instead of the status label would
// fail the check above, and a ready row legitimately reads Online.
func TestUpstreamListForbiddenWordsBites(t *testing.T) {
	for _, leaked := range []string{"healthy", "Connected (5 tools)", "Online", "connected"} {
		if !p109ForbiddenForUnusable.MatchString(leaked) {
			t.Errorf("the forbidden-word pattern must match %q", leaked)
		}
	}
	rows := upstreamServerRows([]map[string]interface{}{{
		"name": "ok", "protocol": "stdio", "tool_count": float64(2),
		"health": healthFixture("ready", true, []interface{}{}, nil),
	}})
	if rows[0][4] != "Online" {
		t.Errorf("a ready row reads Online, got %q", rows[0][4])
	}
}
