package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 PR-3 (US2, FR-031): the login pre-flight.
//
// `mcpproxy auth login --server=<name>` reads the stored OAuth row before it
// does anything else. When the row cannot be decrypted (a lost or different
// key), a login could not even overwrite it, so the command explains the
// situation, names the exact next commands, exits non-zero and opens no
// browser (nothing reaches discovery, DCR or the authorization server). The
// refusal itself is PR-2's guard in the connection path and the REST login
// handler; this is the message on top. It never prints key material: key ids
// are public fingerprints.

// reasonText is the words for a tokenseal reason code ("key_mismatch" ->
// "sealed with a different key"), phrased as the health detail phrases it.
func reasonText(reason string) string {
	detail := oauth.TokenUndecryptableDetail(&storage.ErrOAuthTokenUndecryptable{Reason: tokenseal.Reason(reason)})
	return strings.TrimPrefix(detail, "stored OAuth token cannot be decrypted: ")
}

// oauthUndecryptableInfo is what the explanation shows.
type oauthUndecryptableInfo struct {
	Server   string
	Reason   string // a tokenseal reason, or "" when unknown
	RowKeyID string // key id in the row's envelope, "" when none
	KeyID    string // id of the configured key, "" when none
}

// printOAuthTokenUndecryptable writes the MCPX_OAUTH_TOKEN_UNDECRYPTABLE
// explanation.
func printOAuthTokenUndecryptable(w io.Writer, info oauthUndecryptableInfo) {
	fmt.Fprintf(w, "\n❌ %s\n", diagnostics.OAuthTokenUndecryptable)
	fmt.Fprintln(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Fprintf(w, "The stored OAuth token for server %q cannot be decrypted.\n", info.Server)
	reason := info.Reason
	if reason == "" {
		fmt.Fprintln(w, "  Reason:             unknown")
	} else {
		fmt.Fprintf(w, "  Reason:             %s (%s)\n", reason, reasonText(reason))
	}
	if info.RowKeyID != "" {
		fmt.Fprintf(w, "  Row key id:         %s\n", info.RowKeyID)
	}
	if info.KeyID != "" {
		fmt.Fprintf(w, "  Configured key id:  %s\n", info.KeyID)
	}
	fmt.Fprintln(w, "\nNo browser was opened and nothing was changed: a new sign-in could not overwrite this row.")
	fmt.Fprintln(w, "\nNext steps:")
	fmt.Fprintln(w, "  1. Restore the key the row was sealed with (check oauth_token_encryption_key,")
	fmt.Fprintln(w, "     MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY[_FILE] and the mount or secret that provides it)")
	fmt.Fprintln(w, "     and restart mcpproxy; the server signs in again by itself, or")
	fmt.Fprintln(w, "  2. Discard the stored token and sign in again:")
	fmt.Fprintf(w, "       mcpproxy auth logout --server=%s\n", info.Server)
	fmt.Fprintf(w, "       mcpproxy auth login --server=%s\n", info.Server)
	fmt.Fprintln(w, "\nDetails: mcpproxy doctor (OAuth Token Storage section), or with mcpproxy stopped: mcpproxy auth tokens status")
	if entry, ok := diagnostics.Get(diagnostics.OAuthTokenUndecryptable); ok && entry.DocsURL != "" {
		fmt.Fprintf(w, "Docs: %s\n", entry.DocsURL)
	}
	fmt.Fprintln(w)
}

// undecryptableRefusal reports the explanation on stderr and returns the
// error that makes the command exit non-zero.
func undecryptableRefusal(info oauthUndecryptableInfo) error {
	printOAuthTokenUndecryptable(os.Stderr, info)
	return cliRefusalError{fmt.Errorf("%s: the stored OAuth token for server %q cannot be decrypted; no login was attempted",
		diagnostics.OAuthTokenUndecryptable, info.Server)}
}

// preflightStandaloneOAuthRow reads server's stored row through the
// key-aware storage helper before any client, discovery or browser step. It
// returns nil when the row is absent, plaintext or opens with the configured
// key. A database that cannot be opened stops the login (a running core
// holding it is exit 3): the login client would fall back to in-memory
// storage, where an undecryptable row is invisible. A key that fails to
// resolve keeps its exit-4 error.
func preflightStandaloneOAuthRow(cfg *config.Config, server *config.ServerConfig) error {
	if cfg.DataDir == "" {
		return nil
	}
	mgr, key, _, err := openStorageForCLIWithKey(cfg, zap.NewNop().Sugar(), false)
	if err != nil {
		var lockErr *storage.DatabaseLockedError
		if errors.As(err, &lockErr) {
			fmt.Fprintln(os.Stderr, lockedRemediation)
		}
		return err
	}
	defer func() { _ = mgr.Close() }()

	_, err = mgr.GetOAuthToken(oauth.GenerateServerKey(server.Name, server.URL))
	if err == nil || !oauth.IsTokenUndecryptable(err) {
		return nil
	}
	info := oauthUndecryptableInfo{Server: server.Name, KeyID: key.ID()}
	var ue *storage.ErrOAuthTokenUndecryptable
	if errors.As(err, &ue) {
		info.Reason, info.RowKeyID = string(ue.Reason), ue.RowKID
	}
	return undecryptableRefusal(info)
}

// preflightDaemonOAuthRow asks the running daemon (GET /api/v1/diagnostics,
// the doctor payload) whether server's row is undecryptable, before it is
// asked to log in. Any problem reading the diagnostics is ignored: the
// daemon's own refusal (REST 409) is rendered by oauthRefusalFromAPIError.
func preflightDaemonOAuthRow(ctx context.Context, client *cliclient.Client, serverName string) error {
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	diag, err := client.GetDiagnostics(pingCtx)
	if err != nil {
		return nil
	}
	raw, ok := diag["oauth_token_storage"]
	if !ok || raw == nil {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var rep contracts.OAuthTokenStorageReport
	if json.Unmarshal(b, &rep) != nil {
		return nil
	}
	for _, s := range rep.Servers {
		if s.Server == serverName {
			return undecryptableRefusal(oauthUndecryptableInfo{
				Server: serverName, Reason: s.Reason, RowKeyID: s.RowKeyID, KeyID: rep.KeyID,
			})
		}
	}
	return nil
}

// oauthRefusalFromAPIError renders the explanation for the daemon's own
// refusal (REST 409 carrying the diagnostics code). It returns nil when err
// is not that refusal.
func oauthRefusalFromAPIError(err error, serverName string) error {
	if err == nil || !strings.Contains(err.Error(), string(diagnostics.OAuthTokenUndecryptable)) {
		return nil
	}
	info := oauthUndecryptableInfo{Server: serverName}
	for _, r := range []tokenseal.Reason{
		tokenseal.ReasonNoKey, tokenseal.ReasonKeyMismatch, tokenseal.ReasonAuthFailed,
		tokenseal.ReasonUnsupportedVersion, tokenseal.ReasonMalformed,
	} {
		if strings.Contains(err.Error(), reasonText(string(r))) {
			info.Reason = string(r)
			break
		}
	}
	return undecryptableRefusal(info)
}
