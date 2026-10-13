package sandbox

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestWrapCommand_ArgvAndEnv(t *testing.T) {
	spec := Spec{
		ReadOnlyPaths:  []string{"/"},
		ReadWritePaths: []string{"/tmp/work"},
		Rlimits:        []Rlimit{{Resource: 7, Cur: 64, Max: 64}},
		BestEffort:     true,
	}
	self := "/opt/mcpproxy/mcpproxy"

	cmd, args, env, err := WrapCommand(self, spec, "/bin/zsh", []string{"-l", "-c", "npx foo"})
	if err != nil {
		t.Fatalf("WrapCommand: %v", err)
	}
	if cmd != self {
		t.Errorf("wrapped command = %q, want self %q", cmd, self)
	}
	wantArgs := []string{Subcommand, "--", "/bin/zsh", "-l", "-c", "npx foo"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("wrapped args = %v, want %v", args, wantArgs)
	}
	if len(env) != 1 || !strings.HasPrefix(env[0], EnvSpec+"=") {
		t.Fatalf("extraEnv = %v, want single %s=... entry", env, EnvSpec)
	}

	// The spec must round-trip through the env back to an identical Spec, so the
	// re-exec child reconstructs exactly what the parent intended.
	t.Setenv(EnvSpec, strings.TrimPrefix(env[0], EnvSpec+"="))
	got, ok, err := SpecFromEnv()
	if err != nil || !ok {
		t.Fatalf("SpecFromEnv: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Errorf("round-tripped spec = %+v, want %+v", got, spec)
	}
}

func TestSpecFromEnv_Absent(t *testing.T) {
	// Snapshot + restore so we can prove the truly-absent case.
	if orig, had := os.LookupEnv(EnvSpec); had {
		t.Cleanup(func() { os.Setenv(EnvSpec, orig) })
	}
	os.Unsetenv(EnvSpec)
	_, ok, err := SpecFromEnv()
	if ok || err != nil {
		t.Fatalf("absent env: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

func TestSpecFromEnv_Malformed(t *testing.T) {
	t.Setenv(EnvSpec, "{not json")
	_, ok, err := SpecFromEnv()
	if !ok {
		t.Errorf("ok = false, want true (var is present even if malformed)")
	}
	if err == nil {
		t.Errorf("err = nil, want decode error for malformed spec")
	}
}
