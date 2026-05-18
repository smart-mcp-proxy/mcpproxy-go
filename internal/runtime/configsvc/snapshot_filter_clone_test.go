package configsvc

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 049: Clone must deep-copy EnabledTools/DisabledTools so a mutation of
// the cloned config cannot corrupt the original snapshot's backing array.
func TestSnapshotClone_DeepCopiesToolFilters(t *testing.T) {
	orig := &config.ServerConfig{
		Name:          "s",
		EnabledTools:  []string{"a", "b"},
		DisabledTools: []string{"x", "y"},
	}
	s := &Snapshot{Config: &config.Config{Servers: []*config.ServerConfig{orig}}}

	cloned := s.Clone()
	cs := cloned.Servers[0]

	// Distinct backing arrays.
	cs.DisabledTools[0] = "MUTATED"
	cs.EnabledTools[0] = "MUTATED"
	if orig.DisabledTools[0] != "x" {
		t.Fatalf("DisabledTools aliased: original corrupted to %v", orig.DisabledTools)
	}
	if orig.EnabledTools[0] != "a" {
		t.Fatalf("EnabledTools aliased: original corrupted to %v", orig.EnabledTools)
	}
	// Values still copied correctly (no data loss).
	cloned2 := s.Clone()
	if len(cloned2.Servers[0].DisabledTools) != 2 || cloned2.Servers[0].DisabledTools[1] != "y" {
		t.Fatalf("DisabledTools not copied: %v", cloned2.Servers[0].DisabledTools)
	}
}
