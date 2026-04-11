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

// recordDoctorTelemetry synthesizes a fixed set of named doctor checks from
// the contracts.Diagnostics result and feeds them to the Tier 2 registry.
// Spec 042 User Story 9.
//
// The check name set is hard-coded (never derived from user input) so that
// the histogram has stable, low-cardinality keys.
func recordDoctorTelemetry(reg *telemetry.CounterRegistry, diag *contracts.Diagnostics) {
	if reg == nil || diag == nil {
		return
	}
	results := []telemetry.DoctorCheckResult{
		doctorCheckResult{name: "upstream_connections", pass: len(diag.UpstreamErrors) == 0},
		doctorCheckResult{name: "oauth_required", pass: len(diag.OAuthRequired) == 0},
		doctorCheckResult{name: "oauth_issues", pass: len(diag.OAuthIssues) == 0},
		doctorCheckResult{name: "missing_secrets", pass: len(diag.MissingSecrets) == 0},
		doctorCheckResult{name: "runtime_warnings", pass: len(diag.RuntimeWarnings) == 0},
		doctorCheckResult{name: "deprecated_configs", pass: len(diag.DeprecatedConfigs) == 0},
	}
	if diag.DockerStatus != nil {
		// "Healthy" is the only safe boolean signal — DockerStatus has many fields
		// but they often contain hostnames/paths, so we only emit a single
		// pass/fail bit.
		dockerOK := len(diag.UpstreamErrors) == 0
		results = append(results, doctorCheckResult{name: "docker_status", pass: dockerOK})
	}
	reg.RecordDoctorRun(results)
}
