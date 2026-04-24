package diagnostics

import (
	"errors"
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
