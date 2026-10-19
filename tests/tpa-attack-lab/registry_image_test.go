package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// labConfigWithRegistry writes the checked-in lab config with
// docker_isolation.registry set (or removed when registry is "").
func labConfigWithRegistry(t *testing.T, registry string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "tpa-lab-config-docker.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	glob, _ := cfg["docker_isolation"].(map[string]interface{})
	if glob == nil {
		glob = map[string]interface{}{}
		cfg["docker_isolation"] = glob
	}
	if registry == "" {
		delete(glob, "registry")
	} else {
		glob["registry"] = registry
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Sol r15 P2: the daemon prefixes an unqualified isolation image with
// docker_isolation.registry (internal/upstream/core/isolation.go
// buildFullImageName), so "tpa-attack-lab:lab" plus a custom registry runs
// registry.example.invalid/library/tpa-attack-lab:lab, not the fixture the
// runner just built. The isolation preflight and the runner must refuse it;
// the default registry (absent or docker.io) resolves to the local tag and
// is accepted.
func TestLabConfigRefusesRegistryInducedImageMismatch(t *testing.T) {
	lib := verdictsLib(t)
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "run-tpa-lab-docker.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, reg := range []string{"", "docker.io"} {
		cfg := labConfigWithRegistry(t, reg)
		if out, code := runLib(t, lib, `lab_config_check_isolation "$1"`, cfg); code != 0 {
			t.Errorf("registry %q: default registry must pass isolation preflight: %s", reg, out)
		}
		images, code := runLib(t, lib, `lab_config_images "$1"`, cfg)
		if code != 0 {
			t.Fatalf("lab_config_images exit %d", code)
		}
		for _, img := range strings.Split(images, "\n") {
			if img != "tpa-attack-lab:lab" {
				t.Errorf("registry %q: effective image %q, want tpa-attack-lab:lab", reg, img)
			}
		}
		cmd := exec.Command("bash", script)
		cmd.Env = append(os.Environ(), "IMAGE=tpa-attack-lab:lab", "TPA_LAB_PREFLIGHT_ONLY=1", "TPA_LAB_CONFIG="+cfg)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("registry %q: runner preflight must pass: %v\n%s", reg, err, out)
		}
	}
	for _, reg := range []string{"registry.example.invalid", "ghcr.io", "localhost:5000"} {
		cfg := labConfigWithRegistry(t, reg)
		if _, code := runLib(t, lib, `lab_config_check_isolation "$1" 2>/dev/null`, cfg); code == 0 {
			t.Errorf("registry %q: isolation preflight must refuse a registry-rewritten image", reg)
		}
		images, _ := runLib(t, lib, `lab_config_images "$1"`, cfg)
		for _, img := range strings.Split(images, "\n") {
			if img == "tpa-attack-lab:lab" {
				t.Errorf("registry %q: lab_config_images reports the raw tag, not the image the daemon runs", reg)
			}
		}
		cmd := exec.Command("bash", script)
		cmd.Env = append(os.Environ(), "IMAGE=tpa-attack-lab:lab", "TPA_LAB_PREFLIGHT_ONLY=1", "TPA_LAB_CONFIG="+cfg)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("registry %q: runner preflight must refuse; output:\n%s", reg, out)
		} else if !strings.Contains(string(out), "registry") {
			t.Errorf("registry %q: refusal should name the registry:\n%s", reg, out)
		}
	}
}
