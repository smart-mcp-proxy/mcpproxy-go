package telemetry

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 107 (US7, FR-038): schema v13 adds feature_flags.server_edition_enabled,
// feature_flags.idp_provider and member_count_bucket (the contract's
// user_count_bucket, renamed off the "user" substring — see
// TestPayloadV13_PassesScanWithCommonUsernameBlocked). The shared test body below
// is untagged; the fixture it consumes is defined twice — once under
// `//go:build server` (payload_v13_fixture_server_test.go) and once under
// `//go:build !server` (payload_v13_fixture_personal_test.go) — so the
// build-tagged config accessors are proven under their own tag rather than
// assumed.

// Canary strings the v13 fixture plants in the server-edition block. None of
// them may ever reach the wire form (contracts/telemetry-v13.md, Privacy).
const (
	v13CanaryIssuerHost = "login.corp.example"
	v13CanaryIssuerURL  = "https://" + v13CanaryIssuerHost + "/realms/corp"
	v13CanaryAdminEmail = "alice.admin@corp.example"
	v13CanaryUserEmail  = "bob.user@corp.example"
	v13CanaryGroupName  = "grp-platform-admins"
	v13CanaryGroupClaim = "corp_groups_claim"
	v13CanaryClientID   = "oidc-client-id-CANARY-4242"
	v13CanarySecret     = "oidc-client-secret-CANARY-9999"
	v13CanaryServerName = "MY-V13-CANARY-SERVER"
	v13CanaryServerURL  = "https://mcp-internal.corp.example/mcp"
	v13CanaryDisplay    = "Corp SSO CANARY"
)

// v13Fixture is what each tagged twin hands the shared body: a config whose
// server-edition block carries every canary, an optional user counter, and the
// values the edition under test must report.
type v13Fixture struct {
	cfg         *config.Config
	userCounter func() (int, error)
	wantEnabled bool
	wantIdP     string
	wantBucket  string
}

// v13CanaryConfig is the edition-neutral part of the fixture. The
// server_edition block is loaded from JSON bytes because the personal build
// holds it as an opaque canonical-JSON carrier with no fields (FR-040): the
// identical document is loaded by both editions, so the personal twin proves
// the carrier leaks nothing while the server twin proves the accessors read it.
func v13CanaryConfig() *config.Config {
	enabled := true
	cfg := &config.Config{
		EnableSocket:      true,
		Features:          &config.FeatureFlags{EnableWebUI: true},
		QuarantineEnabled: &enabled,
		Telemetry: &config.TelemetryConfig{
			AnonymousID:          "550e8400-e29b-41d4-a716-446655440000",
			AnonymousIDCreatedAt: "2026-04-10T12:00:00Z",
		},
		Servers: []*config.ServerConfig{
			{Name: v13CanaryServerName, URL: v13CanaryServerURL, Protocol: "http"},
		},
	}
	block := map[string]any{
		"enabled":      true,
		"admin_emails": []string{v13CanaryAdminEmail},
		"public_url":   "https://" + v13CanaryIssuerHost,
		"oauth": map[string]any{
			"provider":      "oidc",
			"client_id":     v13CanaryClientID,
			"client_secret": v13CanarySecret,
			"issuer_url":    v13CanaryIssuerURL,
			"groups_claim":  v13CanaryGroupClaim,
			"display_name":  v13CanaryDisplay,
		},
	}
	raw, err := json.Marshal(block)
	if err != nil {
		panic("v13 fixture: marshal server_edition block: " + err.Error())
	}
	if err := json.Unmarshal(raw, &cfg.ServerEdition); err != nil {
		panic("v13 fixture: load server_edition block: " + err.Error())
	}
	return cfg
}

// v13Forbidden is every canary plus the enum-free strings the contract names
// (issuer host, group names, emails, server names). All of them are asserted
// absent from the rendered payload.
func v13Forbidden() []string {
	return []string{
		v13CanaryIssuerHost, v13CanaryIssuerURL, v13CanaryAdminEmail, v13CanaryUserEmail,
		v13CanaryGroupName, v13CanaryGroupClaim, v13CanaryClientID, v13CanarySecret,
		v13CanaryServerName, v13CanaryServerURL, v13CanaryDisplay,
		"corp.example", "@", "issuer", "client_secret", "admin_emails", "groups",
	}
}

