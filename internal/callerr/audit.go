package callerr

import (
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
)

// AuditClass maps the outcome onto the frozen Spec 107 audit `error_class`
// vocabulary (FR-047), so the audit line and the activity record agree. It is
// total: any Outcome, including the zero value, maps to a member.
func (o Outcome) AuditClass() audit.ErrorClass {
	if o.audit != "" {
		return o.audit
	}
	switch o.Class {
	case ClassNetwork:
		return audit.ErrorClassUpstreamUnavailable
	case ClassTimeout:
		return audit.ErrorClassUpstreamTimeout
	case ClassHTTP:
		switch o.HTTPStatus {
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return audit.ErrorClassUpstreamUnavailable
		}
		return audit.ErrorClassUpstreamError
	case ClassJSONRPC, ClassToolError, ClassSessionTerminated, ClassAuth:
		return audit.ErrorClassUpstreamError
	case ClassProxyPolicy:
		if o.Domain == DomainClient {
			return audit.ErrorClassValidation
		}
		return audit.ErrorClassInternal
	case ClassCancelled:
		return audit.ErrorClassCancelled
	default:
		return audit.ErrorClassInternal
	}
}
