package server

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

func seqPtr(v uint64) *uint64 { return &v }

// TestTPABundleSnapshotFromInfo pins the FR-021 provenance gate: the schema
// line and the sequence leave the install only for the embedded default or a
// bundle verified against a PUBLISHER key.
func TestTPABundleSnapshotFromInfo(t *testing.T) {
	tests := []struct {
		name string
		info scanner.BundleInfo
		want telemetry.TPABundleSnapshot
	}{
		{
			name: "embedded default with a sequence",
			info: scanner.BundleInfo{Source: scanner.BundleSourceEmbedded, Authority: scanner.AuthorityPublisher, BundleVersion: "0.1.0", Sequence: "42"},
			want: telemetry.TPABundleSnapshot{Source: "embedded", BundleVersion: "0.1", Sequence: seqPtr(42)},
		},
		{
			name: "embedded default without a sequence reports 0",
			info: scanner.BundleInfo{Source: scanner.BundleSourceEmbedded, Authority: scanner.AuthorityPublisher, BundleVersion: "0.1.0"},
			want: telemetry.TPABundleSnapshot{Source: "embedded", BundleVersion: "0.1", Sequence: seqPtr(0)},
		},
		{
			name: "publisher-verified file bundle",
			info: scanner.BundleInfo{Source: scanner.BundleSourceFile, Authority: scanner.AuthorityPublisher, SignatureVerified: true, BundleVersion: "0.2.7", Sequence: "9"},
			want: telemetry.TPABundleSnapshot{Source: "file", SignatureVerified: true, BundleVersion: "0.2", Sequence: seqPtr(9)},
		},
		{
			name: "operator-signed bundle copying a publisher version string reports other",
			info: scanner.BundleInfo{Source: scanner.BundleSourceFile, Authority: scanner.AuthorityOperator, SignatureVerified: true, BundleVersion: "0.1.0", Sequence: "42"},
			want: telemetry.TPABundleSnapshot{Source: "file", SignatureVerified: true, BundleVersion: "other"},
		},
		{
			name: "unsigned file bundle copying a publisher version string reports other",
			info: scanner.BundleInfo{Source: scanner.BundleSourceFile, BundleVersion: "0.1.0", Sequence: "42"},
			want: telemetry.TPABundleSnapshot{Source: "file", BundleVersion: "other"},
		},
		{
			name: "unparseable publisher version reports other",
			info: scanner.BundleInfo{Source: scanner.BundleSourceEmbedded, Authority: scanner.AuthorityPublisher, BundleVersion: "nightly-2026", Sequence: "3"},
			want: telemetry.TPABundleSnapshot{Source: "embedded", BundleVersion: "other"},
		},
		{
			name: "publisher sequence that is not a decimal reports other",
			info: scanner.BundleInfo{Source: scanner.BundleSourceEmbedded, Authority: scanner.AuthorityPublisher, BundleVersion: "0.1.0", Sequence: "abc"},
			want: telemetry.TPABundleSnapshot{Source: "embedded", BundleVersion: "other"},
		},
		{
			name: "publisher sequence above 2^53-1 reports other",
			info: scanner.BundleInfo{Source: scanner.BundleSourceEmbedded, Authority: scanner.AuthorityPublisher, BundleVersion: "0.1.0", Sequence: "9007199254740992"},
			want: telemetry.TPABundleSnapshot{Source: "embedded", BundleVersion: "other"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tpaBundleSnapshotFromInfo(tc.info)
			require.NotNil(t, got)
			assert.Equal(t, tc.want, *got)
		})
	}
}

// TestTPABundleSnapshotPassesAnonymityBackstop runs every mapped shape through
// the real heartbeat builder and the telemetry anonymity backstop.
func TestTPABundleSnapshotPassesAnonymityBackstop(t *testing.T) {
	prevBlocked := telemetry.BlockedValues
	telemetry.BlockedValues = nil
	t.Cleanup(func() { telemetry.BlockedValues = prevBlocked })

	infos := []scanner.BundleInfo{
		{Source: scanner.BundleSourceEmbedded, Authority: scanner.AuthorityPublisher, BundleVersion: "0.1.0", Sequence: "42"},
		{Source: scanner.BundleSourceEmbedded, Authority: scanner.AuthorityPublisher, BundleVersion: "0.1.0"},
		{Source: scanner.BundleSourceFile, Authority: scanner.AuthorityPublisher, SignatureVerified: true, BundleVersion: "0.1.0", Sequence: "7"},
		{Source: scanner.BundleSourceFile, Authority: scanner.AuthorityOperator, SignatureVerified: true, BundleVersion: "0.1.0", Sequence: "7"},
		{Source: scanner.BundleSourceFile, BundleVersion: "0.1.0", Sequence: "7"},
		{Source: scanner.BundleSourceFile, BundleVersion: "weird /home/user/x"},
	}
	for _, info := range infos {
		snap := tpaBundleSnapshotFromInfo(info)
		svc := newBundleTelemetryService(t)
		svc.SetTPABundleProvider(func() *telemetry.TPABundleSnapshot { return snap })
		data, err := json.Marshal(svc.BuildPayload())
		require.NoError(t, err)
		require.NoError(t, telemetry.ScanForPII(data), "payload for %+v: %s", info, data)
	}
}

func newBundleTelemetryService(t *testing.T) *telemetry.Service {
	t.Helper()
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("CI", "")
	t.Setenv("MCPPROXY_TELEMETRY", "")
	cfg := &config.Config{
		EnableSocket: true,
		Features:     &config.FeatureFlags{EnableWebUI: true},
		Telemetry: &config.TelemetryConfig{
			AnonymousID:          "550e8400-e29b-41d4-a716-446655440000",
			AnonymousIDCreatedAt: "2026-04-10T12:00:00Z",
		},
	}
	return telemetry.New(cfg, "", "v1.2.3", "personal", zap.NewNop())
}
