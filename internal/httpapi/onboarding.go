package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// OnboardingState is the response shape for GET /api/v1/onboarding/state.
// It bundles the wizard's two predicates (does the user have any client
// connected? any server configured?) with the persisted engagement record,
// so the frontend can decide whether to auto-show the wizard and which
// steps to render.
type OnboardingStateResponse struct {
	// HasConnectedClient is true if at least one supported AI client currently
	// has mcpproxy registered in its config.
	HasConnectedClient bool `json:"has_connected_client"`

	// HasConfiguredServer is true if at least one upstream MCP server is
	// configured (regardless of current connection health).
	HasConfiguredServer bool `json:"has_configured_server"`

	// ConnectedClientCount is the number of supported clients currently
	// pointing at mcpproxy.
	ConnectedClientCount int `json:"connected_client_count"`

	// ConnectedClientIDs are the identifiers of supported clients currently
	// pointing at mcpproxy. Drawn exclusively from the fixed adapter table —
	// user-entered values never appear here.
	ConnectedClientIDs []string `json:"connected_client_ids"`

	// ConfiguredServerCount is the number of upstream MCP servers configured
	// in mcpproxy (counts both enabled and disabled).
	ConfiguredServerCount int `json:"configured_server_count"`

	// State is the persisted wizard engagement record. Engaged is true once
	// the wizard was shown and the user completed or skipped it.
	State storage.OnboardingState `json:"state"`

	// ShouldShowWizard is the derived flag the frontend uses to decide
	// whether to auto-show. True when not engaged and IncompleteTabCount > 0
	// (Spec 046 v2 — semantics widened to also count the Verify tab).
	ShouldShowWizard bool `json:"should_show_wizard"`

	// --- Spec 046 v2 additions ---

	// FirstMCPClientEver is true once any MCP client has successfully completed
	// an `initialize` round-trip with this mcpproxy. Sourced from the Spec 044
	// activation bucket. Drives the Verify tab's "green check" state.
	FirstMCPClientEver bool `json:"first_mcp_client_ever"`

	// MCPClientsSeenEver is the capped list of recognized client names that
	// have ever called this mcpproxy. Names come from the MCP `initialize`
	// payload's `clientInfo.name` field, sanitized. Surfaces on the Verify tab
	// so the user can see whether their real IDE — not a test client — has
	// connected.
	MCPClientsSeenEver []string `json:"mcp_clients_seen_ever"`

	// IncompleteTabCount is the number of wizard tabs whose state is incomplete.
	// Drives the sidebar Setup entry's badge. Formula:
	//   +1 if HasConnectedClient == false
	//   +1 if HasConfiguredServer == false
	//   +1 if FirstMCPClientEver == false
	IncompleteTabCount int `json:"incomplete_tab_count"`
}

// OnboardingMarkRequest is the request body for /api/v1/onboarding/mark
// endpoints. Each step's status can be set independently, and the wizard
// can be marked engaged in the same call.
type OnboardingMarkRequest struct {
	// Engaged marks the wizard as engaged (completed or explicitly skipped).
	// Once true, the wizard does not auto-show again.
	Engaged bool `json:"engaged"`

	// ConnectStepStatus is one of: "", "completed", "skipped". Empty
	// preserves the existing value.
	ConnectStepStatus string `json:"connect_step_status,omitempty"`

	// ServerStepStatus is one of: "", "completed", "skipped". Empty
	// preserves the existing value.
	ServerStepStatus string `json:"server_step_status,omitempty"`

	// MarkShown records the wizard's first display time if not already set.
	MarkShown bool `json:"mark_shown,omitempty"`
}

// handleGetOnboardingState godoc
// @Summary     Get onboarding wizard state and predicates (Spec 046)
// @Description Returns the wizard engagement record alongside live predicates
// @Description (whether any client is connected, whether any server is configured),
// @Description plus a derived ShouldShowWizard flag the frontend can rely on.
// @Tags        onboarding
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Success     200 {object} contracts.APIResponse "OnboardingStateResponse"
// @Failure     503 {object} contracts.ErrorResponse "Service unavailable"
// @Router      /api/v1/onboarding/state [get]
func (s *Server) handleGetOnboardingState(w http.ResponseWriter, r *http.Request) {
	resp, err := s.computeOnboardingState()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("compute onboarding state: %v", err))
		return
	}
	s.writeSuccess(w, resp)
}

