package oauth

import (
	"context"

	"go.uber.org/zap"
)

// TokenSaveResult says what became of a token response that was being stored
// (GH #1827). It is a closed set, safe to log.
type TokenSaveResult string

const (
	// TokenSaveSaved: the token was written.
	TokenSaveSaved TokenSaveResult = "saved"
	// TokenSaveSuperseded: a refresh flight's result was dropped because a
	// newer grant (a sign-in or another flight) was stored meanwhile.
	TokenSaveSuperseded TokenSaveResult = "superseded"
	// TokenSaveInstanceReplaced: the server instance was removed, replaced
	// or reconfigured, so nothing was written.
	TokenSaveInstanceReplaced TokenSaveResult = "instance_replaced"
	// TokenSaveCancelled: the request's context ended (the refresh flight's
	// deadline, say) before the token could be stored.
	TokenSaveCancelled TokenSaveResult = "cancelled"
	// TokenSaveFailed: the storage write failed.
	TokenSaveFailed TokenSaveResult = "failed"
)

// RefreshFlightLogFields returns the flight number and trigger of the refresh
// flight ctx belongs to (flight 0, trigger "none" outside one), so a log line
// can be matched to the "OAuth refresh flight started" line (GH #1827).
func RefreshFlightLogFields(ctx context.Context) []zap.Field {
	info, _ := RefreshFlightInfoFrom(ctx)
	trigger := string(info.Trigger)
	if trigger == "" {
		trigger = "none"
	}
	return []zap.Field{zap.Uint64("flight", info.Flight), zap.String("trigger", trigger)}
}

// LogTokenSave records what became of one token response that was being
// stored (GH #1827): written, dropped as superseded, dropped for a replaced
// instance, or failed. refreshTokenChanged reports that the stored refresh
// token was replaced by a different one, i.e. the provider rotated it. It
// logs no token, code or error text. A nil logger logs nothing.
func LogTokenSave(ctx context.Context, logger *zap.Logger, server string, result TokenSaveResult, refreshTokenChanged bool) {
	if logger == nil {
		return
	}
	fields := append([]zap.Field{zap.String("server", server), zap.String("result", string(result))},
		RefreshFlightLogFields(ctx)...)
	fields = append(fields, zap.Bool("refresh_token_changed", refreshTokenChanged))
	logger.Info("OAuth token save finished", fields...)
}
