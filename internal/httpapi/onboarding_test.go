package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// onboardingTestController extends the routing mock with controllable
// onboarding state and activation evidence for the Spec 080 US1 decision
// matrix tests.
type onboardingTestController struct {
	mockRoutingController

	state *storage.OnboardingState
	saved *storage.OnboardingState

	firstEver bool
	// activationPanicsLeft makes GetActivationFirstMCPClient panic for the
	// next N calls, simulating an evaluation error (FR-002a).
	activationPanicsLeft int
}

func (m *onboardingTestController) GetOnboardingState() (*storage.OnboardingState, error) {
	if m.state == nil {
		return &storage.OnboardingState{}, nil
	}
	cp := *m.state
	return &cp, nil
}

func (m *onboardingTestController) SaveOnboardingState(st *storage.OnboardingState) error {
	m.saved = st
	m.state = st
	return nil
}

func (m *onboardingTestController) GetActivationFirstMCPClient() (bool, []string) {
	if m.activationPanicsLeft > 0 {
		m.activationPanicsLeft--
		panic("simulated activation store failure")
	}
	return m.firstEver, nil
}

func postOnboardingMark(t *testing.T, srv *Server, body OnboardingMarkRequest) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/mark", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func newOnboardingTestServer(t *testing.T, ctrl *onboardingTestController) *Server {
	t.Helper()
	ctrl.apiKey = "test-key"
	ctrl.routingMode = "retrieve_tools"
	return NewServer(ctrl, zap.NewNop().Sugar(), nil)
}

// TestNextConnectStepStatus covers the pure decision matrix for the connect
// step (Spec 080 FR-002/FR-002a/FR-004).
func TestNextConnectStepStatus(t *testing.T) {
	evidence := func() bool { return true }
	noEvidence := func() bool { return false }

	cases := []struct {
		name      string
		current   string
		requested string
		hasEv     func() bool
		want      string
	}{
		// FR-002: untouched step skipped at dismissal, positive evidence.
		{"untouched skip with evidence upgrades", "", "skipped", evidence, "completed_external"},
		// FR-002a: no evidence -> today's behavior.
		{"untouched skip without evidence stays skipped", "", "skipped", noEvidence, "skipped"},
		// FR-004: completed never regresses.
		{"completed not regressed by skip", "completed", "skipped", evidence, "completed"},
		// FR-004: completed_external never regresses to skipped.
		{"completed_external not regressed by skip", "completed_external", "skipped", noEvidence, "completed_external"},
		// In-wizard completion after external is an upgrade, not a regression.
		{"completed_external upgraded by in-wizard completion", "completed_external", "completed", noEvidence, "completed"},
		// FR-003: historical skipped is never rewritten retroactively.
		{"already-skipped not rewritten despite evidence", "skipped", "skipped", evidence, "skipped"},
		// In-wizard completion applies directly; evidence check irrelevant.
		{"untouched completed applies directly", "", "completed", noEvidence, "completed"},
		// Skipped can still be completed later from the Setup entry.
		{"skipped upgraded by later in-wizard completion", "skipped", "completed", noEvidence, "completed"},
		// Empty request preserves current.
		{"empty request preserves current", "completed", "", evidence, "completed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nextConnectStepStatus(tc.current, tc.requested, tc.hasEv)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestMarkOnboarding_SkipUpgradesWhenFirstMCPClientEver asserts the
// handshake-evidence half of FR-002: no client config entries, but an MCP
// client already handshaked (activation.first_mcp_client_ever).
func TestMarkOnboarding_SkipUpgradesWhenFirstMCPClientEver(t *testing.T) {
	ctrl := &onboardingTestController{firstEver: true}
	srv := newOnboardingTestServer(t, ctrl)
	// No connect service wired: evidence must come from activation alone.

	w := postOnboardingMark(t, srv, OnboardingMarkRequest{Engaged: true, ConnectStepStatus: "skipped"})

	assert.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, ctrl.saved)
	assert.Equal(t, storage.StepStatusCompletedExternal, ctrl.saved.ConnectStepStatus)
	assert.True(t, ctrl.saved.Engaged)
}

// TestMarkOnboarding_SkipUpgradesWhenClientConnected asserts the
// connected-client half of FR-002 via a real connect.Service pointed at a
// fake home with one connected client config.
func TestMarkOnboarding_SkipUpgradesWhenClientConnected(t *testing.T) {
	ctrl := &onboardingTestController{firstEver: false}
	srv := newOnboardingTestServer(t, ctrl)

	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	svc := connect.NewServiceWithHome("127.0.0.1:8080", "", home)
	cfgPath := connect.ConfigPath("opencode", home)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o755))
	require.NoError(t, os.WriteFile(cfgPath,
		[]byte(`{"mcp":{"mcpproxy":{"type":"remote","url":"http://127.0.0.1:8080/mcp"}}}`), 0o644))
	require.Positive(t, svc.GetConnectedCount(), "precondition: fake home must count as connected")
	srv.SetConnectService(svc)

	w := postOnboardingMark(t, srv, OnboardingMarkRequest{Engaged: true, ConnectStepStatus: "skipped"})

	assert.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, ctrl.saved)
	assert.Equal(t, storage.StepStatusCompletedExternal, ctrl.saved.ConnectStepStatus)
}

