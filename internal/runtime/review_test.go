package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func TestReviewFixtureContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "review_fixture.json"))
	require.NoError(t, err)
	var fixture struct {
		Server struct {
			Quarantined         bool `json:"quarantined"`
			DefinitionsCaptured bool `json:"definitions_captured"`
		} `json:"server"`
		Tools []struct {
			Name           string                  `json:"name"`
			Description    string                  `json:"description"`
			Tier           contracts.Tier          `json:"tier"`
			Annotations    *config.ToolAnnotations `json:"annotations"`
			ApprovalStatus string                  `json:"approval_status"`
			ScanVerdict    string                  `json:"scan_verdict"`
			Previous       json.RawMessage         `json:"previous"`
		} `json:"tools"`
		Expected struct {
			QueueCount int                    `json:"queue_count"`
			ToolCount  int                    `json:"tool_count"`
			TierCounts map[contracts.Tier]int `json:"tier_counts"`
		} `json:"expected"`
	}
	require.NoError(t, json.Unmarshal(data, &fixture))
	require.True(t, fixture.Server.Quarantined)
	require.True(t, fixture.Server.DefinitionsCaptured)
	require.Len(t, fixture.Tools, fixture.Expected.ToolCount)
	counts := map[contracts.Tier]int{}
	var malicious, changed bool
	for _, tool := range fixture.Tools {
		counts[tool.Tier]++
		if tool.Tier != contracts.TierUnknown {
			require.Equal(t, tool.Tier, contracts.AnnotationTier(tool.Annotations), tool.Name)
		}
		malicious = malicious || tool.Name == "malicious_0" && tool.Description == "<img src=x onerror=alert(1)> [click me](https://attacker.example)"
		changed = changed || tool.Name == "changed_0" && len(tool.Previous) > 0
	}
	require.Equal(t, fixture.Expected.TierCounts, counts)
	require.True(t, malicious)
	require.True(t, changed)

	// The fixture is the shared contract used by the review UI.  Populate the
	// same stored records the runtime sees and compare every contract field,
	// rather than merely checking that the fixture is internally consistent.
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{
		Name: "filesystem", Enabled: true, Quarantined: true,
	}})
	for _, want := range fixture.Tools {
		record := &storage.ToolApprovalRecord{
			ServerName: "filesystem", ToolName: want.Name,
			Status: want.ApprovalStatus, CurrentDescription: want.Description,
			CurrentAnnotations: want.Annotations, HeldVerdict: want.ScanVerdict,
		}
		if len(want.Previous) > 0 {
			var previous struct {
				Description string `json:"description"`
			}
			require.NoError(t, json.Unmarshal(want.Previous, &previous), want.Name)
			record.PreviousDescription = previous.Description
		}
		require.NoError(t, rt.storageManager.SaveToolApproval(record), want.Name)
	}

	queue, err := rt.GetReviewQueue(context.Background())
	require.NoError(t, err)
	require.Equal(t, fixture.Expected.QueueCount, queue.Count)
	require.Len(t, queue.Servers, fixture.Expected.QueueCount)
	require.Equal(t, fixture.Expected.ToolCount, queue.Servers[0].ToolsCaptured)
	require.Equal(t, fixture.Expected.TierCounts, queue.Servers[0].TierCounts)

	review, err := rt.GetServerReview(context.Background(), "filesystem")
	require.NoError(t, err)
	require.True(t, review.Server.DefinitionsCaptured)
	require.Len(t, review.Tools, fixture.Expected.ToolCount)
	gotByName := make(map[string]ReviewTool, len(review.Tools))
	for _, got := range review.Tools {
		gotByName[got.Name] = got
	}
	for _, want := range fixture.Tools {
		got, ok := gotByName[want.Name]
		require.True(t, ok, want.Name)
		require.Equal(t, want.Description, got.Description, want.Name)
		require.Equal(t, want.Tier, got.Tier, want.Name)
		require.Equal(t, want.Annotations, got.Annotations, want.Name)
		require.Equal(t, want.ScanVerdict, got.ScanVerdict, want.Name)
		if len(want.Previous) == 0 {
			require.Nil(t, got.Previous, want.Name)
			require.Nil(t, got.Diff, want.Name)
			continue
		}
		require.NotNil(t, got.Previous, want.Name)
		require.NotNil(t, got.Diff, want.Name)
		var previous struct {
			Description string `json:"description"`
		}
		require.NoError(t, json.Unmarshal(want.Previous, &previous), want.Name)
		require.Equal(t, previous.Description, got.Previous.Description, want.Name)
		require.Equal(t, "@@ -1 +1 @@\n-"+previous.Description+"\n+"+want.Description, got.Diff.Description, want.Name)
	}
}

