package core

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/dialgate"
)

const refreshReplayFirstAttemptTimeout = 10 * time.Second
const refreshReplayBudget = 30 * time.Second

// withRefreshReplay makes at most one extra attempt inside the coordinator's
// existing deadline. It is only enabled by an operator for a pinned endpoint
// known to support idempotent replay; it is never inferred from a network error.
func (c *Client) withRefreshReplay(refresh oauth.RefreshFunc, endpoint func(context.Context) (string, error)) oauth.RefreshFunc {
	if c.config == nil || c.config.Command != "" || !c.config.OAuth.RefreshReplayAllowed() || c.storage == nil || endpoint == nil {
		return refresh
	}
	tokenEndpoint := c.config.OAuth.TokenEndpoint
	return func(ctx context.Context, rec *storage.OAuthTokenRecord) (*client.Token, error) {
		budget, cancel := context.WithTimeout(ctx, refreshReplayBudget)
		defer cancel()
		if c.Gate().Retired() {
			return nil, dialgate.ErrRetired
		}
		target, lookupErr := endpoint(budget)
		if budget.Err() != nil {
			return nil, budget.Err()
		}
		if lookupErr != nil || target != tokenEndpoint {
			// An unverified AS retains the original deadline and one-shot
			// behavior, including the original metadata/error scrubbing path.
			c.logRefreshReplaySkipped("token_endpoint_unverified")
			return refresh(ctx, rec)
		}
		first, cancelFirst := context.WithTimeout(budget, refreshReplayFirstAttemptTimeout)
		start := time.Now()
		tok, err := refresh(first, rec)
		cancelFirst()
		c.logRefreshReplayAttempt(1, start, err)
		if err == nil {
			return tok, nil
		}
		if !retryableRefreshReplayError(err) || budget.Err() != nil || c.Gate().Retired() {
			c.logRefreshReplaySkipped("failure_not_retryable_or_deadline")
			return nil, err
		}

		// Discovery must still identify the same explicitly pinned provider.
		// Overrides/redirected discovery never opt another AS into replay.
		target, lookupErr = endpoint(budget)
		if lookupErr != nil || target != tokenEndpoint {
			c.logRefreshReplaySkipped("token_endpoint_unverified")
			return nil, err
		}
		current, loadErr := c.storage.GetOAuthTokenBound(
			oauth.GenerateServerKey(c.config.Name, c.config.URL), c.CredentialBinding())
		if loadErr != nil || current == nil || rec == nil ||
			current.RefreshToken != rec.RefreshToken || current.ClientID != rec.ClientID ||
			current.Revision != rec.Revision {
			// A login, logout, same-binding write or retirement superseded the
			// attempt. Let the existing coordinator reconcile it, never resend.
			c.logRefreshReplaySkipped("stored_record_changed_or_unavailable")
			return nil, err
		}
		if budget.Err() != nil || c.Gate().Retired() {
			c.logRefreshReplaySkipped("deadline_or_retired")
			return nil, err
		}
		start = time.Now()
		tok, err = refresh(budget, current)
		c.logRefreshReplayAttempt(2, start, err)
		return tok, err
	}
}

func retryableRefreshReplayError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || dialgate.IsRetired(err) {
		return false
	}
	class, _ := oauth.ClassifyRefreshError(err)
	if class != oauth.RefreshClassNetwork {
		return false
	}
	// Do not use the classifier's substring fallback as permission to replay.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}

func (c *Client) logRefreshReplayAttempt(attempt int, start time.Time, err error) {
	if c.logger == nil {
		return
	}
	class, status := oauth.ClassifyRefreshError(err)
	c.logger.Info("OAuth refresh attempt finished",
		zap.Int("attempt", attempt), zap.String("outcome", string(class)),
		zap.String("transport_failure", refreshReplayTransportFailureKind(err)),
		zap.Int("http_status", status), zap.Duration("duration", time.Since(start)))
}

func refreshReplayTransportFailureKind(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "truncated_response"
	case errors.Is(err, io.EOF):
		return "eof"
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		if networkError.Timeout() {
			return "timeout"
		}
		return "network"
	}
	return "none"
}

func (c *Client) logRefreshReplaySkipped(reason string) {
	if c.logger != nil {
		c.logger.Info("OAuth quick retry skipped", zap.String("reason", reason))
	}
}