// TestMarkOnboarding_SkipStaysSkippedWithoutEvidence is acceptance scenario 3:
// fresh install, zero connected clients, never handshaked.
func TestMarkOnboarding_SkipStaysSkippedWithoutEvidence(t *testing.T) {
	ctrl := &onboardingTestController{firstEver: false}
	srv := newOnboardingTestServer(t, ctrl)

	w := postOnboardingMark(t, srv, OnboardingMarkRequest{Engaged: true, ConnectStepStatus: "skipped"})

	assert.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, ctrl.saved)
	assert.Equal(t, storage.StepStatusSkipped, ctrl.saved.ConnectStepStatus)
}

// TestMarkOnboarding_EvidenceErrorFallsBackToSkipped asserts FR-002a: an
// evaluation error during the evidence check falls back to "skipped" and
// never blocks dismissal.
func TestMarkOnboarding_EvidenceErrorFallsBackToSkipped(t *testing.T) {
	// Panic exactly once: the evidence check hits it, the post-save
	// recompute (a separate call) succeeds.
	ctrl := &onboardingTestController{firstEver: true, activationPanicsLeft: 1}
	srv := newOnboardingTestServer(t, ctrl)

	w := postOnboardingMark(t, srv, OnboardingMarkRequest{Engaged: true, ConnectStepStatus: "skipped"})

	assert.Equal(t, http.StatusOK, w.Code, "dismissal must not be blocked by an evidence-check failure")
	require.NotNil(t, ctrl.saved)
	assert.Equal(t, storage.StepStatusSkipped, ctrl.saved.ConnectStepStatus)
	assert.True(t, ctrl.saved.Engaged)
}

// TestMarkOnboarding_CompletedStatusesNeverRegress asserts FR-004 at the
// endpoint level: a later skip (e.g. wizard re-dismissed after the user
// disconnected all clients) does not downgrade a recorded completion.
func TestMarkOnboarding_CompletedStatusesNeverRegress(t *testing.T) {
	for _, terminal := range []string{storage.StepStatusCompleted, storage.StepStatusCompletedExternal} {
		t.Run(terminal, func(t *testing.T) {
			ctrl := &onboardingTestController{
				firstEver: false, // no evidence anymore: clients disconnected
				state:     &storage.OnboardingState{Engaged: true, ConnectStepStatus: terminal},
			}
			srv := newOnboardingTestServer(t, ctrl)

			w := postOnboardingMark(t, srv, OnboardingMarkRequest{ConnectStepStatus: "skipped"})

			assert.Equal(t, http.StatusOK, w.Code)
			require.NotNil(t, ctrl.saved)
			assert.Equal(t, terminal, ctrl.saved.ConnectStepStatus)
		})
	}
}

// TestMarkOnboarding_HistoricalSkipNotRewritten asserts FR-003: an
// already-persisted "skipped" is not retroactively upgraded, even with
// evidence present at a later dismissal.
func TestMarkOnboarding_HistoricalSkipNotRewritten(t *testing.T) {
	ctrl := &onboardingTestController{
		firstEver: true,
		state:     &storage.OnboardingState{Engaged: true, ConnectStepStatus: storage.StepStatusSkipped},
	}
	srv := newOnboardingTestServer(t, ctrl)

	w := postOnboardingMark(t, srv, OnboardingMarkRequest{ConnectStepStatus: "skipped"})

	assert.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, ctrl.saved)
	assert.Equal(t, storage.StepStatusSkipped, ctrl.saved.ConnectStepStatus)
}

// TestMarkOnboarding_StatusValidation asserts the request enum: while the
// STORED connect-step enum is widened with "completed_external" (FR-001),
// the request enum is not — only the server-side evidence check may produce
// "completed_external" (edge case: never guess it without positive
// evidence), so a client sending it directly is rejected, as are unknown
// values for both steps.
func TestMarkOnboarding_StatusValidation(t *testing.T) {
	t.Run("connect step rejects completed_external from clients", func(t *testing.T) {
		ctrl := &onboardingTestController{firstEver: true} // even with evidence present
		srv := newOnboardingTestServer(t, ctrl)
		w := postOnboardingMark(t, srv, OnboardingMarkRequest{ConnectStepStatus: "completed_external"})
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Nil(t, ctrl.saved)
	})

	t.Run("server step rejects completed_external", func(t *testing.T) {
		ctrl := &onboardingTestController{}
		srv := newOnboardingTestServer(t, ctrl)
		w := postOnboardingMark(t, srv, OnboardingMarkRequest{ServerStepStatus: "completed_external"})
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Nil(t, ctrl.saved)
	})

	t.Run("connect step rejects unknown value", func(t *testing.T) {
		ctrl := &onboardingTestController{}
		srv := newOnboardingTestServer(t, ctrl)
		w := postOnboardingMark(t, srv, OnboardingMarkRequest{ConnectStepStatus: "bogus"})
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Nil(t, ctrl.saved)
	})
}

// TestMarkOnboarding_InWizardCompletionUnaffected asserts acceptance
// scenario 4 (first half): completing the connect step inside the wizard
// records "completed" regardless of external evidence.
func TestMarkOnboarding_InWizardCompletionUnaffected(t *testing.T) {
	ctrl := &onboardingTestController{firstEver: true}
	srv := newOnboardingTestServer(t, ctrl)

	w := postOnboardingMark(t, srv, OnboardingMarkRequest{ConnectStepStatus: "completed"})

	assert.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, ctrl.saved)
	assert.Equal(t, storage.StepStatusCompleted, ctrl.saved.ConnectStepStatus)
}
