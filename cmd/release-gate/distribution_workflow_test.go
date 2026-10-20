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

// Windows package publishing (Spec 121 FR-012/FR-017): stable-only, gated by the
// release, non-blocking, reachable from both release workflows and by manual
// dispatch, and skipping (not failing) when unconfigured.
func TestWindowsPackagesWorkflowInvariants(t *testing.T) {
	dir := filepath.Join("..", "..", ".github", "workflows")
	wf := parseWorkflow(t, filepath.Join(dir, "publish-windows-packages.yml"))
	raw := repoFile(t, ".github", "workflows", "publish-windows-packages.yml")

	for _, want := range []string{"workflow_call:", "workflow_dispatch:"} {
		if !strings.Contains(raw, want) {
			t.Errorf("publish-windows-packages.yml must declare %s (reusable and manually dispatchable)", want)
		}
	}
	for _, name := range []string{"scoop", "winget"} {
		j, ok := wf.Jobs[name]
		if !ok {
			t.Fatalf("missing job %q", name)
		}
		if !strings.Contains(j.If.Value, "'-'") {
			t.Errorf("job %q must be stable-only (if: %q)", name, j.If.Value)
		}
		if strings.TrimSpace(j.ContinueOnError.Value) != "true" {
			t.Errorf("job %q must be continue-on-error: an optional surface never fails a release", name)
		}
	}
	if !strings.Contains(raw, "SCOOP_BUCKET_TOKEN is not set") || !strings.Contains(raw, "WINGET_TOKEN is not set") {
		t.Error("both jobs must skip with a notice when their token is not configured")
	}
	if strings.Contains(raw, "@main") || strings.Contains(raw, "winget-releaser") {
		t.Error("no unpinned third-party action: manifests are generated by scripts/gen-winget-manifest.sh")
	}

	for _, name := range []string{"release.yml", "retry-sign-release.yml"} {
		caller := parseWorkflow(t, filepath.Join(dir, name))
		j, ok := caller.Jobs["publish-windows-packages"]
		if !ok {
			t.Errorf("%s must call publish-windows-packages", name)
			continue
		}
		if !strings.HasSuffix(j.Uses, "publish-windows-packages.yml") {
			t.Errorf("%s: publish-windows-packages must use the reusable workflow, got %q", name, j.Uses)
		}
		if !caller.transitiveNeeds("publish-windows-packages")["release"] {
			t.Errorf("%s: publish-windows-packages must need the release job", name)
		}
		if !strings.Contains(j.If.Value, "'-'") {
			t.Errorf("%s: publish-windows-packages must exclude prerelease tags (if: %q)", name, j.If.Value)
		}
	}
	// release.yml's gate audit must classify this caller as a publisher.
	rel := parseWorkflow(t, filepath.Join(dir, "release.yml"))
	if !rel.Jobs["publish-windows-packages"].publishes() {
		t.Error("publish-windows-packages must be classified as a publisher by the QA-gate audit")
	}
	gate := rel.gateJobName()
	if !rel.transitiveNeeds("publish-windows-packages")[gate] {
		t.Errorf("publish-windows-packages must transitively depend on the qa gate %q", gate)
	}
}

// The Inno script must stay silent-safe and own its channel marker (FR-014).
func TestInstallerChannelMarkerContract(t *testing.T) {
	iss := repoFile(t, "scripts", "installer.iss")
	for _, want := range []string{
		"{param:CHANNEL|}",              // winget switch
		"'winget'",                      // only this token is ever written
		"DeleteFile(MarkerPath)",        // stale marker removed on a plain install
		`Name: "{app}\install-channel"`, // removed on uninstall
		"if not WizardSilent then",      // no blocking dialog under winget
	} {
		if !strings.Contains(iss, want) {
			t.Errorf("installer.iss lost %q (Spec 121 FR-014)", want)
		}
	}
	// No MsgBox may be reachable unguarded from NeedsAddPath.
	idx := strings.Index(iss, "function NeedsAddPath")
	end := strings.Index(iss[idx:], "// Install-channel marker")
	body := iss[idx : idx+end]
	if strings.Count(body, "MsgBox(") != 1 || !strings.Contains(body, "if not WizardSilent then\n      MsgBox(") {
		t.Error("NeedsAddPath must show its warning only when not WizardSilent")
	}
}
