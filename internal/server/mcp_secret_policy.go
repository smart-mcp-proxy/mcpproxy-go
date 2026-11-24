package server

import (
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

func joinMCPSecretPolicyMessages(errs []config.ValidationError) string {
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, e.Message)
	}
	return strings.Join(msgs, "; ")
}
