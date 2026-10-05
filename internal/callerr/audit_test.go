package callerr

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
)

// FR-047: AuditClass is a TOTAL mapping from (class, domain, status) onto the
// frozen Spec 107 vocabulary.
func TestOutcomeAuditClassTotalMapping(t *testing.T) {
	tests := []struct {
		o    Outcome
		want audit.ErrorClass
	}{
		{Outcome{Class: ClassNetwork, Domain: DomainUpstream}, audit.ErrorClassUpstreamUnavailable},
		{Outcome{Class: ClassTimeout, Domain: DomainUpstream}, audit.ErrorClassUpstreamTimeout},
		{Outcome{Class: ClassTimeout, Domain: DomainProxy}, audit.ErrorClassUpstreamTimeout},
		{Outcome{Class: ClassHTTP, Domain: DomainUpstream, HTTPStatus: 502}, audit.ErrorClassUpstreamUnavailable},
		{Outcome{Class: ClassHTTP, Domain: DomainUpstream, HTTPStatus: 503}, audit.ErrorClassUpstreamUnavailable},
		{Outcome{Class: ClassHTTP, Domain: DomainUpstream, HTTPStatus: 504}, audit.ErrorClassUpstreamUnavailable},
		{Outcome{Class: ClassHTTP, Domain: DomainUpstream, HTTPStatus: 500}, audit.ErrorClassUpstreamError},
		{Outcome{Class: ClassHTTP, Domain: DomainUpstream, HTTPStatus: 429}, audit.ErrorClassUpstreamError},
		{Outcome{Class: ClassHTTP, Domain: DomainUpstream}, audit.ErrorClassUpstreamError},
		{Outcome{Class: ClassJSONRPC, Domain: DomainUpstream}, audit.ErrorClassUpstreamError},
		{Outcome{Class: ClassToolError, Domain: DomainUpstream}, audit.ErrorClassUpstreamError},
		{Outcome{Class: ClassSessionTerminated, Domain: DomainUpstream}, audit.ErrorClassUpstreamError},
		{Outcome{Class: ClassAuth, Domain: DomainUpstream}, audit.ErrorClassUpstreamError},
		{Outcome{Class: ClassProxyPolicy, Domain: DomainClient}, audit.ErrorClassValidation},
		{Outcome{Class: ClassProxyPolicy, Domain: DomainProxy}, audit.ErrorClassInternal},
		{Outcome{Class: ClassCancelled, Domain: DomainClient}, audit.ErrorClassCancelled},
		{Outcome{Class: ClassProxyInternal, Domain: DomainProxy}, audit.ErrorClassInternal},
		{Outcome{}, audit.ErrorClassInternal},
		{Outcome{Class: Class("bogus")}, audit.ErrorClassInternal},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, tc.o.AuditClass(), "%+v", tc.o)
	}
}

// Every value audit.ErrorClassOf produces today for a production error maps
// to the same audit class through the new classifier.
func TestAuditClassAgreesWithErrorClassOf(t *testing.T) {
	errs := []error{
		contextCanceled(), contextDeadline(), sanitisationErr(), limitErr(), validationErr(),
	}
	for _, err := range errs {
		o, ok := Classify(nil, err, Facts{Dispatched: true})
		assert.True(t, ok)
		assert.Equal(t, audit.ErrorClassOf(err), o.AuditClass(), "%v", err)
	}
}
