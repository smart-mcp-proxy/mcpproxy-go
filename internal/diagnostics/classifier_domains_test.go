package diagnostics

import (
	"errors"
	"strings"
	"testing"
)

// These golden-sample tests cover the OAUTH / DOCKER / CONFIG / QUARANTINE
// classifier fallbacks and the typed-wrap fast path added in spec 044
// (phase D extension). They intentionally use real-looking raw error strings
// collected from the upstream manager / mcp-go / Docker CLI so that adding
// WrapError() calls at the producer side later will not break classification.

// --- WrapError typed fast path ---------------------------------------------

func TestClassify_WrapError_OverridesFallback(t *testing.T) {
	wrapped := WrapError(OAuthCallbackMismatch, errors.New("some free-text message"))
	got := Classify(wrapped, ClassifierHints{})
	if got != OAuthCallbackMismatch {
		t.Errorf("Classify(WrapError) = %q, want %q", got, OAuthCallbackMismatch)
	}
}

func TestClassify_WrapError_NilPassthrough(t *testing.T) {
	if WrapError(OAuthCallbackMismatch, nil) != nil {
		t.Errorf("WrapError(_, nil) must return nil")
	}
}

// --- OAUTH ------------------------------------------------------------------

func TestClassify_OAuth_RefreshExpired(t *testing.T) {
	samples := []error{
		errors.New("refresh_token expired"),
		errors.New("the refresh token has expired"),
	}
	for _, err := range samples {
		if got := Classify(err, ClassifierHints{}); got != OAuthRefreshExpired {
			t.Errorf("Classify(%q) = %q, want %q", err, got, OAuthRefreshExpired)
		}
	}
}

func TestClassify_OAuth_Refresh403(t *testing.T) {
	err := errors.New("token refresh failed: HTTP 403 invalid_grant")
	if got := Classify(err, ClassifierHints{}); got != OAuthRefresh403 {
		t.Errorf("Classify(refresh_403) = %q, want %q", got, OAuthRefresh403)
	}
}

func TestClassify_OAuth_DiscoveryFailed(t *testing.T) {
	err := errors.New("OAuth metadata unavailable at .well-known/oauth-authorization-server")
	if got := Classify(err, ClassifierHints{}); got != OAuthDiscoveryFailed {
		t.Errorf("Classify(oauth_discovery) = %q, want %q", got, OAuthDiscoveryFailed)
	}
}

// --- DOCKER -----------------------------------------------------------------

func TestClassify_Docker_DaemonDown(t *testing.T) {
	err := errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?")
	if got := Classify(err, ClassifierHints{}); got != DockerDaemonDown {
		t.Errorf("Classify(docker_daemon_down) = %q, want %q", got, DockerDaemonDown)
	}
}

func TestClassify_Docker_NoPermission(t *testing.T) {
	err := errors.New("Got permission denied while trying to connect to the Docker daemon socket")
	if got := Classify(err, ClassifierHints{}); got != DockerNoPermission {
		t.Errorf("Classify(docker_no_permission) = %q, want %q", got, DockerNoPermission)
	}
}

func TestClassify_Docker_SnapAppArmor(t *testing.T) {
	err := errors.New("running container with no-new-privileges under AppArmor profile snap.docker.docker fails")
	if got := Classify(err, ClassifierHints{}); got != DockerSnapAppArmor {
		t.Errorf("Classify(snap_apparmor) = %q, want %q", got, DockerSnapAppArmor)
	}
}

