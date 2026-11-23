//go:build !server

package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// --- transport I/O is interrupted, not only the context ------------------------

// A handler that ignores its context and sits in a write to a client that
// stopped reading ends when the request is interrupted, and its registry entry
// is released. (The grant "g1" does not exist, so the watcher ends the request
// on its first pass.)
func TestOAuthStreams_BlockedWriteIsInterrupted(t *testing.T) {
	srv := newOAuthLoginTestServer(t, func(c *config.Config) { c.MCPOAuthEnabled = true })
	done := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		srv.serveTracked(w, r, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			chunk := bytes.Repeat([]byte("x"), 1<<20)
			for {
				if _, err := w.Write(chunk); err != nil {
					return
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
		}), "g1", time.Now().Add(time.Hour), srv.oauthLoginSvc().DisableEpoch())
	}))
	defer ts.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(ts.URL, "http://"))
	require.NoError(t, err)
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	require.NoError(t, err)
	// never read: the handler fills the socket buffers and blocks in Write
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a handler blocked in a write survived the end of its login")
	}
	require.Eventually(t, func() bool { return srv.oauthTrackedRequests() == 0 }, 3*time.Second, 20*time.Millisecond)
}

// The same for a request body that stalls.
func TestOAuthStreams_StalledUploadIsInterrupted(t *testing.T) {
	srv := newOAuthLoginTestServer(t, func(c *config.Config) { c.MCPOAuthEnabled = true })
	done := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		srv.serveTracked(w, r, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
		}), "g1", time.Now().Add(time.Hour), srv.oauthLoginSvc().DisableEpoch())
	}))
	defer ts.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(ts.URL, "http://"))
	require.NoError(t, err)
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 1000000\r\n\r\nabc")
	require.NoError(t, err)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a stalled upload survived the end of its login")
	}
	require.Eventually(t, func() bool { return srv.oauthTrackedRequests() == 0 }, 3*time.Second, 20*time.Millisecond)
}

// Shutdown interrupts what is open, joins the watcher and refuses new tracking.
func TestOAuthStreams_ShutdownJoinsWatcher(t *testing.T) {
	srv := newOAuthLoginTestServer(t, func(c *config.Config) { c.MCPOAuthEnabled = true })
	interrupted := make(chan struct{})
	var once sync.Once
	release := srv.trackOAuthRequest("g1", time.Now().Add(time.Hour), srv.oauthLoginSvc().DisableEpoch(), func() { once.Do(func() { close(interrupted) }) })
	defer release()
	reg := srv.oauthStreams.Load()
	require.NotNil(t, reg)

	stopped := make(chan struct{})
	go func() { srv.stopOAuthStreams(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stopOAuthStreams did not return: the watcher was not joined")
	}
	select {
	case <-interrupted:
	default:
		t.Fatal("the tracked request was not interrupted at shutdown")
	}
	reg.mu.Lock()
	assert.False(t, reg.running, "no watcher goroutine is left")
	reg.mu.Unlock()

	late := make(chan struct{})
	var onceLate sync.Once
	srv.trackOAuthRequest("g2", time.Now().Add(time.Hour), 0, func() { onceLate.Do(func() { close(late) }) })
	select {
	case <-late:
	default:
		t.Fatal("a request tracked after shutdown must be ended at once")
	}
	srv.stopOAuthStreams() // idempotent
}

// --- real daemon: what ends an open stream ------------------------------------

type streamDaemon struct {
	t     *testing.T
	env   *TestEnvironment
	base  string
	svc   *runtime.OAuthLoginService
	httpc *http.Client
}

func newStreamDaemon(t *testing.T) *streamDaemon {
	t.Helper()
	env := NewTestEnvironmentWithOptions(t, TestEnvironmentOptions{
		Mutate: func(cfg *config.Config, _ string) { cfg.MCPOAuthEnabled = true },
	})
	t.Cleanup(env.Cleanup)
	return &streamDaemon{
		t: t, env: env, base: strings.TrimSuffix(env.proxyAddr, "/mcp"), svc: env.proxyServer.oauthLoginSvc(),
		httpc: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

// login runs the whole flow for a per-client address and returns the tokens
// and the registration id.
func (d *streamDaemon) login(client string) (access, refresh, regID string) {
	t := d.t
	redirect := "http://127.0.0.1:50999/callback"
	resp, err := d.httpc.Post(d.base+"/oauth/register", "application/json", strings.NewReader(`{"redirect_uris":["`+redirect+`"]}`))
	require.NoError(t, err)
	var reg map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&reg))
	resp.Body.Close()
	regID, _ = reg["client_id"].(string)
	verifier := strings.Repeat("q", 50)
	sum := sha256.Sum256([]byte(verifier))
	resource := d.base + "/mcp/oauth/" + client
	q := url.Values{"response_type": {"code"}, "client_id": {regID}, "redirect_uri": {redirect},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "resource": {resource}}
	resp, err = d.httpc.Get(d.base + "/oauth/authorize?" + q.Encode())
	require.NoError(t, err)
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	redir, err := d.svc.Approve(context.Background(), loc.Query().Get("request"), runtime.ApprovalChoice{Client: runtime.ClientChoice{ID: client}})
	require.NoError(t, err)
	ru, _ := url.Parse(redir)
	form := url.Values{"grant_type": {"authorization_code"}, "code": {ru.Query().Get("code")}, "redirect_uri": {redirect},
		"client_id": {regID}, "code_verifier": {verifier}, "resource": {resource}}
	resp, err = d.httpc.Post(d.base+"/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	defer resp.Body.Close()
	var tok map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&tok))
	access, _ = tok["access_token"].(string)
	refresh, _ = tok["refresh_token"].(string)
	require.NotEmpty(t, access)
	return access, refresh, regID
}

