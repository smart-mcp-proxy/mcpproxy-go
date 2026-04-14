package httpapi

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// findCheck returns the doctorCheckResult with the given name, or ok=false.
func findCheck(results []doctorCheckResult, name string) (doctorCheckResult, bool) {
	for _, r := range results {
		if r.name == name {
			return r, true
		}
	}
	return doctorCheckResult{}, false
}

func TestBuildDoctorCheckResults_DockerHealthyWithUpstreamErrors(t *testing.T) {
	diag := &contracts.Diagnostics{
		DockerStatus: &contracts.DockerStatus{Available: true},
		UpstreamErrors: []contracts.UpstreamError{
			{ServerName: "github", ErrorMessage: "connection refused", Timestamp: time.Now()},
		},
	}

	results := buildDoctorCheckResults(diag)

	docker, ok := findCheck(results, "docker_status")
	assert.True(t, ok, "docker_status should be emitted when DockerStatus != nil")
	assert.True(t, docker.pass, "docker_status should pass when Docker daemon is Available, even if unrelated upstreams have errors")

	upstream, ok := findCheck(results, "upstream_connections")
	assert.True(t, ok, "upstream_connections should always be emitted")
	assert.False(t, upstream.pass, "upstream_connections should fail when there are upstream errors")
}

func TestBuildDoctorCheckResults_DockerUnavailableNoUpstreamErrors(t *testing.T) {
	diag := &contracts.Diagnostics{
		DockerStatus:   &contracts.DockerStatus{Available: false, Error: "dial unix /var/run/docker.sock: connect: no such file or directory"},
		UpstreamErrors: nil,
	}

	results := buildDoctorCheckResults(diag)

	docker, ok := findCheck(results, "docker_status")
	assert.True(t, ok, "docker_status should be emitted when DockerStatus != nil")
	assert.False(t, docker.pass, "docker_status should fail when Docker daemon is not Available")

	upstream, ok := findCheck(results, "upstream_connections")
	assert.True(t, ok, "upstream_connections should always be emitted")
	assert.True(t, upstream.pass, "upstream_connections should pass when there are no upstream errors")
}

func TestBuildDoctorCheckResults_DockerNilNoUpstreamErrors(t *testing.T) {
	diag := &contracts.Diagnostics{
		DockerStatus:   nil,
		UpstreamErrors: nil,
	}

	results := buildDoctorCheckResults(diag)

	_, ok := findCheck(results, "docker_status")
	assert.False(t, ok, "docker_status should NOT be emitted when DockerStatus is nil (Docker isolation disabled)")

	upstream, ok := findCheck(results, "upstream_connections")
	assert.True(t, ok, "upstream_connections should be emitted regardless of DockerStatus")
	assert.True(t, upstream.pass, "upstream_connections should pass when there are no upstream errors")
}

func TestBuildDoctorCheckResults_AllHealthy(t *testing.T) {
	diag := &contracts.Diagnostics{
		DockerStatus:   &contracts.DockerStatus{Available: true, Version: "24.0.7"},
		UpstreamErrors: nil,
	}

	results := buildDoctorCheckResults(diag)

	docker, ok := findCheck(results, "docker_status")
	assert.True(t, ok)
	assert.True(t, docker.pass, "docker_status should pass when Docker is Available and no upstream errors")

	upstream, ok := findCheck(results, "upstream_connections")
	assert.True(t, ok)
	assert.True(t, upstream.pass, "upstream_connections should pass when no upstream errors")
}

func TestBuildDoctorCheckResults_NilDiagReturnsNil(t *testing.T) {
	results := buildDoctorCheckResults(nil)
	assert.Nil(t, results)
}
