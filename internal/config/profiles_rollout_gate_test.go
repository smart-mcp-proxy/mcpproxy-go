package config

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPolicyEnforcementReady_RollsOutGate pins the FR-009a rollout gate: with
// PolicyEnforcementReady() false (the value shipped from 108-a until
// 108-d), each of the six v3 policy fields AND a non-empty
// anonymous_profile (known or unknown, data-model.md §1) are a fatal
// validation error with the exact data-model.md §1 text; a legacy profile
// still loads. 108-d's T055a asserts the constant flips to true and inverts
// this test.
func TestPolicyEnforcementReady_RollsOutGate(t *testing.T) {
	require.False(t, PolicyEnforcementReady(), "must ship false until 108-d lands the last execution gate (FR-009a)")

	trueVal := true
	falseVal := false
	switchTo := []string{"other"}

	cases := []struct {
		field string
		set   func(p *ProfileConfig)
	}{
		{"max_tier", func(p *ProfileConfig) { p.MaxTier = "read" }},
		{"unannotated", func(p *ProfileConfig) { p.Unannotated = "deny" }},
		{"tools", func(p *ProfileConfig) { p.Tools = &ProfileToolRules{Allow: []string{"a:list"}} }},
		{"code_execution", func(p *ProfileConfig) { p.CodeExecution = &falseVal }},
		{"management_tools", func(p *ProfileConfig) { p.ManagementTools = &trueVal }},
		{"switchable_to", func(p *ProfileConfig) { p.SwitchableTo = &switchTo }},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			p := ProfileConfig{Name: "prof", Servers: []string{"a"}}
			tc.set(&p)
			cfg := &Config{Profiles: []ProfileConfig{p, {Name: "other", Servers: []string{"a"}}}}

			_, err := ValidateProfiles(cfg)
			require.EqualError(t, err, "profiles[0]: "+tc.field+" is not supported by this build (Profiles v3 enforcement incomplete)")
		})
	}

	t.Run("legacy profile still loads", func(t *testing.T) {
		cfg := &Config{Servers: []*ServerConfig{{Name: "a"}}, Profiles: []ProfileConfig{{Name: "legacy", Servers: []string{"a"}, Title: "Legacy"}}}
		_, err := ValidateProfiles(cfg)
		require.NoError(t, err)
	})

	t.Run("anonymous_profile naming a known profile is fatal while the gate is closed", func(t *testing.T) {
		cfg := &Config{Servers: []*ServerConfig{{Name: "a"}}, AnonymousProfile: "legacy", Profiles: []ProfileConfig{{Name: "legacy", Servers: []string{"a"}}}}
		_, err := ValidateProfiles(cfg)
		require.EqualError(t, err, "anonymous_profile is not supported by this build (Profiles v3 enforcement incomplete)")
	})

	t.Run("anonymous_profile naming an unknown profile is fatal while the gate is closed", func(t *testing.T) {
		cfg := &Config{AnonymousProfile: "missing"}
		_, err := ValidateProfiles(cfg)
		require.EqualError(t, err, "anonymous_profile is not supported by this build (Profiles v3 enforcement incomplete)")
	})

	t.Run("anonymous_profile is fatal even with zero profiles configured", func(t *testing.T) {
		cfg := &Config{AnonymousProfile: "legacy"}
		_, err := ValidateProfiles(cfg)
		require.EqualError(t, err, "anonymous_profile is not supported by this build (Profiles v3 enforcement incomplete)")
	})

	t.Run("test-only override admits v3 fields", func(t *testing.T) {
		EnablePolicyForTest(t)
		cfg := &Config{Profiles: []ProfileConfig{{Name: "prof", Servers: []string{"a"}, MaxTier: "read"}}}
		_, err := ValidateProfiles(cfg)
		require.NoError(t, err)
	})

	t.Run("test-only override admits anonymous_profile", func(t *testing.T) {
		EnablePolicyForTest(t)
		cfg := &Config{Servers: []*ServerConfig{{Name: "a"}}, AnonymousProfile: "legacy", Profiles: []ProfileConfig{{Name: "legacy", Servers: []string{"a"}}}}
		warnings, err := ValidateProfiles(cfg)
		require.NoError(t, err)
		require.Empty(t, warnings)
	})

	t.Run("override closes the gate again once the test that opened it ends", func(t *testing.T) {
		require.False(t, PolicyEnforcementReady())
		t.Run("nested", func(t *testing.T) {
			EnablePolicyForTest(t)
			require.True(t, PolicyEnforcementReady())
		})
		require.False(t, PolicyEnforcementReady(), "tb.Cleanup from the nested subtest must have closed the gate")
	})
}

// TestEnablePolicyForTest_PanicsOutsideATestBinary builds a plain `main`
// package that calls config.EnablePolicyForTest(nil) and runs it with `go
// build`+exec (not `go test`), asserting the override is unreachable outside
// a test binary: testing.Testing() is false there, so the panic fires (T004a
// item 2, zcode review round 2: "the override cannot ship").
func TestEnablePolicyForTest_PanicsOutsideATestBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping go build subprocess probe in -short mode")
	}
	bin := filepath.Join(t.TempDir(), "overrideprobe")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, "./testdata/overrideprobe")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build overrideprobe: %v\n%s", err, out)
	}

	out, err := exec.Command(bin).CombinedOutput()
	require.Error(t, err, "overrideprobe must exit non-zero: EnablePolicyForTest must panic outside a test binary")
	require.Contains(t, string(out), "EnablePolicyForTest called outside a test binary")
}

