package telemetry

import (
	"reflect"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func TestBuildFeatureFlagSnapshotNilConfig(t *testing.T) {
	snap := BuildFeatureFlagSnapshot(nil)
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if snap.OAuthProviderTypes == nil {
		t.Error("expected non-nil empty slice for OAuthProviderTypes")
	}
}

func TestBuildFeatureFlagSnapshotFromConfig(t *testing.T) {
	enabledTrue := true
	cfg := &config.Config{
		EnableSocket:        true,
		Features:            &config.FeatureFlags{EnableWebUI: true},
		RequireMCPAuth:      false,
		EnableCodeExecution: true,
		QuarantineEnabled:   &enabledTrue,
		SensitiveDataDetection: &config.SensitiveDataDetectionConfig{
			Enabled: true,
		},
		Servers: []*config.ServerConfig{
			{
				Name:  "google-drive",
				URL:   "https://accounts.google.com/o/oauth2/auth",
				OAuth: &config.OAuthConfig{ClientID: "secret-google-id"},
			},
			{
				Name:  "github-issues",
				URL:   "https://github.com/login/oauth/authorize",
				OAuth: &config.OAuthConfig{ClientID: "secret-github-id"},
			},
			{
				Name:  "internal-saml",
				URL:   "https://login.example.com/oauth",
				OAuth: &config.OAuthConfig{ClientID: "secret-internal"},
			},
			{
				Name: "no-oauth-server",
				URL:  "https://api.example.com",
			},
		},
	}

	snap := BuildFeatureFlagSnapshot(cfg)
	if !snap.EnableSocket {
		t.Error("EnableSocket should be true")
	}
	if !snap.EnableWebUI {
		t.Error("EnableWebUI should be true")
	}
	if snap.RequireMCPAuth {
		t.Error("RequireMCPAuth should be false")
	}
	if !snap.EnableCodeExecution {
		t.Error("EnableCodeExecution should be true")
	}
	if !snap.QuarantineEnabled {
		t.Error("QuarantineEnabled should be true")
	}
	if !snap.SensitiveDataDetectionEnabled {
		t.Error("SensitiveDataDetectionEnabled should be true")
	}

	want := []string{"generic", "github", "google"}
	if !reflect.DeepEqual(snap.OAuthProviderTypes, want) {
		t.Errorf("OAuthProviderTypes = %v, want %v", snap.OAuthProviderTypes, want)
	}
}

func TestBuildFeatureFlagSnapshotNilFeatures(t *testing.T) {
	// When cfg.Features is nil, EnableWebUI should fall back to false rather
	// than panic. Guards against a nil-pointer deref in the emitter.
	cfg := &config.Config{
		EnableSocket: true,
		// Features intentionally omitted (nil).
	}
	snap := BuildFeatureFlagSnapshot(cfg)
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if snap.EnableWebUI {
		t.Error("EnableWebUI should be false when cfg.Features is nil")
	}
}

func TestBuildFeatureFlagSnapshotEmptyOAuthList(t *testing.T) {
	cfg := &config.Config{
		Servers: []*config.ServerConfig{
			{Name: "no-oauth", URL: "https://api.example.com"},
		},
	}
	snap := BuildFeatureFlagSnapshot(cfg)
	if len(snap.OAuthProviderTypes) != 0 {
		t.Errorf("expected empty list, got %v", snap.OAuthProviderTypes)
	}
}

func TestClassifyOAuthProvider(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://accounts.google.com/oauth", "google"},
		{"https://oauth2.googleapis.com/token", "google"},
		{"https://api.github.com/user", "github"},
		{"https://login.microsoftonline.com/common", "microsoft"},
		{"https://login.example.com/oauth", "generic"},
		{"https://corp-saml.internal.tld/auth", "generic"},
		{"", "generic"},
	}
	for _, c := range cases {
		t.Run(c.url, func(t *testing.T) {
			got := classifyOAuthProvider(c.url)
			if got != c.want {
				t.Errorf("classifyOAuthProvider(%q) = %q, want %q", c.url, got, c.want)
			}
		})
	}
}

func TestFeatureFlagSnapshotPayloadHasNoOAuthSecrets(t *testing.T) {
	cfg := &config.Config{
		Servers: []*config.ServerConfig{
			{
				Name:  "test",
				URL:   "https://accounts.google.com/oauth",
				OAuth: &config.OAuthConfig{ClientID: "MY-SUPER-SECRET-CLIENT-ID-12345"},
			},
		},
	}
	snap := BuildFeatureFlagSnapshot(cfg)
	for _, t := range snap.OAuthProviderTypes {
		if t == "MY-SUPER-SECRET-CLIENT-ID-12345" || t == "https://accounts.google.com/oauth" {
			panic("OAuth secret leaked into provider types")
		}
	}
}