func TestReviewPayload_ClassifiesCapturedAndLegacyAnnotations(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "github", Enabled: true, Quarantined: true}})
	readOnly := true
	tools := []*config.ToolMetadata{
		{Name: "safe", Description: "Read data", ParamsJSON: `{"type":"object"}`, Annotations: &config.ToolAnnotations{ReadOnlyHint: &readOnly}},
		{Name: "plain", Description: "No hints", ParamsJSON: `{"type":"object"}`, Annotations: &config.ToolAnnotations{}},
	}
	for _, tool := range tools {
		tool.ServerName = "github"
		tool.Hash = calculateToolApprovalHash(tool.Name, tool.Description, tool.ParamsJSON, nil)
	}
	_, err := rt.checkToolApprovals("github", tools)
	require.NoError(t, err)

	review, err := rt.GetServerReview(context.Background(), "github")
	require.NoError(t, err)
	require.True(t, review.Server.DefinitionsCaptured)
	// A nil annotation value is only a historical persisted record. Fresh
	// discovery without hints is captured as an empty record and is unannotated.
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "legacy", CurrentHash: "legacy-hash",
		Status: storage.ToolApprovalStatusPending, CurrentDescription: "No stored metadata",
		CurrentSchema: `{"type":"object"}`,
	}))
	review, err = rt.GetServerReview(context.Background(), "github")
	require.NoError(t, err)
	require.Len(t, review.Tools, 3)
	byName := make(map[string]ReviewTool, len(review.Tools))
	for _, tool := range review.Tools {
		byName[tool.Name] = tool
	}
	require.Equal(t, contracts.TierRead, byName["safe"].Tier)
	require.Equal(t, contracts.TierUnannotated, byName["plain"].Tier)
	require.Equal(t, contracts.TierUnknown, byName["legacy"].Tier)
	require.Equal(t, "not_scanned", byName["safe"].ScanVerdict)

	approved, err := rt.storageManager.GetToolApproval("github", "safe")
	require.NoError(t, err)
	approved.Status = storage.ToolApprovalStatusChanged
	approved.PreviousDescription = "Old description"
	approved.PreviousAnnotations = &config.ToolAnnotations{Title: "Old title"}
	require.NoError(t, rt.storageManager.SaveToolApproval(approved))
	review, err = rt.GetServerReview(context.Background(), "github")
	require.NoError(t, err)
	byName = make(map[string]ReviewTool, len(review.Tools))
	for _, tool := range review.Tools {
		byName[tool.Name] = tool
	}
	require.NotNil(t, byName["safe"].Previous)
	require.Contains(t, byName["safe"].Diff.Description, "Old description")
	require.Contains(t, byName["safe"].Diff.Annotations, "Old title")
}

func TestReviewPayload_QueueAndUncapturedDefinitions(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{
		{Name: "filesystem", Enabled: true, Quarantined: true},
		{Name: "github", Enabled: true, Quarantined: false},
	})
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "github", ToolName: "pending", Status: storage.ToolApprovalStatusPending,
		ApprovedAt: time.Now().UTC(), CurrentDescription: "Read data",
	}))

	queue, err := rt.GetReviewQueue(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, queue.Count)
	require.Equal(t, "server_review", queue.Servers[0].Kind)
	require.Equal(t, "tool_review", queue.Servers[1].Kind)

	review, err := rt.GetServerReview(context.Background(), "filesystem")
	require.NoError(t, err)
	require.False(t, review.Server.DefinitionsCaptured)
	require.Empty(t, review.Tools)
}

// UX-04: a disabled quarantined server stays in the queue (it is still owed a
// review) but is labelled so operators can tell it from an active blocker.
func TestReviewQueue_ReportsServerEnabledState(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{
		{Name: "live", Enabled: true, Quarantined: true},
		{Name: "parked", Enabled: false, Quarantined: true},
	})
	queue, err := rt.GetReviewQueue(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, queue.Count)
	byName := map[string]ReviewQueueRow{}
	for _, row := range queue.Servers {
		byName[row.Server] = row
	}
	require.True(t, byName["live"].Enabled)
	require.False(t, byName["parked"].Enabled)
	raw, err := json.Marshal(byName["parked"])
	require.NoError(t, err)
	require.Contains(t, string(raw), `"enabled":false`)
}

// UX-04: "Oldest first" must order by when the review was owed, not by the
// previous approval. A changed tool keeps its old ApprovedAt, so since comes
// from DefinitionChangedAt; a changed tool with no change stamp reports none.
func TestReviewQueue_SinceIgnoresPriorApprovalOfChangedTool(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{
		{Name: "stamped", Enabled: true},
		{Name: "unstamped", Enabled: true},
	})
	lastYear := time.Now().UTC().AddDate(-1, 0, 0)
	changedAt := time.Now().UTC().Add(-time.Hour)
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "stamped", ToolName: "t", Status: storage.ToolApprovalStatusChanged,
		ApprovedAt: lastYear, DefinitionChangedAt: changedAt,
	}))
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "unstamped", ToolName: "t", Status: storage.ToolApprovalStatusChanged,
		ApprovedAt: lastYear,
	}))
	queue, err := rt.GetReviewQueue(context.Background())
	require.NoError(t, err)
	byName := map[string]ReviewQueueRow{}
	for _, row := range queue.Servers {
		byName[row.Server] = row
	}
	require.NotNil(t, byName["stamped"].Since)
	require.True(t, byName["stamped"].Since.After(lastYear.AddDate(0, 6, 0)), "since must not be the old approval time: %v", byName["stamped"].Since)
	require.Nil(t, byName["unstamped"].Since)
}

