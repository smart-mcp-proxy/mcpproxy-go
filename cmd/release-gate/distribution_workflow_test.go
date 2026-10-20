package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Spec 121 workflow/packaging invariants for the install surfaces. These are
// audits of files, not behavior: they fail the build when a future edit breaks
// a contract the official MCP registry or a package manager relies on.

func repoFile(t *testing.T, rel ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, rel...)...))
	if err != nil {
		t.Fatalf("read %v: %v", rel, err)
	}
	return string(b)
}

func jobRunText(j job) string {
	var sb strings.Builder
	for _, s := range j.Steps {
		sb.WriteString(s.Run)
		sb.WriteString("\n")
	}
	return sb.String()
}

// The registry verifies image ownership by a label equal to server.json "name".
// Three places must agree or the publish is rejected (immutably per version).
func TestRegistryOwnershipLabelAgreesEverywhere(t *testing.T) {
	var sj struct {
		Name     string `json:"name"`
		Packages []struct {
			RegistryType string `json:"registryType"`
			Identifier   string `json:"identifier"`
		} `json:"packages"`
	}
	if err := json.Unmarshal([]byte(repoFile(t, "server.json")), &sj); err != nil {
		t.Fatal(err)
	}
	if sj.Name == "" {
		t.Fatal("server.json has no name")
	}
	if len(sj.Packages) == 0 {
		t.Fatal("server.json has no packages: registry-fed catalogs show no install action (Spec 121 FR-001)")
	}
	var oci bool
	for _, p := range sj.Packages {
		if p.RegistryType == "oci" {
			oci = true
			if !strings.HasPrefix(p.Identifier, "ghcr.io/smart-mcp-proxy/mcpproxy-server:") {
				t.Errorf("oci identifier %q must name the published image with an explicit tag", p.Identifier)
			}
		}
	}
	if !oci {
		t.Error("server.json must keep an oci package")
	}

	want := `io.modelcontextprotocol.server.name`
	docker := repoFile(t, "Dockerfile")
	if !regexp.MustCompile(`(?m)^LABEL\s+` + regexp.QuoteMeta(want) + `="` + regexp.QuoteMeta(sj.Name) + `"`).MatchString(docker) {
		t.Errorf("Dockerfile must carry LABEL %s=%q", want, sj.Name)
	}
	rel := repoFile(t, ".github", "workflows", "release.yml")
	if !strings.Contains(rel, want+"="+sj.Name) {
		t.Errorf("release.yml build-docker labels must include %s=%s", want, sj.Name)
	}
}

// Both registry publishers must stamp via the shared script and run the
// preflight before publishing, stay stable-only, and never block the release.
func TestRegistryPublishersShareStampAndPreflight(t *testing.T) {
	for _, name := range []string{"release.yml", "retry-sign-release.yml"} {
		name := name
		t.Run(name, func(t *testing.T) {
			wf := parseWorkflow(t, filepath.Join("..", "..", ".github", "workflows", name))
			j, ok := wf.Jobs["mcp-registry"]
			if !ok {
				t.Fatalf("%s has no mcp-registry job", name)
			}
			if strings.TrimSpace(j.ContinueOnError.Value) != "true" {
				t.Errorf("%s mcp-registry must be continue-on-error: a registry hiccup must not fail a release", name)
			}
			run := jobRunText(j)
			stamp := strings.Index(run, "scripts/stamp-server-json.sh")
			pre := strings.Index(run, "scripts/registry-preflight.sh")
			pub := strings.Index(run, "mcp-publisher publish")
			if stamp < 0 || pre < 0 || pub < 0 {
				t.Fatalf("%s mcp-registry must stamp (%d), preflight (%d) and publish (%d)", name, stamp, pre, pub)
			}
			if !(stamp < pre && pre < pub) {
				t.Errorf("%s: order must be stamp < preflight < publish (got %d, %d, %d)", name, stamp, pre, pub)
			}
			if strings.Contains(run, `'.version = $v'`) {
				t.Errorf("%s still stamps only .version inline; the OCI identifier would go stale", name)
			}
			if !strings.Contains(strings.TrimSpace(j.If.Value), "'-'") && !strings.Contains(j.If.Value, `'-'`) {
				t.Errorf("%s mcp-registry must exclude prerelease tags (if: %q)", name, j.If.Value)
			}
		})
	}
}

// release.yml: the registry job must only run after the image is pushed.
func TestRegistryPublishAfterImagePush(t *testing.T) {
	wf := parseWorkflow(t, filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	closure := wf.transitiveNeeds("mcp-registry")
	for _, need := range []string{"release", "build-docker"} {
		if !closure[need] {
			t.Errorf("mcp-registry must transitively need %q (transitive needs: %v)", need, sortedKeys(closure))
		}
	}
}