func (d *streamDaemon) refresh(regID, refresh string) (access, newRefresh string, status int) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {regID}}
	resp, err := d.httpc.Post(d.base+"/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	require.NoError(d.t, err)
	defer resp.Body.Close()
	var tok map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&tok)
	access, _ = tok["access_token"].(string)
	newRefresh, _ = tok["refresh_token"].(string)
	return access, newRefresh, resp.StatusCode
}

// openStream opens a session on the client's OAuth address and an SSE GET
// stream on it; the channel closes when the stream ends.
func (d *streamDaemon) openStream(client, access string) chan struct{} {
	t := d.t
	do := func(method, body, session string) *http.Response {
		req, _ := http.NewRequest(method, d.base+"/mcp/oauth/"+client, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("Accept", "application/json, text/event-stream")
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
		}
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		resp, err := d.httpc.Do(req)
		require.NoError(t, err)
		return resp
	}
	resp := do(http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`, "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	session := resp.Header.Get("Mcp-Session-Id")
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	do(http.MethodPost, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, session).Body.Close()
	stream := do(http.MethodGet, "", session)
	require.Equal(t, http.StatusOK, stream.StatusCode)
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, stream.Body)
		stream.Body.Close()
		close(done)
	}()
	return done
}

func streamOpen(done chan struct{}, d time.Duration) bool {
	select {
	case <-done:
		return false
	case <-time.After(d):
		return true
	}
}

func (d *streamDaemon) requireEnds(done chan struct{}, what string) {
	d.t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		d.t.Fatalf("the stream did not end after %s", what)
	}
}

func (d *streamDaemon) setEnabled(on bool) {
	cfg := d.env.proxyServer.runtime.Config()
	next := *cfg
	next.MCPOAuthEnabled = on
	d.env.proxyServer.runtime.UpdateConfig(&next, "")
}

// A switch-off followed at once by a switch-on still ends what was open before
// it; a stream opened after the re-enable stays.
func TestOAuthStreams_QuickOffOnEndsPreDisableStreams(t *testing.T) {
	d := newStreamDaemon(t)
	access, _, _ := d.login("cursor")
	before := d.openStream("cursor", access)
	require.True(t, streamOpen(before, time.Second))

	d.setEnabled(false)
	d.setEnabled(true)
	d.requireEnds(before, "off -> on")

	after := d.openStream("cursor", access) // the login itself survives off -> on
	require.True(t, streamOpen(after, 2500*time.Millisecond), "a stream opened after the re-enable stays open")
}

// Client revoke, client forget and replay-revoke each end the affected stream;
// an unrelated client's stream stays open.
func TestOAuthStreams_ClientRevokeForgetAndReplayEndStreams(t *testing.T) {
	d := newStreamDaemon(t)
	rt := d.env.proxyServer.runtime
	bystander, _, _ := d.login("windsurf")
	bystanderStream := d.openStream("windsurf", bystander)

	// replay-revoke: an authentic old refresh token presented again.
	{
		access1, refresh1, regID := d.login("codex")
		access2, _, status := d.refresh(regID, refresh1)
		require.Equal(t, http.StatusOK, status)
		require.NotEqual(t, access1, access2)
		stream := d.openStream("codex", access2)
		require.True(t, streamOpen(stream, 500*time.Millisecond))
		_, _, status = d.refresh(regID, refresh1) // replay
		require.Equal(t, http.StatusBadRequest, status)
		d.requireEnds(stream, "a refresh-token replay")
	}

	// client revoke
	{
		access, _, _ := d.login("gemini")
		stream := d.openStream("gemini", access)
		require.True(t, streamOpen(stream, 500*time.Millisecond))
		_, err := rt.CredentialsService().Revoke(context.Background(), runtime.Actor{}, runtime.CredentialRef{Client: "gemini"}, false)
		require.NoError(t, err)
		d.requireEnds(stream, "a client revoke")
	}

	// client forget
	{
		access, _, _ := d.login("opencode")
		stream := d.openStream("opencode", access)
		require.True(t, streamOpen(stream, 500*time.Millisecond))
		_, err := rt.ClientsService().Forget(context.Background(), runtime.Actor{}, "opencode", false)
		require.NoError(t, err)
		d.requireEnds(stream, "a client forget")
	}

	require.True(t, streamOpen(bystanderStream, time.Second), "an unrelated client's stream stays open")
}
