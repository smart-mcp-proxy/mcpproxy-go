package managed

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills"
)

// SupportsSkills reports whether the connected upstream announced the skills
// extension (and the resources capability it requires).
func (mc *Client) SupportsSkills() bool {
	return mc.coreClient != nil && mc.IsConnected() && mc.coreClient.SupportsSkills()
}

// ListSkillsCaptured pages the upstream's skills/list through the session
// recovery path. The capture is stamped before the request goes out, as for
// tools, so the runtime can drop a result that a reconnect overtook. A
// *skills.TruncatedError comes back together with the partial entries.
func (mc *Client) ListSkillsCaptured(ctx context.Context) ([]skills.Entry, []skills.EntryError, InventoryCapture, error) {
	if mc.retired.Load() {
		return nil, nil, InventoryCapture{}, ErrInstanceRetired
	}
	if !mc.IsConnected() {
		return nil, nil, InventoryCapture{}, fmt.Errorf("client not connected (state: %s)", mc.StateManager.GetState().String())
	}

	capture := mc.newCapture()
	var entries []skills.Entry
	var entryErrs []skills.EntryError
	err := mc.withSession(ctx, "skills/list", func() (e error) {
		entries, entryErrs, e = mc.coreClient.ListSkills(ctx)
		return e
	})
	if err != nil {
		var tr *skills.TruncatedError
		if !errors.As(err, &tr) {
			mc.logger.Debug("Failed to list skills",
				zap.String("server", mc.GetConfig().Name),
				zap.Error(err))
			return nil, nil, capture, err
		}
	}
	return entries, entryErrs, capture, err
}

// ReadSkillFile reads one listed skill file through the session recovery path.
// The caller verifies the digest.
func (mc *Client) ReadSkillFile(ctx context.Context, ref skills.FileRef) ([]byte, bool, string, error) {
	if mc.retired.Load() {
		return nil, false, "", ErrInstanceRetired
	}
	if !mc.IsConnected() {
		return nil, false, "", fmt.Errorf("client not connected (state: %s)", mc.StateManager.GetState().String())
	}
	var (
		content  []byte
		isText   bool
		mimeType string
	)
	err := mc.withSession(ctx, "resources/read", func() (e error) {
		content, isText, mimeType, e = mc.coreClient.ReadSkillFile(ctx, ref)
		return e
	})
	if err != nil {
		return nil, false, "", err
	}
	return content, isText, mimeType, nil
}