// handleMarkOnboardingState godoc
// @Summary     Mark onboarding wizard state (Spec 046)
// @Description Updates wizard engagement and per-step status. Once engaged is
// @Description true, the wizard does not auto-show again, even if state regresses.
// @Tags        onboarding
// @Accept      json
// @Produce     json
// @Security    ApiKeyAuth
// @Security    ApiKeyQuery
// @Param       body body OnboardingMarkRequest true "Mark request"
// @Success     200 {object} contracts.APIResponse "Updated OnboardingStateResponse"
// @Failure     400 {object} contracts.ErrorResponse "Bad request"
// @Failure     503 {object} contracts.ErrorResponse "Service unavailable"
// @Router      /api/v1/onboarding/mark [post]
func (s *Server) handleMarkOnboardingState(w http.ResponseWriter, r *http.Request) {
	var req OnboardingMarkRequest
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeError(w, r, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
			return
		}
	}

	if !validStepStatus(req.ConnectStepStatus) || !validStepStatus(req.ServerStepStatus) {
		s.writeError(w, r, http.StatusBadRequest, `step status must be "", "completed", or "skipped"`)
		return
	}

	state, err := s.controller.GetOnboardingState()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("read state: %v", err))
		return
	}
	if state == nil {
		state = &storage.OnboardingState{}
	}

	now := time.Now()
	if req.MarkShown && state.FirstShownAt == nil {
		t := now
		state.FirstShownAt = &t
	}
	if req.ConnectStepStatus != "" {
		state.ConnectStepStatus = req.ConnectStepStatus
	}
	if req.ServerStepStatus != "" {
		state.ServerStepStatus = req.ServerStepStatus
	}
	if req.Engaged && !state.Engaged {
		state.Engaged = true
		t := now
		state.EngagedAt = &t
	}

	if err := s.controller.SaveOnboardingState(state); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("save state: %v", err))
		return
	}

	resp, err := s.computeOnboardingState()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, fmt.Sprintf("recompute state: %v", err))
		return
	}
	s.writeSuccess(w, resp)
}

// computeOnboardingState assembles the response from the connect service,
// the configured-server count, and the persisted engagement record.
func (s *Server) computeOnboardingState() (*OnboardingStateResponse, error) {
	resp := &OnboardingStateResponse{
		ConnectedClientIDs: []string{},
	}

	if svc := s.getConnectService(); svc != nil {
		resp.ConnectedClientCount = svc.GetConnectedCount()
		resp.ConnectedClientIDs = svc.GetConnectedIDs()
		resp.HasConnectedClient = resp.ConnectedClientCount > 0
	}

	servers, err := s.controller.GetAllServers()
	if err == nil {
		resp.ConfiguredServerCount = len(servers)
		resp.HasConfiguredServer = len(servers) > 0
	}

	state, err := s.controller.GetOnboardingState()
	if err != nil {
		return nil, err
	}
	if state == nil {
		state = &storage.OnboardingState{}
	}
	resp.State = *state

	// Spec 046 v2: pull activation state for the Verify tab.
	resp.FirstMCPClientEver, resp.MCPClientsSeenEver = s.controller.GetActivationFirstMCPClient()
	if resp.MCPClientsSeenEver == nil {
		resp.MCPClientsSeenEver = []string{}
	}

	// Badge formula: +1 per incomplete tab.
	if !resp.HasConnectedClient {
		resp.IncompleteTabCount++
	}
	if !resp.HasConfiguredServer {
		resp.IncompleteTabCount++
	}
	if !resp.FirstMCPClientEver {
		resp.IncompleteTabCount++
	}

	resp.ShouldShowWizard = !state.Engaged && resp.IncompleteTabCount > 0

	return resp, nil
}

// validStepStatus returns true if v is an allowed step-status value.
func validStepStatus(v string) bool {
	return v == "" || v == "completed" || v == "skipped"
}
