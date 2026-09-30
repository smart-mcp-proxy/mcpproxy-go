package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// configFunnelController is the optional controller capability behind every
// config-writing REST path (Spec 108-f F2): the read of the desired config, the
// caller's mutation, the FR-008a guard and the write all run under one lock,
// and the write's profile_change records are attributed to the caller. It is
// kept separate from ServerController (like viewAsController) so controllers
// that do not write configuration need no stub; those fall back to the
// controller's own read and ApplyConfig.
type configFunnelController interface {
	MutateConfig(
		ctx context.Context,
		actor internalRuntime.Actor,
		mutate func(desired *config.Config) (internalRuntime.ChangeHint, error),
		tokens internalRuntime.TokenRewrite,
	) (*internalRuntime.ConfigApplyResult, *internalRuntime.ConfigDiff, error)
}

// configMutationRefusal is what a mutate callback returns to make the handler
// answer a specific HTTP response: either a plain status and message, or an
// apply-style error carrying structured validation errors.
type configMutationRefusal struct {
	status int
	msg    string
	// result and applyMsg, when set, route through writeApplyConfigError.
	result   *internalRuntime.ConfigApplyResult
	applyMsg string
	err      error
}

func (e *configMutationRefusal) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return e.msg
}

// mutateConfig runs mutate over the desired configuration and applies the
// result, answering every failure itself. It returns (result, true) on
// success and (nil, false) after writing the response.
func (s *Server) mutateConfig(
	w http.ResponseWriter, r *http.Request,
	applyFailure string,
	mutate func(desired *config.Config) error,
) (*internalRuntime.ConfigApplyResult, bool) {
	if fc, ok := s.controller.(configFunnelController); ok {
		result, _, err := fc.MutateConfig(r.Context(), actorFromRequest(r),
			func(desired *config.Config) (internalRuntime.ChangeHint, error) {
				return internalRuntime.ChangeHint{}, mutate(desired)
			}, internalRuntime.TokenRewrite{})
		if err != nil {
			s.writeConfigMutationError(w, r, applyFailure, result, err)
			return nil, false
		}
		return result, true
	}

	cfg, err := s.desiredConfigForPatch()
	if err != nil {
		s.logger.Errorw("Failed to read configuration for a config write", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to read configuration")
		return nil, false
	}
	if cfg == nil {
		s.writeError(w, r, http.StatusInternalServerError, "Configuration not available")
		return nil, false
	}
	// A controller may hand out its live pointer: mutate a shallow copy so the
	// running config is never edited in place (the funnel gets a deep copy).
	edited := *cfg
	if err := mutate(&edited); err != nil {
		s.writeConfigMutationError(w, r, applyFailure, nil, err)
		return nil, false
	}
	result, err := s.controller.ApplyConfig(&edited, s.controller.GetConfigPath())
	if err != nil {
		s.writeApplyConfigError(w, r, applyFailure, result, err)
		return nil, false
	}
	return result, true
}

// writeConfigMutationError maps a MutateConfig (or mutate callback) failure to
// its response.
func (s *Server) writeConfigMutationError(w http.ResponseWriter, r *http.Request, applyFailure string, result *internalRuntime.ConfigApplyResult, err error) {
	var refusal *configMutationRefusal
	if errors.As(err, &refusal) {
		if refusal.result != nil {
			s.writeApplyConfigError(w, r, refusal.applyMsg, refusal.result, refusal.err)
			return
		}
		s.writeError(w, r, refusal.status, refusal.msg)
		return
	}
	var val *internalRuntime.ValidationError
	if errors.As(err, &val) {
		s.writeClientBindingError(w, r, http.StatusBadRequest, "", val.Field, val.Message)
		return
	}
	if errors.Is(err, internalRuntime.ErrConfigUnavailable) {
		s.logger.Errorw("Failed to read configuration for a config write", "error", err)
		s.writeError(w, r, http.StatusInternalServerError, "Failed to read configuration")
		return
	}
	s.writeApplyConfigError(w, r, applyFailure, result, err)
}
