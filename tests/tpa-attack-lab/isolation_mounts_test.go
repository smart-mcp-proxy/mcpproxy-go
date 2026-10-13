package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/core"
)

// labCheckedInConfig decodes tests/tpa-lab-config-docker.json over the
// daemon's defaults, the way the loader does, optionally after mutating the
// raw docker_isolation block.
func labCheckedInConfig(t *testing.T, mutate func(glob map[string]interface{})) (*config.Config, string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "tpa-lab-config-docker.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		var raw map[string]interface{}
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatal(err)
		}
		mutate(raw["docker_isolation"].(map[string]interface{}))
		if data, err = json.Marshal(raw); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		cfg = nil // the daemon would refuse it too; only the preflight runs
	}
	path := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg, path
}

// sharedMounts returns every volume/mount argument of a docker run.
func sharedMounts(args []string) []string {
	var out []string
	for i, a := range args {
		switch {
		case a == "-v" || a == "--volume" || a == "--mount" || a == "--volumes-from":
			if i+1 < len(args) {
				out = append(out, a+" "+args[i+1])
			} else {
				out = append(out, a)
			}
		case strings.HasPrefix(a, "--volume=") || strings.HasPrefix(a, "--mount=") || strings.HasPrefix(a, "--volumes-from="):
			out = append(out, a)
		}
	}
	return out
}

// TestLabDockerArgsHaveNoSharedMounts (Sol r14 P2): the launcher mounts
// shared, persistent package-cache volumes (mcpproxy-uv-cache,
// mcpproxy-npm-cache, ...) when docker_isolation.enable_cache_volume is true,
// which is also the daemon default. A lab upstream must never get one, for
// any runtime: it could read or poison caches used by ordinary upstreams.
func TestLabDockerArgsHaveNoSharedMounts(t *testing.T) {
	cfg, path := labCheckedInConfig(t, nil)
	im := core.NewIsolationManager(cfg.DockerIsolation)
	for _, s := range cfg.Servers {
		for _, rt := range []string{im.DetectRuntimeType(s.Command), "python", "uvx", "npx", "node", "go"} {
			args, err := im.BuildDockerArgs(s, rt)
			if err != nil {
				t.Fatalf("%s/%s: %v", s.Name, rt, err)
			}
			if m := sharedMounts(args); len(m) > 0 {
				t.Errorf("%s runtime %s: lab docker run carries shared mounts %v", s.Name, rt, m)
			}
		}
	}
	lib := verdictsLib(t)
	if out, code := runLib(t, lib, `lab_config_check_isolation "$1" 2>&1`, path); code != 0 {
		t.Fatalf("checked-in lab config must pass the preflight: %s", out)
	}

	// Control: with the cache enabled, or the key omitted (the daemon
	// default is true), a Python upstream gets the shared uv cache, and the
	// preflight must refuse the config.
	for name, mutate := range map[string]func(map[string]interface{}){
		"enabled": func(g map[string]interface{}) { g["enable_cache_volume"] = true },
		"omitted": func(g map[string]interface{}) { delete(g, "enable_cache_volume") },
		"string":  func(g map[string]interface{}) { g["enable_cache_volume"] = "false" },
	} {
		bad, badPath := labCheckedInConfig(t, mutate)
		if name != "string" {
			bim := core.NewIsolationManager(bad.DockerIsolation)
			args, err := bim.BuildDockerArgs(bad.Servers[0], "python")
			if err != nil {
				t.Fatal(err)
			}
			if m := sharedMounts(args); len(m) == 0 || !strings.Contains(strings.Join(m, " "), "mcpproxy-uv-cache") {
				t.Errorf("%s: control expected the shared uv cache mount, got %v", name, m)
			}
		}
		out, code := runLib(t, lib, `lab_config_check_isolation "$1" 2>&1`, badPath)
		if code == 0 {
			t.Errorf("%s: a cache-enabled lab config must be refused", name)
		} else if !strings.Contains(out, "enable_cache_volume") {
			t.Errorf("%s: refused for the wrong reason: %s", name, out)
		}
	}
}

// TestRunLabDockerCleanupHintIsPIDScoped (Sol r14 P2): the runner tracks its
// own DAEMON_PID, so its printed cleanup must stop that PID and never suggest
// a pattern-matched pkill, which could hit other lab sessions or daemons.
func TestRunLabDockerCleanupHintIsPIDScoped(t *testing.T) {
	for _, script := range []string{"run-tpa-lab-docker.sh", "tpa-lab-redteam-experiments.sh"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "scripts", script))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			if strings.Contains(line, "pkill") || strings.Contains(line, "killall") {
				t.Errorf("%s:%d: broad process-kill instruction: %s", script, i+1, trimmed)
			}
		}
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "scripts", "run-tpa-lab-docker.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `cleanup:        $( [[ "$KEEP" == "1" ]] && echo "kill $DAEMON_PID; " )`) {
		t.Error("run-tpa-lab-docker.sh cleanup hint must stop the captured DAEMON_PID")
	}
}
