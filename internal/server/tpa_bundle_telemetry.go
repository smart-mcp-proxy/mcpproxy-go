package server

import (
	"strconv"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// tpaBundleMaxSequence is 2^53-1, the largest integer every JSON consumer
// represents exactly; the telemetry anonymity backstop refuses anything above.
const tpaBundleMaxSequence = 1<<53 - 1

// tpaBundleTelemetrySnapshot is the heartbeat provider (Spec 101 FR-021,
// research D7): it maps the scanner's live bundle status to the wire snapshot.
func tpaBundleTelemetrySnapshot() *telemetry.TPABundleSnapshot {
	return tpaBundleSnapshotFromInfo(scanner.BundleStatus())
}

// tpaBundleSnapshotFromInfo applies the PROVENANCE gate. The schema line and the
// sequence are reported only when the active bundle's authority is "publisher"
// (the embedded default, or signature-verified against a publisher key).
// Everything else - an unsigned file or fetch, or a bundle verified by an operator key -
// reports "other" with no sequence, REGARDLESS of the version string it carries:
// an operator corpus that copies a publisher version string must not be able to
// masquerade as that publisher release in the fleet statistics.
func tpaBundleSnapshotFromInfo(info scanner.BundleInfo) *telemetry.TPABundleSnapshot {
	snap := &telemetry.TPABundleSnapshot{
		Source:            scanner.BundleSourceFile,
		SignatureVerified: info.SignatureVerified,
		BundleVersion:     "other",
	}
	// Any other source (including an unknown one) stays "file"; the closed
	// enum the backstop enforces is embedded|file|fetch.
	switch info.Source {
	case scanner.BundleSourceEmbedded:
		snap.Source = scanner.BundleSourceEmbedded
	case scanner.BundleSourceFetch:
		snap.Source = scanner.BundleSourceFetch
	}
	if info.Authority != scanner.AuthorityPublisher {
		return snap
	}
	// The backstop refuses a schema line for an external bundle that is not
	// signature-verified; fail closed here too rather than rely on it.
	if snap.Source != scanner.BundleSourceEmbedded && !info.SignatureVerified {
		return snap
	}
	line, ok := tpaSchemaLine(info.BundleVersion)
	if !ok {
		return snap
	}
	var seq uint64
	switch {
	case info.Sequence != "":
		n, err := strconv.ParseUint(info.Sequence, 10, 64)
		if err != nil || n > tpaBundleMaxSequence {
			return snap
		}
		seq = n
	case snap.Source == scanner.BundleSourceEmbedded:
		// The embedded manifest may carry no sequence; report 0.
	default:
		return snap
	}
	snap.BundleVersion = line
	snap.Sequence = &seq
	return snap
}

// tpaSchemaLine reduces a release string such as "0.1.0" to its "<major>.<minor>"
// schema line ("0.1"). ok is false when the string does not start with two
// numeric components of at most three digits.
func tpaSchemaLine(version string) (string, bool) {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return "", false
	}
	for _, p := range parts[:2] {
		if p == "" || len(p) > 3 {
			return "", false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return "", false
			}
		}
	}
	return parts[0] + "." + parts[1], true
}
