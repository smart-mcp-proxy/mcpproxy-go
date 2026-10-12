package runtime

import (
	"context"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Test-only discovery entry points. Since Spec 116 PR3 every production
// discovery write carries the commit generation of the instance it listed
// (there is no generation-0 exemption any more); these fixtures bind to the
// server's CURRENT committed generation, as a pass from the current instance
// would. A database no commit has written yet is not consulted (generation 0).

// committedGenForTest is serverName's stored generation, or 0 when storage
// has no committed row for it.
func (r *Runtime) committedGenForTest(serverName string) int64 {
	if r.storageManager == nil {
		return 0
	}
	if committed, err := r.storageManager.ConfigCommitted(); err != nil || !committed {
		return 0
	}
	row, err := r.storageManager.GetStoredServer(serverName)
	if err != nil || row == nil {
		return 0
	}
	return row.Stamp.Gen
}

// applyDifferentialToolUpdate performs the differential index update of a
// server's tools as a discovery pass of its current instance would.
func (r *Runtime) applyDifferentialToolUpdate(ctx context.Context, serverName string, newTools []*config.ToolMetadata) error {
	return r.applyDifferentialToolUpdateAt(ctx, serverName, newTools, r.committedGenForTest(serverName))
}

func (r *Runtime) persistQuarantinedToolDefinitions(serverName string, tools []*config.ToolMetadata) error {
	_, err := r.persistQuarantinedToolDefinitionsAt(serverName, tools, r.committedGenForTest(serverName))
	return err
}

func (r *Runtime) captureQuarantinedToolDefinitionsFromTools(serverName string, tools []*config.ToolMetadata) error {
	if err := r.persistQuarantinedToolDefinitions(serverName, tools); err != nil {
		return err
	}
	r.emitReviewChanged(serverName)
	return nil
}
