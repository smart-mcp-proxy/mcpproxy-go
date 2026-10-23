package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// feedbackContractFixture is the wire contract between SubmitFeedback and the
// telemetry worker's /v1/feedback handler (repo mcpproxy-telemetry). The worker
// keeps a byte-identical copy at test/fixtures/feedback_contract.json and its
// tests assert every field below is read from this exact nesting. Issue #1724:
// the client nested context under "context" while the worker read top-level
// fields, so every feedback issue ever filed had an empty Context table. Change
// both copies together.
const feedbackContractFixture = "testdata/feedback_contract.json"

func loadFeedbackContract(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(feedbackContractFixture)
	if err != nil {
		t.Fatalf("read %s: %v", feedbackContractFixture, err)
	}
	return data
}

// jsonKeyPaths returns every object key path in a decoded JSON value
// ("context.diagnostics.fix_attempted_24h"). Keys of free-form maps (the MCPX_
// code maps) are reported as "<key>" so the contract pins the shape, not data.
func jsonKeyPaths(prefix string, v any, out map[string]bool) {
	obj, ok := v.(map[string]any)
	if !ok {
		return
	}
	freeForm := strings.HasSuffix(prefix, "error_code_counts_24h") || strings.HasSuffix(prefix, "current_error_codes")
	for k, child := range obj {
		if freeForm {
			k = "<key>"
		}
		p := k
		if prefix != "" {
			p = prefix + "." + k
		}
		out[p] = true
		jsonKeyPaths(p, child, out)
	}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestFeedbackContract_FixtureRoundTrips proves every fixture field maps onto a
// FeedbackRequest field at the same nesting: decoding rejects unknown fields
// and re-encoding reproduces the fixture exactly.
func TestFeedbackContract_FixtureRoundTrips(t *testing.T) {
	data := loadFeedbackContract(t)

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var req FeedbackRequest
	if err := dec.Decode(&req); err != nil {
		t.Fatalf("fixture does not decode into FeedbackRequest: %v", err)
	}

	encoded, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var want, got any
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("FeedbackRequest does not round-trip the contract fixture\nwant: %s\ngot:  %s", data, encoded)
	}
}

// TestFeedbackContract_SubmitSendsOnlyContractFields sends a fully populated
// feedback through SubmitFeedback and asserts the wire body uses only key
// paths the fixture declares — a new field must be added to the fixture (and
// so to the worker) before it ships.
func TestFeedbackContract_SubmitSendsOnlyContractFields(t *testing.T) {
	clearTelemetryEnv(t)
	withFixedMachineID(t, strings.Repeat("b", 64))

	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = readAll(r)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(FeedbackResponse{Success: true})
	}))
	defer server.Close()

	svc := newFeedbackTestService(t, server.URL, true)
	if _, err := svc.SubmitFeedback(context.Background(), &FeedbackRequest{
		Category: "bug",
		Message:  "Something is broken and I want to report it",
		Email:    "test@example.com",
	}); err != nil {
		t.Fatalf("SubmitFeedback: %v", err)
	}

	var fixture, sent any
	if err := json.Unmarshal(loadFeedbackContract(t), &fixture); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decode sent body %q: %v", body, err)
	}
	allowed := map[string]bool{}
	jsonKeyPaths("", fixture, allowed)
	sentKeys := map[string]bool{}
	jsonKeyPaths("", sent, sentKeys)
	for _, k := range sortedKeys(sentKeys) {
		if !allowed[k] {
			t.Errorf("SubmitFeedback sent %q, which the contract fixture does not declare", k)
		}
	}
}

