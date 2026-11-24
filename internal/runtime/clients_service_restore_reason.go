package runtime

import "go.uber.org/zap"

// Reasons a connect rollback does not restore the prior binding.
const (
	restoreSkippedGuard        = "guard"
	restoreSkippedInspectError = "inspect_error"
)

// restoreSkipReason answers whether the rollback of a connect for clientID must
// skip restoring the prior binding: "" restores as before, "guard" when the
// guard now refuses the restored state, "inspect_error" when that could not be
// evaluated. The revocation of the fresh API key never depends on this: an
// inspection failure only costs the optional restore, and is logged (s.mu held).
func (s *ClientsService) restoreSkipReason(clientID string) string {
	refused, err := s.restoreRefusedInner(clientID)
	switch {
	case err != nil:
		s.logger.Warn("connect rollback: cannot evaluate the binding restore; revoking the key without restoring",
			zap.String("client_id", clientID), zap.Error(err))
		return restoreSkippedInspectError
	case refused:
		return restoreSkippedGuard
	}
	return ""
}
