package management

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"mcpproxy-go/internal/contracts"
	"mcpproxy-go/internal/secret"
)

// Doctor aggregates health diagnostics from all system components.
// This implements FR-009 through FR-013: comprehensive health diagnostics.
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

	// Collect upstream connection errors and OAuth requirements
	for _, srvRaw := range serversRaw {
		// Extract server fields
		serverName := getStringFromMap(srvRaw, "name")
		lastError := getStringFromMap(srvRaw, "last_error")
		authenticated := getBoolFromMap(srvRaw, "authenticated")
		hasOAuth := srvRaw["oauth"] != nil

		// Check for connection errors
		if lastError != "" {
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

		// Check for OAuth requirements
		if hasOAuth && !authenticated {
			diag.OAuthRequired = append(diag.OAuthRequired, contracts.OAuthRequirement{
				ServerName: serverName,
				State:      "unauthenticated",
				Message:    fmt.Sprintf("Run: mcpproxy auth login --server=%s", serverName),
			})
		}
	}

	// Check for OAuth issues (parameter mismatches)
	diag.OAuthIssues = s.detectOAuthIssues(serversRaw)

	// Check for missing secrets
	diag.MissingSecrets = s.findMissingSecrets(ctx, serversRaw)

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
		"missing_secrets", len(diag.MissingSecrets))

	return diag, nil
}

// findMissingSecrets identifies secrets referenced in configuration but not resolvable.
// This implements T041: helper for identifying which servers reference a secret.
func (s *service) findMissingSecrets(ctx context.Context, serversRaw []map[string]interface{}) []contracts.MissingSecretInfo {
	secretUsage := make(map[string][]string) // secret name -> list of servers using it

	for _, srvRaw := range serversRaw {
		serverName := getStringFromMap(srvRaw, "name")

		// Check environment variables for secret references
		if envRaw, ok := srvRaw["env"]; ok {
			if envMap, ok := envRaw.(map[string]interface{}); ok {
				for _, valueRaw := range envMap {
					if valueStr, ok := valueRaw.(string); ok {
						if ref := parseSecretRef(valueStr); ref != nil {
							if !s.isSecretResolvable(ctx, *ref) {
								secretUsage[ref.Name] = append(secretUsage[ref.Name], serverName)
							}
						}
					}
				}
			}
		}
	}

	// Convert map to slice
	var missingSecrets []contracts.MissingSecretInfo
	for secretName, servers := range secretUsage {
		missingSecrets = append(missingSecrets, contracts.MissingSecretInfo{
			SecretName: secretName,
			UsedBy:     servers,
		})
	}

	return missingSecrets
}

// isSecretResolvable checks if a secret can be resolved (e.g., environment variable exists).
func (s *service) isSecretResolvable(ctx context.Context, ref secret.Ref) bool {
	if s.secretResolver == nil {
		return false
	}

	// Environment variables: quick check without resolving to avoid errors
	if ref.Type == secret.SecretTypeEnv {
		val, ok := os.LookupEnv(ref.Name)
		return ok && val != ""
	}

	// Attempt to resolve; success indicates the secret exists/works
	if _, err := s.secretResolver.Resolve(ctx, ref); err == nil {
		return true
	}

	return false
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

func parseSecretRef(value string) *secret.Ref {
	if !secret.IsSecretRef(value) {
		return nil
	}
	ref, err := secret.ParseSecretRef(value)
	if err != nil {
		return nil
	}
	return ref
}

// detectOAuthIssues identifies OAuth configuration issues like missing parameters.
func (s *service) detectOAuthIssues(serversRaw []map[string]interface{}) []contracts.OAuthIssue {
	var issues []contracts.OAuthIssue

	for _, srvRaw := range serversRaw {
		serverName := getStringFromMap(srvRaw, "name")
		hasOAuth := srvRaw["oauth"] != nil
		lastError := getStringFromMap(srvRaw, "last_error")
		authenticated := getBoolFromMap(srvRaw, "authenticated")

		// Skip servers without OAuth or already authenticated
		if !hasOAuth || authenticated {
			continue
		}

		// Check for parameter-related errors
		if strings.Contains(strings.ToLower(lastError), "requires") &&
			strings.Contains(strings.ToLower(lastError), "parameter") {
			// Extract parameter name from error
			paramName := extractParameterName(lastError)

			issues = append(issues, contracts.OAuthIssue{
				ServerName:    serverName,
				Issue:         "OAuth provider parameter mismatch",
				Error:         lastError,
				MissingParams: []string{paramName},
				Resolution: "This requires MCPProxy support for OAuth extra_params. " +
					"Track progress: https://github.com/smart-mcp-proxy/mcpproxy-go/issues",
				DocumentationURL: "https://www.rfc-editor.org/rfc/rfc8707.html",
			})
		}
	}

	return issues
}

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

func getBoolFromMap(m map[string]interface{}, key string) bool {
	if val, ok := m[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}
