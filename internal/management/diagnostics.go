package management

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
)

// extractHealthFromMap extracts health status from a server map.
// The health can be stored as either *contracts.HealthStatus (from GetAllServers)
// or as map[string]interface{} (from JSON deserialization).
func extractHealthFromMap(srvRaw map[string]interface{}) (action, detail string) {
	healthRaw, ok := srvRaw["health"]
	if !ok || healthRaw == nil {
		return "", ""
	}

	// Try direct struct pointer first (from GetAllServers)
	if hs, ok := healthRaw.(*contracts.HealthStatus); ok && hs != nil {
		return hs.Action, hs.Detail
	}

	// Try map[string]interface{} (from JSON deserialization)
	if healthMap, ok := healthRaw.(map[string]interface{}); ok && healthMap != nil {
		action = getStringFromMap(healthMap, "action")
		detail = getStringFromMap(healthMap, "detail")
		return action, detail
	}

	return "", ""
}

// Doctor aggregates health diagnostics from all system components.
// This implements FR-009 through FR-013: comprehensive health diagnostics.
// Refactored to aggregate from Health.Action (single source of truth).
func (s *service) Doctor(ctx context.Context) (*contracts.Diagnostics, error) {
	// Get all servers from runtime
	serversRaw, err := s.runtime.GetAllServers()
	if err != nil {
		s.logger.Errorw("Failed to get servers for diagnostics", "error", err)
		return nil, fmt.Errorf("failed to get servers: %w", err)
	}

	diag := &contracts.Diagnostics{
		Timestamp:       time.Now(),
		UpstreamErrors:  []contracts.UpstreamError{},
		OAuthRequired:   []contracts.OAuthRequirement{},
		OAuthIssues:     []contracts.OAuthIssue{},
		MissingSecrets:  []contracts.MissingSecretInfo{},
		RuntimeWarnings: []string{},
	}

	// Aggregate by secret name for cross-cutting view
	secretsMap := make(map[string][]string) // secret name -> list of servers using it

	// Aggregate diagnostics from Health.Action (single source of truth)
	for _, srvRaw := range serversRaw {
		serverName := getStringFromMap(srvRaw, "name")
		lastError := getStringFromMap(srvRaw, "last_error")

		// Extract health status from server using helper that handles both
		// struct pointer (from GetAllServers) and map (from JSON)
		healthAction, healthDetail := extractHealthFromMap(srvRaw)

		// Aggregate based on Health.Action
		switch healthAction {
		case health.ActionRestart:
			errorTime := time.Now()
			if errorTimeStr := getStringFromMap(srvRaw, "error_time"); errorTimeStr != "" {
				if parsed, err := time.Parse(time.RFC3339, errorTimeStr); err == nil {
					errorTime = parsed
				}
			}
			diag.UpstreamErrors = append(diag.UpstreamErrors, contracts.UpstreamError{
				ServerName:   serverName,
				ErrorMessage: healthDetail,
				Timestamp:    errorTime,
			})

		case health.ActionLogin:
			diag.OAuthRequired = append(diag.OAuthRequired, contracts.OAuthRequirement{
				ServerName: serverName,
				State:      "unauthenticated",
				Message:    fmt.Sprintf("Run: mcpproxy auth login --server=%s", serverName),
			})

		case health.ActionConfigure:
			// Extract parameter name from error
			paramName := extractParameterName(healthDetail)
			diag.OAuthIssues = append(diag.OAuthIssues, contracts.OAuthIssue{
				ServerName:    serverName,
				Issue:         "OAuth provider parameter mismatch",
				Error:         healthDetail,
				MissingParams: []string{paramName},
				Resolution: "MCPProxy auto-detects RFC 8707 resource parameter from Protected Resource Metadata (RFC 9728). " +
					"Check detected values: mcpproxy auth status --server=" + serverName + ". " +
					"To override, add extra_params.resource to OAuth config.",
				DocumentationURL: "https://www.rfc-editor.org/rfc/rfc8707.html",
			})

		case health.ActionSetSecret:
			// Group by secret name for cross-cutting view
			secretName := healthDetail
			if secretName != "" {
				secretsMap[secretName] = append(secretsMap[secretName], serverName)
			}
		}

		// Fallback: check for errors without health action for backward compatibility
		// Only add to UpstreamErrors if not already handled by health action
		if healthAction == "" && lastError != "" {
			errorTime := time.Now()
			if errorTimeStr := getStringFromMap(srvRaw, "error_time"); errorTimeStr != "" {
				if parsed, err := time.Parse(time.RFC3339, errorTimeStr); err == nil {
					errorTime = parsed
				}
			}
			diag.UpstreamErrors = append(diag.UpstreamErrors, contracts.UpstreamError{
				ServerName:   serverName,
				ErrorMessage: lastError,
				Timestamp:    errorTime,
			})
		}
	}

	// Convert secrets map to slice
	for secretName, servers := range secretsMap {
		diag.MissingSecrets = append(diag.MissingSecrets, contracts.MissingSecretInfo{
			SecretName: secretName,
			UsedBy:     servers,
		})
	}

	// Check Docker status if isolation is enabled
	if s.config.DockerIsolation != nil && s.config.DockerIsolation.Enabled {
		diag.DockerStatus = s.checkDockerDaemon()
	}

	// Calculate total issues
	diag.TotalIssues = len(diag.UpstreamErrors) + len(diag.OAuthRequired) +
		len(diag.OAuthIssues) + len(diag.MissingSecrets) + len(diag.RuntimeWarnings)

	s.logger.Infow("Doctor diagnostics completed",
		"total_issues", diag.TotalIssues,
		"upstream_errors", len(diag.UpstreamErrors),
		"oauth_required", len(diag.OAuthRequired),
		"oauth_issues", len(diag.OAuthIssues),
		"missing_secrets", len(diag.MissingSecrets))

	return diag, nil
}


// checkDockerDaemon checks if Docker daemon is available and returns status.
// This implements T042: helper for checking Docker availability.
func (s *service) checkDockerDaemon() *contracts.DockerStatus {
	status := &contracts.DockerStatus{
		Available: false,
	}

	// Try to run `docker info` to check daemon availability
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
	output, err := cmd.Output()

	if err != nil {
		status.Available = false
		status.Error = err.Error()
		s.logger.Debugw("Docker daemon not available", "error", err)
	} else {
		status.Available = true
		status.Version = strings.TrimSpace(string(output))
		s.logger.Debugw("Docker daemon available", "version", status.Version)
	}

	return status
}

// Helper functions to extract fields from map[string]interface{}

// extractParameterName extracts the parameter name from an error message.
// Example: "requires 'resource' parameter" -> "resource"
func extractParameterName(errorMsg string) string {
	// Look for pattern: 'parameter_name' parameter
	start := strings.Index(errorMsg, "'")
	if start == -1 {
		return "unknown"
	}
	end := strings.Index(errorMsg[start+1:], "'")
	if end == -1 {
		return "unknown"
	}
	return errorMsg[start+1 : start+1+end]
}

func getStringFromMap(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