// TestSubmitFeedback_IncludesTelemetryRefWhenEnabled: with telemetry on, the
// feedback carries the same anonymous_id and machine_id the heartbeat reports,
// so a maintainer can join the report to that install's telemetry.
func TestSubmitFeedback_IncludesTelemetryRefWhenEnabled(t *testing.T) {
	clearTelemetryEnv(t)
	machineID := strings.Repeat("c", 64)
	withFixedMachineID(t, machineID)

	var got FeedbackRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(FeedbackResponse{Success: true})
	}))
	defer server.Close()

	svc := newFeedbackTestService(t, server.URL, true)
	if _, err := svc.SubmitFeedback(context.Background(), &FeedbackRequest{
		Category: "bug",
		Message:  "High CPU usage while idle, please help",
	}); err != nil {
		t.Fatalf("SubmitFeedback: %v", err)
	}

	if got.Context.AnonymousID != testFeedbackAnonID {
		t.Errorf("anonymous_id = %q, want %q", got.Context.AnonymousID, testFeedbackAnonID)
	}
	if got.Context.MachineID != machineID {
		t.Errorf("machine_id = %q, want %q", got.Context.MachineID, machineID)
	}
	if got.Context.LastStartupOutcome != "success" {
		t.Errorf("last_startup_outcome = %q, want success", got.Context.LastStartupOutcome)
	}
	if got.Context.PreviousShutdown != PreviousShutdownCrash {
		t.Errorf("previous_shutdown = %q, want %q", got.Context.PreviousShutdown, PreviousShutdownCrash)
	}
	if got.Context.GoVersion == "" {
		t.Error("go_version missing")
	}
	if got.Context.ToolCount != 7 {
		t.Errorf("tool_count = %d, want 7", got.Context.ToolCount)
	}
}

// TestSubmitFeedback_NoTelemetryRefWhenDisabled: a user who turned telemetry
// off has not agreed to have this report linked to an install, so the
// correlation ids and diagnostics are withheld; the basic context still goes.
func TestSubmitFeedback_NoTelemetryRefWhenDisabled(t *testing.T) {
	clearTelemetryEnv(t)
	withFixedMachineID(t, strings.Repeat("d", 64))

	var got FeedbackRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(FeedbackResponse{Success: true})
	}))
	defer server.Close()

	svc := newFeedbackTestService(t, server.URL, false)
	if _, err := svc.SubmitFeedback(context.Background(), &FeedbackRequest{
		Category: "bug",
		Message:  "High CPU usage while idle, please help",
	}); err != nil {
		t.Fatalf("SubmitFeedback: %v", err)
	}

	if got.Context.AnonymousID != "" || got.Context.MachineID != "" {
		t.Errorf("telemetry disabled but ids sent: anonymous_id=%q machine_id=%q", got.Context.AnonymousID, got.Context.MachineID)
	}
	if got.Context.LastStartupOutcome != "" || got.Context.PreviousShutdown != "" || got.Context.Diagnostics != nil {
		t.Errorf("telemetry disabled but diagnostics sent: %+v", got.Context)
	}
	if got.Context.Version != "v1.0.0" || got.Context.ServerCount != 3 {
		t.Errorf("basic context missing: %+v", got.Context)
	}
}

const testFeedbackAnonID = "550e8400-e29b-41d4-a716-446655440000"

func newFeedbackTestService(t *testing.T, endpoint string, telemetryEnabled bool) *Service {
	t.Helper()
	enabled := telemetryEnabled
	cfg := &config.Config{
		Telemetry: &config.TelemetryConfig{
			Enabled:            &enabled,
			Endpoint:           endpoint,
			AnonymousID:        testFeedbackAnonID,
			LastStartupOutcome: "success",
		},
	}
	svc := New(cfg, "", "v1.0.0", "personal", zap.NewNop())
	svc.SetRuntimeStats(&mockRuntimeStats{
		serverCount:    3,
		connectedCount: 2,
		toolCount:      7,
		routingMode:    "retrieve_tools",
	})
	svc.SetPreChurn(PreviousShutdownCrash, nil, nil)
	return svc
}

func withFixedMachineID(t *testing.T, id string) {
	t.Helper()
	prev := machineIDProvider
	machineIDProvider = func() string { return id }
	resetMachineIDForTest()
	t.Cleanup(func() {
		machineIDProvider = prev
		resetMachineIDForTest()
	})
}

func readAll(r *http.Request) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}