func newV13Service(t *testing.T, fx v13Fixture) *Service {
	t.Helper()
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")
	ResetEnvKindForTest()
	t.Cleanup(ResetEnvKindForTest)

	svc := New(fx.cfg, "", "v1.2.3", "test", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{
		serverCount: 1, connectedCount: 1, toolCount: 10,
		routingMode: "retrieve_tools", quarantine: true,
	})
	svc.SetUserCounter(fx.userCounter)
	return svc
}

// TestPayloadV13_EditionFields proves the three v13 fields for the edition
// this binary was built as, plus that env_markers.is_container is still on
// the wire (unchanged, but the container topology is what US7 reads it with).
func TestPayloadV13_EditionFields(t *testing.T) {
	fx := newV13Fixture()
	svc := newV13Service(t, fx)
	payload := svc.BuildPayload()

	if payload.SchemaVersion != SchemaVersion {
		t.Fatalf("payload.SchemaVersion = %d, want SchemaVersion (%d)", payload.SchemaVersion, SchemaVersion)
	}
	if payload.FeatureFlags == nil {
		t.Fatal("feature_flags missing from payload")
	}
	if payload.FeatureFlags.ServerEditionEnabled != fx.wantEnabled {
		t.Errorf("feature_flags.server_edition_enabled = %v, want %v", payload.FeatureFlags.ServerEditionEnabled, fx.wantEnabled)
	}
	if payload.FeatureFlags.IdPProvider != fx.wantIdP {
		t.Errorf("feature_flags.idp_provider = %q, want %q", payload.FeatureFlags.IdPProvider, fx.wantIdP)
	}
	if payload.UserCountBucket != fx.wantBucket {
		t.Errorf("member_count_bucket = %q, want %q", payload.UserCountBucket, fx.wantBucket)
	}
	if payload.EnvMarkers == nil {
		t.Fatal("env_markers missing from payload")
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)
	for _, required := range []string{
		`"server_edition_enabled":`,
		`"idp_provider":"` + fx.wantIdP + `"`,
		`"member_count_bucket":"` + fx.wantBucket + `"`,
		`"is_container":`,
	} {
		if !strings.Contains(js, required) {
			t.Errorf("expected payload to contain %q, missing from:\n%s", required, js)
		}
	}
}

// TestPayloadV13_IdPProviderIsClosedEnum pins the vocabulary: whatever the
// edition reports must be one of the five contract values, never an issuer.
func TestPayloadV13_IdPProviderIsClosedEnum(t *testing.T) {
	fx := newV13Fixture()
	payload := newV13Service(t, fx).BuildPayload()
	switch payload.FeatureFlags.IdPProvider {
	case "google", "github", "microsoft", "oidc", "none":
	default:
		t.Errorf("idp_provider %q is outside the closed enum google|github|microsoft|oidc|none", payload.FeatureFlags.IdPProvider)
	}
	switch payload.UserCountBucket {
	case "0", "1-10", "11-100", "101-1000", "1000+":
	default:
		t.Errorf("member_count_bucket %q is outside the closed enum 0|1-10|11-100|101-1000|1000+", payload.UserCountBucket)
	}
}

// TestPayloadV13_UserCounterNilReportsZero: no counter installed (the personal
// edition, or a short-lived CLI command) reports "0", never an omitted field.
func TestPayloadV13_UserCounterNilReportsZero(t *testing.T) {
	fx := newV13Fixture()
	fx.userCounter = nil
	payload := newV13Service(t, fx).BuildPayload()
	if payload.UserCountBucket != "0" {
		t.Errorf("member_count_bucket with nil counter = %q, want \"0\"", payload.UserCountBucket)
	}
}

