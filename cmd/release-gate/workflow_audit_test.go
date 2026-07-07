package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestPublishJobsGatedOnQAGate is the FR-022 / SC-004 workflow-dependency
// audit: it parses the two publisher workflows and asserts that every
// artifact-publishing job's transitive `needs` closure includes the job that
// invokes the reusable release-qa-gate workflow. If a future job publishes to
// users without chaining to the gate, this test fails — mechanically enforcing
// "0 artifacts are ever published from a tag whose gate failed".
//
// The gate itself is structural: the `release` job (which creates the GitHub
// Release) depends on the qa-gate job, and every other public-facing job
// cascades from `release`. A job that never runs (`if: false...`) cannot
// publish and is excluded.
func TestPublishJobsGatedOnQAGate(t *testing.T) {
	// go test runs with the package directory (cmd/release-gate) as the working
	// directory, so the workflows live two levels up.
	workflowDir := filepath.Join("..", "..", ".github", "workflows")
	for _, name := range []string{"release.yml", "prerelease.yml"} {
		name := name
		t.Run(name, func(t *testing.T) {
			wf := parseWorkflow(t, filepath.Join(workflowDir, name))

			gateJob := wf.gateJobName()
			if gateJob == "" {
				t.Fatalf("%s: no job invokes the reusable release-qa-gate workflow (uses: */release-qa-gate.yml); publishing is ungated", name)
			}

			var sawPublisher bool
			for jobName, j := range wf.Jobs {
				if jobName == gateJob {
					continue // the gate job is not a publisher
				}
				if j.disabled() {
					continue // a job that can never run cannot publish
				}
				if !j.publishes() {
					continue
				}
				sawPublisher = true
				closure := wf.transitiveNeeds(jobName)
				if !closure[gateJob] {
					t.Errorf("%s: publishing job %q does not transitively depend on the qa-gate job %q (transitive needs: %v) — it could publish artifacts for a tag whose gate failed",
						name, jobName, gateJob, sortedKeys(closure))
				}
			}
			if !sawPublisher {
				t.Errorf("%s: no artifact-publishing job detected — the audit's publish signatures are stale and no longer match this workflow", name)
			}
		})
	}
}

// --- workflow model -------------------------------------------------------

type workflow struct {
	Jobs map[string]job `yaml:"jobs"`
}

type job struct {
	Needs needsList `yaml:"needs"`
	Uses  string    `yaml:"uses"`
	If    yaml.Node `yaml:"if"`
	Steps []step    `yaml:"steps"`
}

type step struct {
	Uses string `yaml:"uses"`
	Run  string `yaml:"run"`
}

// needsList decodes `needs:` which may be a scalar (`needs: build`) or a
// sequence (`needs: [build, qa-gate]`).
type needsList []string

func (n *needsList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		*n = []string{value.Value}
	case yaml.SequenceNode:
		var out []string
		for _, item := range value.Content {
			out = append(out, item.Value)
		}
		*n = out
	}
	return nil
}

func (j job) disabled() bool {
	// Treat `if: false` and `if: false && <expr>` as statically disabled.
	return strings.HasPrefix(strings.TrimSpace(j.If.Value), "false")
}

// publishSignatures are substrings whose presence marks a job as publishing a
// user-facing artifact (GitHub release, docker image, homebrew tap, docs site,
// package repos, MCP registry, provenance attachment, marketing dispatch).
var publishSignatures = []string{
	"action-gh-release",     // create GitHub Release + upload assets
	"gh release upload",     // upload additional release assets
	"gh release create",     // create a release
	"mcp-publisher",         // publish to the MCP registry
	"pages deploy",          // wrangler pages deploy (docs site)
	"wrangler-action",       // cloudflare pages deploy
	"repository-dispatch",   // trigger the marketing site update
	"build-push-action",     // docker build + push
	"slsa-github-generator", // SLSA provenance attached to the release
	"homebrew_tap_token",    // push to the homebrew tap
	"publish.sh",            // publish apt/rpm repos
}

func (j job) publishes() bool {
	haystacks := []string{strings.ToLower(j.Uses)}
	for _, s := range j.Steps {
		haystacks = append(haystacks, strings.ToLower(s.Uses), strings.ToLower(s.Run))
	}
	for _, h := range haystacks {
		for _, sig := range publishSignatures {
			if strings.Contains(h, sig) {
				return true
			}
		}
	}
	return false
}

func (wf workflow) gateJobName() string {
	for name, j := range wf.Jobs {
		if strings.Contains(j.Uses, "release-qa-gate") {
			return name
		}
	}
	return ""
}

func (wf workflow) transitiveNeeds(job string) map[string]bool {
	seen := map[string]bool{}
	var visit func(string)
	visit = func(n string) {
		j, ok := wf.Jobs[n]
		if !ok {
			return
		}
		for _, dep := range j.Needs {
			if seen[dep] {
				continue
			}
			seen[dep] = true
			visit(dep)
		}
	}
	visit(job)
	return seen
}

func parseWorkflow(t *testing.T, path string) workflow {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var wf workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(wf.Jobs) == 0 {
		t.Fatalf("%s: no jobs parsed", path)
	}
	return wf
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// small n; simple insertion order is fine but sort for stable messages
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