// TestClassify_Docker_IsolationSpawn exercises the #696 / image-mismatch
// routing: when the docker-isolation hint is set, ENOENT-class failures on the
// stdio transport must resolve to DOCKER codes rather than a plain
// MCPX_STDIO_SPAWN_ENOENT, so the telemetry dashboard sees the real cause.
func TestClassify_Docker_IsolationSpawn(t *testing.T) {
	cases := []struct {
		name string
		err  error
		hint ClassifierHints
		want Code
	}{
		{
			// #696: docker CLI not on the spawn PATH; the login-shell wrap
			// reports `docker: command not found`.
			name: "cli_not_found_shell",
			err:  errors.New("stdio transport (command=\"/bin/zsh\", docker_isolation=true): recent stderr: docker: command not found"),
			hint: ClassifierHints{Transport: "stdio", DockerIsolated: true},
			want: DockerCLINotFound,
		},
		{
			// #696 via zsh login shell: the common macOS shape is the REVERSED
			// wording `zsh:1: command not found: docker` (name after the colon),
			// which must still classify as CLI-not-found, not in-container EXEC.
			name: "cli_not_found_zsh_reversed",
			err:  errors.New("stdio transport (docker_isolation=true): recent stderr: zsh:1: command not found: docker"),
			hint: ClassifierHints{Transport: "stdio", DockerIsolated: true},
			want: DockerCLINotFound,
		},
		{
			// shellwrap resolution failure surfaces this even without the hint.
			name: "cli_not_found_resolve",
			err:  errors.New("docker not found in PATH or well-known locations"),
			hint: ClassifierHints{Transport: "stdio", DockerIsolated: true},
			want: DockerCLINotFound,
		},
		{
			// In-container interpreter missing (e.g. uvx absent in python:3.11).
			name: "exec_not_found",
			err:  errors.New("docker: Error response from daemon: failed to create task: OCI runtime create failed: runc create failed: exec: \"uvx\": executable file not found in $PATH: unknown"),
			hint: ClassifierHints{Transport: "stdio", DockerIsolated: true},
			want: DockerExecNotFound,
		},
		{
			// OCI runtime / arch mismatch with no interpreter-missing detail.
			name: "oci_runtime",
			err:  errors.New("docker: Error response from daemon: failed to create shim task: OCI runtime create failed: exec format error: unknown"),
			hint: ClassifierHints{Transport: "stdio", DockerIsolated: true},
			want: DockerOCIRuntime,
		},
		{
			// Bare "exec format error" WITH the isolation hint → OCI (wrong-arch
			// image under docker isolation).
			name: "bare_exec_format_isolated",
			err:  errors.New("stdio transport (docker_isolation=true): recent stderr: exec format error"),
			hint: ClassifierHints{Transport: "stdio", DockerIsolated: true},
			want: DockerOCIRuntime,
		},
		{
			// Bare "exec format error" WITHOUT the isolation hint must stay
			// STDIO-classified (a non-docker wrong-arch host binary), NOT a
			// Docker code. Codex round-5 regression.
			name: "bare_exec_format_no_hint_stays_stdio",
			err:  errors.New("failed to spawn stdio server: recent stderr: exec format error"),
			hint: ClassifierHints{Transport: "stdio"},
			want: STDIOSpawnExecFormat,
		},
		{
			// A real docker OCI error that lacks the hint but carries "oci
			// runtime" context still classifies as OCI (not STDIO), via the
			// classifyDocker fallback.
			name: "oci_context_no_hint",
			err:  errors.New("oci runtime create failed: exec format error"),
			hint: ClassifierHints{Transport: "stdio"},
			want: DockerOCIRuntime,
		},
		{
			// Same ENOENT string WITHOUT the isolation hint stays a plain stdio
			// spawn failure — no false DOCKER attribution for host stdio servers.
			name: "non_containerized_enoent",
			err:  errors.New("failed to spawn: executable file not found in $PATH"),
			hint: ClassifierHints{Transport: "stdio"},
			want: STDIOSpawnENOENT,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.err, tc.hint); got != tc.want {
				t.Errorf("Classify(%q) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// --- CONFIG -----------------------------------------------------------------

func TestClassify_Config_ParseError(t *testing.T) {
	err := errors.New("config: invalid JSON at offset 42")
	if got := Classify(err, ClassifierHints{}); got != ConfigParseError {
		t.Errorf("Classify(config_parse) = %q, want %q", got, ConfigParseError)
	}
}

func TestClassify_Config_MissingSecret(t *testing.T) {
	err := errors.New("missing secret: GITHUB_TOKEN referenced by server 'gh'")
	if got := Classify(err, ClassifierHints{}); got != ConfigMissingSecret {
		t.Errorf("Classify(missing_secret) = %q, want %q", got, ConfigMissingSecret)
	}
}

func TestClassify_Config_DeprecatedField(t *testing.T) {
	err := errors.New("features: deprecated field will be removed in a future release")
	if got := Classify(err, ClassifierHints{}); got != ConfigDeprecatedField {
		t.Errorf("Classify(deprecated) = %q, want %q", got, ConfigDeprecatedField)
	}
}

// --- QUARANTINE -------------------------------------------------------------

func TestClassify_Quarantine_PendingApproval(t *testing.T) {
	err := errors.New("tool 'delete_repo' is in quarantine and not approved for execution")
	if got := Classify(err, ClassifierHints{}); got != QuarantinePendingApproval {
		t.Errorf("Classify(pending_approval) = %q, want %q", got, QuarantinePendingApproval)
	}
}

func TestClassify_Quarantine_ToolChanged(t *testing.T) {
	err := errors.New("tool description changed since last approval; re-approval required (rug pull protection)")
	if got := Classify(err, ClassifierHints{}); got != QuarantineToolChanged {
		t.Errorf("Classify(tool_changed) = %q, want %q", got, QuarantineToolChanged)
	}
}

// --- RUNTIME-AWARE REMEDIATION (MCP-2909) -----------------------------------

// TestRuntimeAwareRemediation_DockerExecNotFound covers the field-report case
// (ElevenLabs / uvx / per-server image override): a `uvx` server pinned to a
// stock `python:3.11` image fails at exec time because that image has no `uvx`.
// The enriched DockerExecNotFound remediation must name the detected runtime,
// the recommended runtime-default image, and flag the per-server override as the
// likely culprit when it differs from the default.
func TestRuntimeAwareRemediation_DockerExecNotFound(t *testing.T) {
	const uvImage = "ghcr.io/astral-sh/uv:python3.13-bookworm-slim"
	defaults := map[string]string{
		"uvx":  uvImage,
		"pipx": uvImage,
		"npx":  "node:22",
	}

	t.Run("uvx_on_bare_python_override", func(t *testing.T) {
		msg := RuntimeAwareRemediation(DockerExecNotFound, ClassifierHints{
			DockerCommand:       "uvx",
			DockerImageOverride: "python:3.11",
			DockerDefaultImages: defaults,
		})
		// Must name the detected runtime.
		if !strings.Contains(msg, "uvx") {
			t.Errorf("message must name the runtime 'uvx'; got: %q", msg)
		}
		// Must name the recommended image.
		if !strings.Contains(msg, uvImage) {
			t.Errorf("message must name recommended image %q; got: %q", uvImage, msg)
		}
		// Must flag the per-server override as the culprit.
		if !strings.Contains(msg, "python:3.11") {
			t.Errorf("message must name the failing override image 'python:3.11'; got: %q", msg)
		}
		if !strings.Contains(strings.ToLower(msg), "override") {
			t.Errorf("message must flag the per-server override; got: %q", msg)
		}
	})

	t.Run("npx_no_override_still_names_runtime_and_image", func(t *testing.T) {
		msg := RuntimeAwareRemediation(DockerExecNotFound, ClassifierHints{
			DockerCommand:       "npx",
			DockerDefaultImages: defaults,
		})
		if !strings.Contains(msg, "npx") {
			t.Errorf("message must name the runtime 'npx'; got: %q", msg)
		}
		if !strings.Contains(msg, "node:22") {
			t.Errorf("message must name recommended image 'node:22'; got: %q", msg)
		}
	})

	t.Run("no_enrichment_without_command", func(t *testing.T) {
		if msg := RuntimeAwareRemediation(DockerExecNotFound, ClassifierHints{
			DockerDefaultImages: defaults,
		}); msg != "" {
			t.Errorf("no docker command → empty enrichment (fall back to static catalog); got: %q", msg)
		}
	})

	t.Run("no_enrichment_for_other_codes", func(t *testing.T) {
		if msg := RuntimeAwareRemediation(DockerCLINotFound, ClassifierHints{
			DockerCommand:       "uvx",
			DockerDefaultImages: defaults,
		}); msg != "" {
			t.Errorf("only DockerExecNotFound is enriched; got: %q", msg)
		}
	})
}
