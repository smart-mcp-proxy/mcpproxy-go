package httpapi

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// doctorCheckResult adapts a synthesized doctor check (name + pass/fail) to
// the telemetry.DoctorCheckResult interface so we can call RecordDoctorRun
// without coupling the registry to internal/contracts.
type doctorCheckResult struct {
	name string
	pass bool
}

func (d doctorCheckResult) GetName() string { return d.name }
func (d doctorCheckResult) IsPass() bool    { return d.pass }

// buildDoctorCheckResults synthesizes the fixed, low-cardinality set of named
// doctor checks emitted by recordDoctorTelemetry. Extracted as a pure helper so
// that the mapping from contracts.Diagnostics fields to telemetry results can
// be unit-tested without spinning up a CounterRegistry.
//
// The check name set is hard-coded (never derived from user input) so that
// the histogram has stable, low-cardinality keys.
func buildDoctorCheckResults(diag *contracts.Diagnostics) []doctorCheckResult {
	if diag == nil {
		return nil
	}
	results := []doctorCheckResult{
		{name: "upstream_connections", pass: len(diag.UpstreamErrors) == 0},
		{name: "oauth_required", pass: len(diag.OAuthRequired) == 0},
		{name: "oauth_issues", pass: len(diag.OAuthIssues) == 0},
		{name: "missing_secrets", pass: len(diag.MissingSecrets) == 0},
		{name: "runtime_warnings", pass: len(diag.RuntimeWarnings) == 0},
		{name: "deprecated_configs", pass: len(diag.DeprecatedConfigs) == 0},
	}
	if diag.DockerStatus != nil {
		// Read the actual Docker daemon probe result populated by
		// internal/management/diagnostics.go:checkDockerDaemon. "Available"
		// is the only safe boolean signal — DockerStatus has additional
		// fields (Version, Error) that may contain hostnames/paths, so we
		// only emit a single pass/fail bit here.
		results = append(results, doctorCheckResult{
			name: "docker_status",
			pass: diag.DockerStatus.Available,
		})
	}
	return results
}

// recordDoctorTelemetry synthesizes a fixed set of named doctor checks from
// the contracts.Diagnostics result and feeds them to the Tier 2 registry.
// Spec 042 User Story 9.
func recordDoctorTelemetry(reg *telemetry.CounterRegistry, diag *contracts.Diagnostics) {
	if reg == nil || diag == nil {
		return
	}
	built := buildDoctorCheckResults(diag)
	results := make([]telemetry.DoctorCheckResult, 0, len(built))
	for _, r := range built {
		results = append(results, r)
	}
	reg.RecordDoctorRun(results)
}
