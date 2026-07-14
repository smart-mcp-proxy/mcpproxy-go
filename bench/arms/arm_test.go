package arms

import (
	"errors"
	"fmt"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// stubArm is a minimal Arm for registry tests.
type stubArm struct {
	name        string
	availureErr error
}

func (s *stubArm) Name() string        { return s.name }
func (s *stubArm) IndexAltering() bool { return false }
func (s *stubArm) LowerBound() bool    { return false }
func (s *stubArm) EncodeTool(bench.Tool) (string, error) {
	return "", nil
}
func (s *stubArm) EncodeListing([]bench.Tool) (string, error) {
	return "", nil
}
func (s *stubArm) EncodeIndexMetadata(t bench.Tool) (config.ToolMetadata, error) {
	return config.ToolMetadata{Name: t.Name, ServerName: t.Server, Description: t.Description, ParamsJSON: string(t.Schema)}, nil
}
func (s *stubArm) Available() error { return s.availureErr }

func TestRegistry_RejectsDuplicateNames(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubArm{name: "stub_arm"}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register(&stubArm{name: "stub_arm"}); err == nil {
		t.Fatal("duplicate Register must fail")
	}
}

func TestRegistry_RejectsInvalidNames(t *testing.T) {
	r := NewRegistry()
	for _, bad := range []string{"", "Baseline", "has-dash", "has space", "ünïcode"} {
		if err := r.Register(&stubArm{name: bad}); err == nil {
			t.Errorf("Register(%q) must fail: names are lowercase snake_case", bad)
		}
	}
}

func TestRegistry_ResolveUnknownArm(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Resolve("no_such_arm"); err == nil {
		t.Fatal("Resolve of unregistered arm must fail")
	}
}

// TestRegistry_ResolveSurfacesUnavailability enforces contract rule 5: a
// registered arm whose runtime is missing reports ErrArmUnavailable at
// registry-resolution time, before any tool is processed.
func TestRegistry_ResolveSurfacesUnavailability(t *testing.T) {
	r := NewRegistry()
	reason := fmt.Errorf("%w: node runtime not found in PATH", ErrArmUnavailable)
	if err := r.Register(&stubArm{name: "needs_node", availureErr: reason}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, err := r.Resolve("needs_node")
	if err == nil {
		t.Fatal("Resolve must surface arm unavailability")
	}
	if !errors.Is(err, ErrArmUnavailable) {
		t.Errorf("Resolve error must wrap ErrArmUnavailable, got: %v", err)
	}
}

func TestRegistry_NamesSortedDeterministic(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"zeta_arm", "alpha_arm", "mid_arm"} {
		if err := r.Register(&stubArm{name: n}); err != nil {
			t.Fatalf("Register(%s): %v", n, err)
		}
	}
	got := r.Names()
	want := []string{"alpha_arm", "mid_arm", "zeta_arm"}
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names() = %v, want %v (sorted)", got, want)
		}
	}
}

// TestDefaultRegistry_BaselineRegistered pins the mandatory baseline arm in
// the package default registry other arms join later (T018-T021).
func TestDefaultRegistry_BaselineRegistered(t *testing.T) {
	arm, err := Resolve("baseline_json")
	if err != nil {
		t.Fatalf("default registry must resolve baseline_json: %v", err)
	}
	if arm.Name() != "baseline_json" {
		t.Errorf("resolved arm name = %q", arm.Name())
	}
}
