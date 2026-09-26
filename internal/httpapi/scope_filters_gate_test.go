package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 109-k (activity-scope-filters, FR-080a): version-skew gate on the
// scope filter parameters "profile", "client", "token" (and the existing
// "agent" alias of "token"). url-filter-contract.md "Backend gate" owns this
// behaviour. With the supported list empty (this PR's value), every one of
// these parameters must be rejected with 400 unsupported_scope_filter on
// every gated route, except "agent" on GET /activity and /activity/export,
// which already honours it today.

func TestRejectUnsupportedScopeFilters_EmptySupportedList(t *testing.T) {
	prev := scopeFilterSupportedFilters
	scopeFilterSupportedFilters = nil
	t.Cleanup(func() { scopeFilterSupportedFilters = prev })

	tests := []struct {
		name     string
		query    string
		honoured []string
		wantOK   bool
		wantErr  string
	}{
		{name: "no scope params", query: "", wantOK: true},
		{name: "existing param untouched", query: "session=ws-abc&status=success", wantOK: true},
		{name: "profile rejected", query: "profile=work", wantOK: false, wantErr: "profile"},
		{name: "client rejected", query: "client=cursor", wantOK: false, wantErr: "client"},
		{name: "token rejected", query: "token=ci-bot", wantOK: false, wantErr: "token"},
		{name: "agent gated like token when not honoured", query: "agent=ci-bot", wantOK: false, wantErr: "agent"},
		{name: "agent exempt when honoured", query: "agent=ci-bot", honoured: []string{"agent"}, wantOK: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/activity?"+tt.query, nil)
			rec := httptest.NewRecorder()

			ok := rejectUnsupportedScopeFilters(rec, req, tt.honoured...)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, 200, rec.Code, "no response should have been written")
				return
			}
			require.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), "unsupported_scope_filter")
			assert.Contains(t, rec.Body.String(), tt.wantErr)
		})
	}
}

func TestRejectUnsupportedScopeFilters_ActivityAgentExemption(t *testing.T) {
	prev := scopeFilterSupportedFilters
	scopeFilterSupportedFilters = nil
	t.Cleanup(func() { scopeFilterSupportedFilters = prev })

	// GET /activity and /activity/export honour `agent` today: unchanged 200
	// (this PR's call sites pass "agent" in honoured for these two routes).
	for _, path := range []string{"/api/v1/activity", "/api/v1/activity/export"} {
		req := httptest.NewRequest(http.MethodGet, path+"?agent=alice", nil)
		rec := httptest.NewRecorder()
		ok := rejectUnsupportedScopeFilters(rec, req, "agent")
		assert.Truef(t, ok, "%s should honour agent unchanged", path)
	}

	// /activity/summary, /activity/usage and /sessions ignore agent today, so
	// it is gated exactly like token (codex round 4): these call sites pass no
	// "agent" in honoured.
	for _, path := range []string{"/api/v1/activity/summary", "/api/v1/activity/usage", "/api/v1/sessions"} {
		req := httptest.NewRequest(http.MethodGet, path+"?agent=alice", nil)
		rec := httptest.NewRecorder()
		ok := rejectUnsupportedScopeFilters(rec, req)
		assert.Falsef(t, ok, "%s must gate agent like token", path)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	}
}

func TestRejectUnsupportedScopeFilters_SupportedAloneDoesNotLiftGate(t *testing.T) {
	prev := scopeFilterSupportedFilters
	scopeFilterSupportedFilters = []string{"profile", "client", "token"}
	t.Cleanup(func() { scopeFilterSupportedFilters = prev })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tools?profile=work", nil)
	rec := httptest.NewRecorder()
	assert.False(t, rejectUnsupportedScopeFilters(rec, req),
		"supported does not itself lift the gate; the handler must also honour the name")

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/tools?profile=work", nil)
	rec2 := httptest.NewRecorder()
	assert.True(t, rejectUnsupportedScopeFilters(rec2, req2, "profile"),
		"supported AND honoured lifts the gate")
}

func TestRejectUnsupportedScopeFilters_NoStorageReadBeforeGate(t *testing.T) {
	// The gate must run before any storage read: a rejected request must not
	// call through to the controller/storage layer. This is asserted at the
	// handler level (activity_handlers_test.go-style integration tests are out
	// of scope here); this unit test pins the pure-function contract that
	// makes that possible — the gate never touches s.controller/s.storage.
	prev := scopeFilterSupportedFilters
	scopeFilterSupportedFilters = nil
	t.Cleanup(func() { scopeFilterSupportedFilters = prev })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tools?client=cursor", nil)
	rec := httptest.NewRecorder()
	ok := rejectUnsupportedScopeFilters(rec, req)
	require.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestScopeFiltersFeatureValue(t *testing.T) {
	prev := scopeFilterSupportedFilters
	defer func() { scopeFilterSupportedFilters = prev }()

	scopeFilterSupportedFilters = nil
	assert.Empty(t, scopeFiltersFeatureValue(), "empty list must not appear in features.scope_filters")

	scopeFilterSupportedFilters = []string{"profile", "client"}
	assert.Equal(t, []string{"profile", "client"}, scopeFiltersFeatureValue())
}