// TestServeRejectsV3PolicyFieldsAtStartup is T004a's item 3 (zcode review
// round 2, "the override cannot ship" — the third of the three artifacts
// tasks.md lists, still missing after round 2 added only item 2 above): a
// BINARY-level check that the FR-009a gate cannot be opened by any runtime
// input. It builds the real ./cmd/mcpproxy binary (not `go test`) and runs
// `serve` against a scratch --data-dir/--config (high port, scratch HOME)
// containing first a profile with a v3 policy field (max_tier) and then a
// bare anonymous_profile, asserting exit code 4 (ExitCodeConfigError,
// cmd/mcpproxy/exit_codes.go) and the exact data-model.md §1 refusal text on
// stderr in both cases. A handful of plausible-looking MCPPROXY_* env vars
// are set to arbitrary values in the child's environment (a deliberately
// minimal env otherwise — no inherited variable can smuggle the gate open
// either): none of them is read by PolicyEnforcementReady (its only inputs
// are the policyEnforcementReadyBase compile-time const and the test-only
// override that TestEnablePolicyForTest_PanicsOutsideATestBinary above
// proves is unreachable outside `go test`), so none of them can open it.
func TestServeRejectsV3PolicyFieldsAtStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping go build + subprocess serve probe in -short mode")
	}

	bin := filepath.Join(t.TempDir(), "mcpproxy-gateprobe")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, "github.com/smart-mcp-proxy/mcpproxy-go/cmd/mcpproxy")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/mcpproxy: %v\n%s", err, out)
	}

	cases := []struct {
		name       string
		configJSON string
		wantStderr string
	}{
		{
			name:       "v3 policy field (max_tier)",
			configJSON: `{"profiles": [{"name": "prof", "servers": [], "max_tier": "read"}]}`,
			wantStderr: "profiles[0]: max_tier is not supported by this build (Profiles v3 enforcement incomplete)",
		},
		{
			name:       "anonymous_profile",
			configJSON: `{"anonymous_profile": "someone"}`,
			wantStderr: "anonymous_profile is not supported by this build (Profiles v3 enforcement incomplete)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			home := filepath.Join(dir, "home")
			dataDir := filepath.Join(dir, "data")
			require.NoError(t, os.MkdirAll(home, 0o700))
			require.NoError(t, os.MkdirAll(dataDir, 0o700))

			configPath := filepath.Join(dir, "config.json")
			require.NoError(t, os.WriteFile(configPath, []byte(tc.configJSON), 0o600))

			//nolint:gosec // test-only: fixed binary this test just built, fixed args
			cmd := exec.Command(bin, "serve",
				"--config", configPath,
				"--data-dir", dataDir,
				"--listen", fmt.Sprintf("127.0.0.1:%d", freeHighPort(t)),
			)
			cmd.Env = arbitraryServeEnv(home)

			out, err := cmd.CombinedOutput()
			var exitErr *exec.ExitError
			require.Error(t, err, "serve must exit non-zero when the FR-009a gate refuses the config; output:\n%s", out)
			require.ErrorAs(t, err, &exitErr, "output:\n%s", out)
			require.Equal(t, 4, exitErr.ExitCode(), "must exit ExitCodeConfigError (cmd/mcpproxy/exit_codes.go); output:\n%s", out)
			require.Contains(t, string(out), tc.wantStderr)
		})
	}
}

// freeHighPort picks a free high port for a scratch --listen so the probe
// binary never collides with a developer's real proxy or another test.
func freeHighPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// arbitraryServeEnv builds a deliberately minimal, arbitrary environment for
// the probe binary: just enough (HOME/PATH, plus TMPDIR/TEMP so the Go
// runtime and OS calls the binary makes have somewhere to write) to run at
// all, pointed at a scratch home so the boot path's early DataDir fallback
// (internal/config/loader.go, before the --data-dir flag override applies)
// can never touch the real ~/.mcpproxy, PLUS a handful of MCPPROXY_*
// variables with names that sound like they might matter here and values
// that very much would if they did — proving none of them opens the FR-009a
// gate.
func arbitraryServeEnv(home string) []string {
	env := []string{
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		"MCPPROXY_API_KEY=arbitrary-test-key",
		"MCPPROXY_DEBUG=true",
		"MCPPROXY_LISTEN=0.0.0.0:0",
		"MCPPROXY_TELEMETRY=false",
		"MCPPROXY_POLICY_ENFORCEMENT_READY=true",
		"MCPPROXY_ENABLE_PROFILES_V3=true",
		"MCPPROXY_ANONYMOUS_PROFILE=legacy",
	}
	if runtime.GOOS == "windows" {
		env = append(env, "USERPROFILE="+home, "TEMP="+os.Getenv("TEMP"), "TMP="+os.Getenv("TMP"))
	} else {
		env = append(env, "TMPDIR="+os.Getenv("TMPDIR"))
	}
	return env
}
