package runtime

import (
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// rotateFenced runs storage.RotateOAuthRefresh inside the config fence: it takes
// the same ConfigCommitLock as the exchange (the lock every config publication
// holds, including the one that switches browser login off), reads ONE config
// snapshot, re-checks MCPOAuthEnabled on it and holds the lock through the
// rotation. A refresh that passed the early Enabled() check therefore cannot
// rotate secrets or extend an expiry after a commit disabled the feature: the
// commit either completed first (uniform refusal, nothing written) or starts
// after the rotation. Refresh does not take bindingWriteMu, so the order is
// configCommitMu -> storage. Key I/O and token generation happened before; the
// replay row, event and notifier run after the lock is released (by the caller).
func (s *OAuthLoginService) rotateFenced(grantID, presentedHash string, ts *tokenSet) (storage.RefreshOutcome, storage.OAuthGrant, *TokenError) {
	if h := s.testHookBeforeRotateLock; h != nil {
		h()
	}
	if s.commitLock != nil {
		s.commitLock.Lock()
		defer s.commitLock.Unlock()
	}
	cfg := s.cfg()
	if cfg == nil || !cfg.MCPOAuthEnabled {
		return storage.RefreshInvalid, storage.OAuthGrant{}, errInvalidGrant
	}
	if h := s.testHookInRotate; h != nil {
		h()
	}
	now := s.now().UTC()
	outcome, after, err := s.store.RotateOAuthRefresh(grantID, presentedHash,
		ts.refreshHash, ts.refreshPrefix, ts.accessHash, now, OAuthAccessTokenTTL, OAuthRefreshTokenTTL)
	if err != nil {
		s.logger.Error("browser login: refresh rotation failed", zap.Error(err))
		return storage.RefreshInvalid, storage.OAuthGrant{}, errTokenServer
	}
	return outcome, after, nil
}
