package runtime

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// ErrConfigUnavailable is returned by MutateConfig when the desired
// configuration cannot be read; REST answers 500.
var ErrConfigUnavailable = errors.New("configuration unavailable")

// TokenRewrite asks MutateConfig to move every stored token pinned to From onto
// To as part of the same write (a profile rename or delete-with-reassign,
// D19). The zero value rewrites nothing.
type TokenRewrite struct {
	From string
	To   string
}

func (t TokenRewrite) active() bool { return t.From != "" && t.To != "" }

// ProfilePinStore is the token-store surface a pin rewrite needs;
// *storage.Manager implements it.
type ProfilePinStore interface {
	RepinProfile(from, to string) (before []auth.AgentToken, err error)
	RestorePins(before []auth.AgentToken) error
}

// pinStore returns the store a TokenRewrite goes through.
func (r *Runtime) pinStore() ProfilePinStore {
	if r.pinStoreOverride != nil {
		return r.pinStoreOverride
	}
	if sm := r.StorageManager(); sm != nil {
		return sm
	}
	return nil
}

// MutateConfig is THE funnel for every write of the desired configuration that
// can move a profile, a binding or an anonymous_profile (Spec 108-f F2): the
// PATCH /config, POST /config/apply and PATCH /config/docker-isolation routes,
// and every profiles-service mutation. Under bindingWriteMu it
//
//  1. reads the desired configuration (a private copy),
//  2. lets mutate edit that copy (the read-merge of a PATCH lives INSIDE the
//     lock, so a concurrent profile write is never lost),
//  3. runs the FR-008a guard over the whole candidate: the mutated config plus
//     the client credentials as they would be after the token rewrite,
//  4. rewrites token pins in one storage transaction (only when asked),
//  5. applies the config, restoring the pins if that fails, and
//  6. writes the `profile_change` records the write amounts to (FR-030).
//
// The ordering never widens (F3): tokens move first, so a crash between (4)
// and (5) leaves a pin naming a profile the running config lacks (deny-all) or
// already has (the requested target), never the old wider scope.
//
// A refused guard, a failed mutate or a failed apply writes no record.
func (r *Runtime) MutateConfig(
	ctx context.Context,
	actor Actor,
	mutate func(desired *config.Config) (ChangeHint, error),
	tokens TokenRewrite,
) (*ConfigApplyResult, *ConfigDiff, error) {
	unlock := r.LockBindingWrites()
	defer unlock()

	before, err := r.GetDesiredConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrConfigUnavailable, err)
	}
	desired, err := r.GetDesiredConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrConfigUnavailable, err)
	}
	hint, err := mutate(desired)
	if err != nil {
		return nil, nil, err
	}
	if err := refuseUnknownAnonymousProfile(before, desired); err != nil {
		return nil, nil, err
	}

	current, err := r.clientCredentialSnapshot()
	if err != nil {
		return nil, nil, fmt.Errorf("cannot inspect client bindings: %w", err)
	}
	candidateTokens := current
	if tokens.active() {
		candidateTokens = rewriteClientPins(current, tokens.From, tokens.To)
	}
	if err := CheckBindingGuard(r.BindingGuard(),
		GuardState{Config: r.Config(), Tokens: current},
		GuardState{Config: desired, Tokens: candidateTokens}); err != nil {
		return nil, nil, err
	}

	var moved []auth.AgentToken
	if tokens.active() {
		// A pin rewrite is only worth doing for a config that will apply:
		// validate first so a bad edit never moves a token.
		if errs := desired.ValidateDetailed(); len(errs) > 0 {
			return &ConfigApplyResult{Success: false, ValidationErrors: errs},
				nil, fmt.Errorf("configuration validation failed: %v", errs[0].Error())
		}
		store := r.pinStore()
		if store == nil {
			return nil, nil, fmt.Errorf("token store unavailable")
		}
		moved, err = store.RepinProfile(tokens.From, tokens.To)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot move token pins: %w", err)
		}
	}

	result, applyErr := r.ApplyConfig(desired, r.funnelConfigPath())
	if applyErr != nil {
		if len(moved) > 0 {
			if restoreErr := r.pinStore().RestorePins(moved); restoreErr != nil {
				// The pins now name a profile the running config lacks (rename)
				// or the reassign target: deny-all or the requested target, never
				// wider. Report both failures.
				r.logger.Error("profile write failed and its token pins could not be restored",
					zap.String("from", tokens.From), zap.String("to", tokens.To), zap.Error(restoreErr))
				return result, nil, fmt.Errorf("%w (and the token pins could not be restored: %v)", applyErr, restoreErr)
			}
		}
		return result, nil, applyErr
	}

	diff := &ConfigDiff{Changes: diffProfiles(before, desired, hint)}
	r.writeProfileChanges(ctx, actor, diff.Changes)
	// The per-profile search indexes of a created, renamed or re-scoped profile
	// are reconciled by the apply itself (applyConfigLocked, #1458).
	return result, diff, nil
}

// funnelConfigPath is the file a funnel write saves to: the runtime's config
// path, else the data dir's default (the path Server.GetConfigPath reports).
func (r *Runtime) funnelConfigPath() string {
	if path := r.ConfigPath(); path != "" {
		return path
	}
	if cfg := r.Config(); cfg != nil {
		return config.GetConfigPath(cfg.DataDir)
	}
	return ""
}

// refuseUnknownAnonymousProfile is FR-030's API-side rule: an API write that
// CHANGES anonymous_profile to a name no profile has is a 400 (an operator
// must never silently get deny-all anonymous). An unchanged dangling value,
// and a hand edit (the config loader), stay warnings.
func refuseUnknownAnonymousProfile(before, after *config.Config) error {
	name := after.AnonymousProfile
	if name == "" || name == before.AnonymousProfile {
		return nil
	}
	for i := range after.Profiles {
		if after.Profiles[i].Name == name {
			return nil
		}
	}
	return &ValidationError{Field: "anonymous_profile", Message: fmt.Sprintf("unknown profile %q", name)}
}

// rewriteClientPins returns tokens with every pin naming from moved to to.
func rewriteClientPins(tokens []auth.AgentToken, from, to string) []auth.AgentToken {
	out := make([]auth.AgentToken, len(tokens))
	copy(out, tokens)
	for i := range out {
		if out[i].ProfilePin == from {
			out[i].ProfilePin = to
		}
	}
	return out
}

// GuardedApplyConfig applies newCfg after the FR-008a guard. It is MutateConfig
// with `replace the desired config with newCfg` as the mutation and no
// attributed actor; callers that know who is writing use MutateConfig.
func (r *Runtime) GuardedApplyConfig(newCfg *config.Config, cfgPath string) (*ConfigApplyResult, error) {
	unlock := r.LockBindingWrites()
	defer unlock()

	tokens, err := r.clientCredentialSnapshot()
	if err != nil {
		return nil, fmt.Errorf("cannot inspect client bindings: %w", err)
	}
	current := GuardState{Config: r.Config(), Tokens: tokens}
	candidate := GuardState{Config: newCfg, Tokens: tokens}
	if err := CheckBindingGuard(r.BindingGuard(), current, candidate); err != nil {
		return nil, err
	}
	return r.ApplyConfig(newCfg, cfgPath)
}
