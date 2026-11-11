package core

import (
	"errors"
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// ErrUpstreamSecretPolicy blocks connect/spawn for one upstream without stopping the daemon.
var ErrUpstreamSecretPolicy = errors.New("upstream secret reference refused by policy")

// UpstreamSecretPolicyError carries the fixed public message for status, doctor and logs.
type UpstreamSecretPolicyError struct {
	ServerName string
	Public     string
	Field      string
}

func (e *UpstreamSecretPolicyError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("server %q: %s", e.ServerName, e.Public)
}

func (e *UpstreamSecretPolicyError) Unwrap() error {
	return ErrUpstreamSecretPolicy
}

func policyExpansionError(serverName string, e secret.SecretExpansionError) *UpstreamSecretPolicyError {
	pub := secret.PublicPolicyMessage(e.Err)
	return &UpstreamSecretPolicyError{
		ServerName: serverName,
		Public:     pub,
		Field:      e.FieldPath,
	}
}