// TestPayloadV13_UserCounterErrorOmitsBucket: an installed counter that fails
// omits the field (the only case it is absent), and the payload still builds.
func TestPayloadV13_UserCounterErrorOmitsBucket(t *testing.T) {
	fx := newV13Fixture()
	fx.userCounter = func() (int, error) { return 0, errors.New("bucket unavailable: " + v13CanaryIssuerHost) }
	payload := newV13Service(t, fx).BuildPayload()
	if payload.UserCountBucket != "" {
		t.Errorf("member_count_bucket with erroring counter = %q, want omitted", payload.UserCountBucket)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), `"member_count_bucket"`) {
		t.Errorf("member_count_bucket must be omitted when the counter errors, got:\n%s", data)
	}
	if strings.Contains(string(data), v13CanaryIssuerHost) {
		t.Errorf("counter error text leaked into the payload:\n%s", data)
	}
}

// TestPayloadV13_UserCountBuckets pins the bucket edges to the bucketUpstream
// vocabulary the contract reuses.
func TestPayloadV13_UserCountBuckets(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0"}, {1, "1-10"}, {10, "1-10"}, {11, "11-100"}, {100, "11-100"},
		{101, "101-1000"}, {1000, "101-1000"}, {1001, "1000+"},
	}
	for _, c := range cases {
		fx := newV13Fixture()
		n := c.n
		fx.userCounter = func() (int, error) { return n, nil }
		got := newV13Service(t, fx).BuildPayload().UserCountBucket
		if got != c.want {
			t.Errorf("member_count_bucket(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// TestPayloadV13_NoForbiddenSubstrings is the Spec 042 privacy regression for
// the v13 additions: the issuer host, group names, admin/user emails, client
// id/secret and server names planted in the fixture must not appear anywhere
// in the rendered payload, and ScanForPII must accept it with those strings
// registered as runtime-blocked values.
func TestPayloadV13_NoForbiddenSubstrings(t *testing.T) {
	fx := newV13Fixture()
	fx.userCounter = func() (int, error) { return 7, nil }
	svc := newV13Service(t, fx)
	// Try to leak identity through the counters too — must be dropped.
	svc.Registry().RecordBuiltinTool(v13CanaryServerName + ":" + v13CanaryGroupName)

	payload := svc.BuildPayload()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	js := string(data)
	for _, forbidden := range v13Forbidden() {
		if strings.Contains(js, forbidden) {
			t.Errorf("PRIVACY VIOLATION: payload contains forbidden substring %q\nfull payload:\n%s", forbidden, js)
		}
	}

	withoutBlockedValues(t)
	BlockedValues = []string{
		v13CanaryIssuerHost, v13CanaryAdminEmail, v13CanaryUserEmail,
		v13CanaryGroupName, v13CanaryServerName,
	}
	if err := ScanForPII(data); err != nil {
		t.Errorf("ScanForPII rejected the v13 payload: %v\n%s", err, js)
	}
}

// TestPayloadV13_PassesScanWithCommonUsernameBlocked pins the reason the wire
// key is member_count_bucket and not user_count_bucket. PopulateBlockedValues
// adds the home-dir basename to BlockedValues and ScanForPII rule 2 matches it
// as a substring of the WHOLE wire form — keys included. "user" is the
// username of every `USER user` container image, so a payload key containing
// "user" would have had every heartbeat from such an install refused before
// it left the machine (observed: the whole heartbeat-send suite fails on a
// machine whose HOME is /Users/user). Any future key must keep this green.
func TestPayloadV13_PassesScanWithCommonUsernameBlocked(t *testing.T) {
	fx := newV13Fixture()
	fx.userCounter = func() (int, error) { return 3, nil }
	payload := newV13Service(t, fx).BuildPayload()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	withoutBlockedValues(t)
	for _, username := range []string{"user", "admin", "mcpproxy"} {
		BlockedValues = []string{username}
		if err := ScanForPII(data); err != nil {
			t.Errorf("a payload key collides with the common username/home basename %q: %v\n%s", username, err, data)
		}
	}
}
