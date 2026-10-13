package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/gatereport"
)

const defaultUpgradeRepo = "smart-mcp-proxy/mcpproxy-go"

type upgradeOpts struct {
	CandidateBinary string
	FixtureBinary   string
	PrevBinary      string // override: skip the GitHub download
	Repo            string
	WorkDir         string
}

// resolveLatestStableTag asks the GitHub API for the latest STABLE release
// (the /releases/latest endpoint never returns prereleases, so rc tags are
// excluded by construction).
func resolveLatestStableTag(ctx context.Context, repo string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("query latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("latest release query: status %d: %s", resp.StatusCode, truncateStr(string(body), 200))
	}
	var rel struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	if rel.TagName == "" || rel.Prerelease {
		return "", fmt.Errorf("latest release is unusable (tag=%q prerelease=%v)", rel.TagName, rel.Prerelease)
	}
	return rel.TagName, nil
}

// downloadPrevBinary fetches the host-matching release tarball for the given
// tag and extracts the mcpproxy binary into destDir.
func downloadPrevBinary(ctx context.Context, repo, tag, destDir string) (string, error) {
	version := strings.TrimPrefix(tag, "v")
	asset := fmt.Sprintf("mcpproxy-%s-%s-%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, tag, asset)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", asset, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("gunzip %s: %w", asset, err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("untar %s: %w", asset, err)
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != "mcpproxy" {
			continue
		}
		out := filepath.Join(destDir, "mcpproxy-"+version)
		f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(f, io.LimitReader(tr, 1<<30)); err != nil { //nolint:gosec // bounded copy
			f.Close()
			return "", err
		}
		f.Close()
		return out, nil
	}
	return "", fmt.Errorf("archive %s did not contain an mcpproxy binary", asset)
}

// runUpgradeCheck implements FR-014: the previous released binary populates a
// real data directory (config + BBolt config.db + Bleve index.bleve), then
// the CANDIDATE starts against the SAME directory and must retain servers,
// quarantine state, and a working search index.
func runUpgradeCheck(ctx context.Context, opts upgradeOpts) (steps []gatereport.Step, details map[string]any, err error) {
	details = map[string]any{}
	if opts.Repo == "" {
		opts.Repo = defaultUpgradeRepo
	}
	workDir, err := mkWorkDir(opts.WorkDir, "gate-upgrade-")
	if err != nil {
		return nil, details, err
	}
	details["work_dir"] = workDir

	step := func(name string, fn func() error) error {
		start := time.Now()
		stepErr := fn()
		s := gatereport.Step{Name: name, Status: gatereport.StatusPass, DurationMS: time.Since(start).Milliseconds()}
		if stepErr != nil {
			s.Status = gatereport.StatusFail
			s.Reason = stepErr.Error()
		}
		steps = append(steps, s)
		if stepErr != nil {
			return fmt.Errorf("step %s: %w", name, stepErr)
		}
		return nil
	}

	// 1. Resolve + download the previous released binary (unless overridden).
	prevBinary := opts.PrevBinary
	if err := step("resolve-previous-release", func() error {
		if prevBinary != "" {
			details["previous_binary"] = prevBinary + " (--prev-binary override)"
			return nil
		}
		tag, err := resolveLatestStableTag(ctx, opts.Repo)
		if err != nil {
			return &infraError{err}
		}
		details["previous_tag"] = tag
		bin, err := downloadPrevBinary(ctx, opts.Repo, tag, workDir)
		if err != nil {
			return &infraError{err}
		}
		prevBinary = bin
		details["previous_binary"] = bin
		return nil
	}); err != nil {
		return steps, details, err
	}

	// 2. Data-dir layout shared by both binaries: two stdio fixtures, one
	// left quarantined so the candidate must preserve that state.
	dataDir := filepath.Join(workDir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return steps, details, err
	}
	fixA := filepath.Join(workDir, "bin", "mcpfixture-upgrade-a")
	fixB := filepath.Join(workDir, "bin", "mcpfixture-upgrade-b")
	if err := copyFile(opts.FixtureBinary, fixA); err != nil {
		return steps, details, err
	}
	if err := copyFile(opts.FixtureBinary, fixB); err != nil {
		return steps, details, err
	}
	defer func() {
		_, _ = killByPattern(fixA)
		_, _ = killByPattern(fixB)
	}()

	port, err := freePort()
	if err != nil {
		return steps, details, err
	}
	listen := fmt.Sprintf("127.0.0.1:%d", port)
	apiKey := newAPIKey()
	configPath := filepath.Join(workDir, "upgrade-config.json")
	cfg := buildGateConfig(listen, dataDir, apiKey, []gateServerConfig{
		{"name": "gate-up-a", "command": fixA, "args": []string{"--transport", "stdio"},
			"protocol": "stdio", "enabled": true, "quarantined": false},
		{"name": "gate-up-b", "command": fixB, "args": []string{"--transport", "stdio"},
			"protocol": "stdio", "enabled": true, "quarantined": true},
	}, nil)
	if err := writeConfig(configPath, cfg); err != nil {
		return steps, details, err
	}

	client := newClient("http://"+listen, apiKey)

	// 3. Run the OLD binary: Ready + one indexed tool call, then SIGTERM and
	// WAIT on the PID (never race the BBolt lock — exit code 3 means raced).
	var oldCore *coreProc
	if err := step("previous-version-populates-data-dir", func() error {
		var err error
		oldCore, err = startCore(ctx, prevBinary, configPath, dataDir, listen, filepath.Join(workDir, "core-old.log"))
		if err != nil {
			return err
		}
		if err := waitCoreReady(ctx, client, oldCore, 60*time.Second); err != nil {
			return err
		}
		if err := waitNamedServerReady(ctx, client, "gate-up-a", 2, 90*time.Second); err != nil {
			return err
		}
		nonce := "gate-upgrade-" + randomNonce()
		text, err := client.callToolREST(ctx, "gate-up-a:echo", map[string]any{"text": nonce}, "")
		if err != nil {
			return fmt.Errorf("indexed tool call on previous version: %w", err)
		}
		if !strings.Contains(text, nonce) {
			return fmt.Errorf("previous-version echo did not round-trip: %s", truncateStr(text, 200))
		}
		if err := assertSearchHasResults(ctx, client, "echo", "gate-up-a", 30*time.Second); err != nil {
			return fmt.Errorf("previous-version index search: %w", err)
		}
		return nil
	}); err != nil {
		if oldCore != nil {
			_ = oldCore.stopGraceful(20 * time.Second)
		}
		return steps, details, err
	}

	if err := step("previous-version-clean-shutdown", func() error {
		return oldCore.stopGraceful(30 * time.Second)
	}); err != nil {
		return steps, details, err
	}
	// The old core's stdio children die with it; make sure before restart.
	_, _ = killByPattern(fixA)
	_, _ = killByPattern(fixB)

	// 4. Start the CANDIDATE on the SAME data dir.
	var newCore *coreProc
	defer func() {
		if newCore != nil {
			_ = newCore.stopGraceful(20 * time.Second)
		}
	}()
	if err := step("candidate-starts-on-old-data-dir", func() error {
		var err error
		newCore, err = startCore(ctx, opts.CandidateBinary, configPath, dataDir, listen, filepath.Join(workDir, "core-new.log"))
		if err != nil {
			return err
		}
		return waitCoreReady(ctx, client, newCore, 60*time.Second)
	}); err != nil {
		return steps, details, err
	}

	if err := step("servers-and-quarantine-retained", func() error {
		servers, err := client.servers(ctx)
		if err != nil {
			return err
		}
		var a, b *serverInfo
		for i := range servers {
			switch servers[i].Name {
			case "gate-up-a":
				a = &servers[i]
			case "gate-up-b":
				b = &servers[i]
			}
		}
		if a == nil || b == nil {
			return fmt.Errorf("configured servers lost across upgrade (got %d servers, a=%v b=%v)", len(servers), a != nil, b != nil)
		}
		if a.Quarantined {
			return fmt.Errorf("gate-up-a gained quarantine across upgrade")
		}
		if !b.Quarantined {
			return fmt.Errorf("gate-up-b LOST its quarantine state across upgrade")
		}
		return waitNamedServerReady(ctx, client, "gate-up-a", 2, 90*time.Second)
	}); err != nil {
		return steps, details, err
	}

	if err := step("index-search-preserved", func() error {
		return assertSearchHasResults(ctx, client, "echo", "gate-up-a", 60*time.Second)
	}); err != nil {
		return steps, details, err
	}

	if err := step("candidate-clean-shutdown", func() error {
		err := newCore.stopGraceful(30 * time.Second)
		newCore = nil
		return err
	}); err != nil {
		return steps, details, err
	}

	return steps, details, nil
}

// waitNamedServerReady is the standalone version of matrixRun.waitServerReady
// for cores the upgrade check owns.
func waitNamedServerReady(ctx context.Context, c *Client, name string, minTools int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last *serverInfo
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		srv, err := c.server(ctx, name)
		if err == nil && srv != nil {
			last = srv
			if srv.Connected && srv.ToolCount >= minTools {
				return nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	if last == nil {
		return fmt.Errorf("server %s never appeared", name)
	}
	return fmt.Errorf("server %s not ready after %s (connected=%v tools=%d last_error=%s)",
		name, timeout, last.Connected, last.ToolCount, last.LastError)
}
