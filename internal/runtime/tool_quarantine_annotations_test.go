package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

func TestCheckToolApprovals_CapturesAndPreservesAnnotationDiff(t *testing.T) {
	rt := setupQuarantineRuntime(t, nil, []*config.ServerConfig{{Name: "github", Enabled: true, Quarantined: true}})
	readOnly := true
	destructive := true
	previous := &config.ToolAnnotations{Title: "List issues", ReadOnlyHint: &readOnly}
	current := &config.ToolAnnotations{Title: "Delete issues", DestructiveHint: &destructive}

	initial := &config.ToolMetadata{
		ServerName:  "github",
		Name:        "manage_issues",
		Description: "Lists issues",
		ParamsJSON:  `{"type":"object"}`,
		Hash:        calculateToolApprovalHash("manage_issues", "Lists issues", `{"type":"object"}`, nil),
		Annotations: previous,
	}
	_, err := rt.checkToolApprovals("github", []*config.ToolMetadata{initial})
	require.NoError(t, err)

	record, err := rt.storageManager.GetToolApproval("github", "manage_issues")
	require.NoError(t, err)
	assert.Equal(t, previous, record.CurrentAnnotations, "first discovery must preserve the upstream annotation snapshot")
	require.NoError(t, rt.ApproveTools("github", []string{"manage_issues"}, "test"))
	record, err = rt.storageManager.GetToolApproval("github", "manage_issues")
	require.NoError(t, err)
	approvedHash := record.ApprovedHash

	updatedMetadata := &config.ToolAnnotations{Title: "Issue manager", ReadOnlyHint: &readOnly}
	annotationOnlyUpdate := *initial
	annotationOnlyUpdate.Annotations = updatedMetadata
	_, err = rt.checkToolApprovals("github", []*config.ToolMetadata{&annotationOnlyUpdate})
	require.NoError(t, err)
	record, err = rt.storageManager.GetToolApproval("github", "manage_issues")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusApproved, record.Status, "annotation-only changes do not trigger a re-review")
	assert.Equal(t, updatedMetadata, record.CurrentAnnotations, "review data tracks the latest annotation snapshot without changing approval state")

	changed := &config.ToolMetadata{
		ServerName:  "github",
		Name:        "manage_issues",
		Description: "Deletes issues",
		ParamsJSON:  `{"type":"object"}`,
		Hash:        calculateToolApprovalHash("manage_issues", "Deletes issues", `{"type":"object"}`, nil),
		Annotations: current,
	}
	_, err = rt.checkToolApprovals("github", []*config.ToolMetadata{changed})
	require.NoError(t, err)

	record, err = rt.storageManager.GetToolApproval("github", "manage_issues")
	require.NoError(t, err)
	assert.Equal(t, storage.ToolApprovalStatusChanged, record.Status)
	assert.Equal(t, approvedHash, record.ApprovedHash, "annotation capture must not change the approval hash")
	assert.Equal(t, updatedMetadata, record.PreviousAnnotations, "review needs the exact pre-change annotation snapshot")
	assert.Equal(t, current, record.CurrentAnnotations, "review needs the current annotation snapshot")
}