func TestReviewPayload_RedactsServerSecretsUnconditionally(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{
		Name: "private", Enabled: true, Quarantined: true,
		Protocol: "stdio", Command: "client --token secret123", Args: []string{"--token", "secret123"},
		URL:     "https://example.test/mcp?api_key=secret123",
		Env:     map[string]string{"TOKEN": "secret123"},
		Headers: map[string]string{"Authorization": "Bearer secret123"},
	}})

	review, err := rt.GetServerReview(context.Background(), "private")
	require.NoError(t, err)
	encoded, err := json.Marshal(review)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "secret123")
	require.NotContains(t, string(encoded), "0001-01-01T00:00:00Z")
	require.NotEqual(t, "client --token secret123", review.Server.Command)
}

func TestReviewToolScanVerdict_UsesToolFindingsAndHeldFallback(t *testing.T) {
	record := &storage.ToolApprovalRecord{ToolName: "delete", HeldVerdict: "dangerous"}
	require.Equal(t, "warnings", reviewToolScanVerdict([]scanner.ScanFinding{{
		Location: "github:delete", ThreatLevel: scanner.ThreatLevelWarning,
	}}, "github", record, true))
	require.Equal(t, "dangerous", reviewToolScanVerdict([]scanner.ScanFinding{{
		Location: "tool:delete", ThreatLevel: scanner.ThreatLevelDangerous,
	}}, "github", record, true))
	require.Equal(t, "dangerous", reviewToolScanVerdict([]scanner.ScanFinding{{
		Location: "README.md", ThreatLevel: scanner.ThreatLevelDangerous,
	}}, "github", record, true), "non-tool scan findings must not be attributed to a tool")
	plain := &storage.ToolApprovalRecord{ToolName: "delete"}
	require.Equal(t, "not_scanned", reviewToolScanVerdict(nil, "github", plain, false))
	require.Equal(t, "clean", reviewToolScanVerdict(nil, "github", plain, true), "a covering scan with no finding for the tool is clean")
	require.Equal(t, "not_scanned", reviewToolScanVerdict([]scanner.ScanFinding{{
		Location: "tool:delete", ThreatLevel: scanner.ThreatLevelDangerous,
	}}, "github", plain, false), "findings of a scan that did not cover the current definition are not applied")
}

func TestReviewUnifiedDiffUsesReadableSingleLineHunk(t *testing.T) {
	require.Equal(t, "@@ -1 +1 @@\n-old\n+new", reviewUnifiedDiff("old", "new"))
	require.Empty(t, reviewUnifiedDiff("same", "same"))
}

// UX-04: an ordinary pending review (filed by discovery, no ApprovedAt) is
// dated by when it began waiting, so it sorts ahead of a later changed-tool
// review; a record from before the stamp existed stays undated.
func TestReviewQueue_PendingSinceFollowsWhenOwed(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{
		{Name: "old-pending", Enabled: true},
		{Name: "new-change", Enabled: true},
	})
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "old-pending", ToolName: "t", Status: storage.ToolApprovalStatusPending,
	}))
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "new-change", ToolName: "t", Status: storage.ToolApprovalStatusChanged,
		ApprovedAt: time.Now().UTC().AddDate(-1, 0, 0), DefinitionChangedAt: time.Now().UTC(),
	}))
	// Re-saving the still-pending record must not move its date.
	first := reviewOwedSinceFor(t, rt, "old-pending")
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, rt.storageManager.SaveToolApproval(&storage.ToolApprovalRecord{
		ServerName: "old-pending", ToolName: "t", Status: storage.ToolApprovalStatusPending,
	}))
	require.True(t, first.Equal(reviewOwedSinceFor(t, rt, "old-pending")))

	queue, err := rt.GetReviewQueue(context.Background())
	require.NoError(t, err)
	byName := map[string]ReviewQueueRow{}
	for _, row := range queue.Servers {
		byName[row.Server] = row
	}
	require.NotNil(t, byName["old-pending"].Since)
	require.NotNil(t, byName["new-change"].Since)
	require.True(t, byName["old-pending"].Since.Before(*byName["new-change"].Since))

	require.True(t, reviewOwedSince(&storage.ToolApprovalRecord{Status: storage.ToolApprovalStatusPending, ApprovedAt: time.Now()}).IsZero(),
		"a legacy pending record with unknown age stays undated")
}

func reviewOwedSinceFor(t *testing.T, rt *Runtime, server string) time.Time {
	t.Helper()
	rec, err := rt.storageManager.GetToolApproval(server, "t")
	require.NoError(t, err)
	return rec.PendingSince
}
