// Package httpapi — diagnostics fix endpoint (spec 044).
//
// POST /api/v1/diagnostics/fix runs a registered fixer for a (server, code)
// tuple. Destructive fixes default to dry_run; the caller must explicitly
// send mode=execute to mutate state.
//
// Rate-limited to 1 request per second per (server, code) tuple; exceeding
// the limit returns 429 with Retry-After.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
)

type fixRequestBody struct {
	Server   string `json:"server"`
	Code     string `json:"code"`
	FixerKey string `json:"fixer_key"`
	Mode     string `json:"mode,omitempty"` // "dry_run" | "execute"
}

// fixRateLimiter enforces 1/s per (server, code). In-memory only; resets on
// restart. Spec 044 FR-008.
type fixRateLimiter struct {
	mu   sync.Mutex
	last map[string]time.Time
}

var globalFixLimiter = &fixRateLimiter{last: map[string]time.Time{}}

// allow returns (true, 0) when a request may proceed; otherwise (false, retryAfterSeconds).
func (l *fixRateLimiter) allow(key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if t, ok := l.last[key]; ok {
		elapsed := now.Sub(t)
		if elapsed < time.Second {
			retry := int((time.Second - elapsed) / time.Second)
			if retry < 1 {
				retry = 1
			}
			return false, retry
		}
	}
	l.last[key] = now
	return true, 0
}

// handleInvokeFix implements POST /api/v1/diagnostics/fix.
func (s *Server) handleInvokeFix(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var body fixRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "Malformed JSON body")
		return
	}
	if body.Server == "" || body.Code == "" || body.FixerKey == "" {
		s.writeError(w, r, http.StatusBadRequest, "server, code, and fixer_key are required")
		return
	}

	// Validate code is registered.
	entry, ok := diagnostics.Get(diagnostics.Code(body.Code))
	if !ok {
		s.writeError(w, r, http.StatusBadRequest, "Unknown error code: "+body.Code)
		return
	}

	// Find the requested fix_step to determine destructive flag.
	var step *diagnostics.FixStep
	for i := range entry.FixSteps {
		if entry.FixSteps[i].FixerKey == body.FixerKey {
			step = &entry.FixSteps[i]
			break
		}
	}
	if step == nil || step.Type != diagnostics.FixStepButton {
		s.writeError(w, r, http.StatusBadRequest, "fixer_key does not correspond to a Button fix_step for this code")
		return
	}

	// Destructive fixes require an explicit mode (dry_run or execute). An
	// absent mode field on a destructive fixer returns 409 so the client is
	// forced to declare intent. Previously the default-to-dry_run path masked
	// this guard and made the check unreachable — gemini P2.
	if step.Destructive && body.Mode == "" {
		s.writeError(w, r, http.StatusConflict, "destructive fix requires explicit mode (dry_run or execute)")
		return
	}

	// Determine mode. Non-destructive fixes default to execute. (Destructive
	// fixes never reach the default branch because of the 409 guard above.)
	mode := body.Mode
	if mode == "" {
		mode = diagnostics.ModeExecute
	}
	if mode != diagnostics.ModeDryRun && mode != diagnostics.ModeExecute {
		s.writeError(w, r, http.StatusBadRequest, "mode must be 'dry_run' or 'execute'")
		return
	}

	// Rate limit per (server, code).
	key := body.Server + "|" + body.Code
	if ok, retry := globalFixLimiter.allow(key); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		s.writeError(w, r, http.StatusTooManyRequests, "Too many fix attempts; try again shortly")
		return
	}

	// Invoke the fixer.
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	started := time.Now()
	result, invokeErr := diagnostics.InvokeFixer(ctx, body.FixerKey, diagnostics.FixRequest{
		ServerID: body.Server,
		Mode:     mode,
	})
	duration := time.Since(started)

	if errors.Is(invokeErr, diagnostics.ErrUnknownFixer) {
		s.writeError(w, r, http.StatusBadRequest, "Unknown fixer_key: "+body.FixerKey)
		return
	}
	// Even on non-fatal errors we respond 200 with outcome=failed so the UI
	// can render a useful message.

	resp := map[string]interface{}{
		"outcome":     result.Outcome,
		"duration_ms": duration.Milliseconds(),
		"mode":        mode,
	}
	if result.Preview != "" {
		resp["preview"] = result.Preview
	}
	if result.FailureMsg != "" {
		resp["failure_msg"] = result.FailureMsg
	}
	if invokeErr != nil && result.FailureMsg == "" {
		resp["failure_msg"] = invokeErr.Error()
	}
	s.writeSuccess(w, resp)
}
