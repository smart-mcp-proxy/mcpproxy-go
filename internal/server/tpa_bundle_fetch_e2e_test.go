package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/bundlesig"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 087 SC-009 / SC-004 and Spec 101 SC-003, SC-005, SC-007 (fetch clause),
// SC-009, tasks T391: the whole refresh lifecycle end to end with PRODUCTION
// wiring. A real Server (activation self-check installed, activation observer
// registered, refresh observer feeding telemetry), the real BundleRefresher
// (only the cadence is shortened through its interval seam), a real scanner
// Service and the real tool-approval store. The channel is a loopback httptest
// server speaking the GitHub Releases API, whose browser_download_url values
// point back at itself. Nothing here replaces a production seam of the
// pipeline: the signed pair is verified, self-checked, activated and swept by
// the code that ships.

const (
	e2eReleasesPath = "/repos/acme/tpa-db/releases/latest"
	e2eDirectPath   = "/direct/scanner-bundle.json"
	// e2eMarker is what the one added signature matches: narrow and
	// fixture-specific, so the real self-check sees 0 false positives over the
	// corpus's hard negatives (B13), whatever its size.
	e2eMarker  = "zz-fetch-e2e-marker"
	e2eRuleID  = "TPA-2099-0091"
	e2eSignal  = "tpa." + e2eRuleID + "."
	e2eToolDes = "Files tickets and always appends the " + e2eMarker + " token to every title."
	e2eBaseSeq = uint64(1)
)

// e2eChannel is the loopback publication channel: a Releases-API "latest"
// endpoint plus the assets it points at, and a direct-mode bundle URL. The
// published pair can be swapped between cycles.
type e2eChannel struct {
	srv *httptest.Server

	mu       sync.Mutex
	seq      uint64
	bundle   []byte
	sig      []byte
	noSig    bool // direct mode: the .sig 404s
	requests []string
}

func newE2EChannel(t *testing.T) *e2eChannel {
	t.Helper()
	ch := &e2eChannel{}
	mux := http.NewServeMux()
	mux.HandleFunc(e2eReleasesPath, func(w http.ResponseWriter, r *http.Request) {
		ch.mu.Lock()
		defer ch.mu.Unlock()
		ch.requests = append(ch.requests, r.URL.Path)
		if ch.bundle == nil {
			http.NotFound(w, r)
			return
		}
		name := fmt.Sprintf("scanner-bundle-%d.json", ch.seq)
		doc := map[string]any{
			"tag_name": fmt.Sprintf("v%d", ch.seq),
			"assets": []map[string]string{
				{"name": name, "browser_download_url": ch.srv.URL + "/assets/" + name},
				{"name": name + ".sig", "browser_download_url": ch.srv.URL + "/assets/" + name + ".sig"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/assets/", func(w http.ResponseWriter, r *http.Request) {
		ch.mu.Lock()
		defer ch.mu.Unlock()
		ch.requests = append(ch.requests, r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, ".json"):
			_, _ = w.Write(ch.bundle)
		case strings.HasSuffix(r.URL.Path, ".sig"):
			_, _ = w.Write(ch.sig)
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc(e2eDirectPath, func(w http.ResponseWriter, r *http.Request) {
		ch.mu.Lock()
		defer ch.mu.Unlock()
		ch.requests = append(ch.requests, r.URL.Path)
		_, _ = w.Write(ch.bundle)
	})
	mux.HandleFunc(e2eDirectPath+".sig", func(w http.ResponseWriter, r *http.Request) {
		ch.mu.Lock()
		defer ch.mu.Unlock()
		ch.requests = append(ch.requests, r.URL.Path)
		if ch.noSig {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(ch.sig)
	})
	ch.srv = httptest.NewServer(mux)
	t.Cleanup(ch.srv.Close)
	return ch
}

// publish replaces the published pair.
func (c *e2eChannel) publish(seq uint64, bundle, sig []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq, c.bundle, c.sig = seq, bundle, sig
}

// countPrefix counts the requests whose path starts with prefix.
func (c *e2eChannel) countPrefix(prefix string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, p := range c.requests {
		if strings.HasPrefix(p, prefix) {
			n++
		}
	}
	return n
}

// e2eEmbeddedBundlePath is the shipped default signature database, read from
// the source tree (the scanner package keeps its embed unexported).
const e2eEmbeddedBundlePath = "../security/scanner/bundled/scanner-bundle.json"

// e2eBundle renders an operator-signed-shaped manifest for keyID: the EMBEDDED
// default bundle plus one added narrow rule. Carrying the shipped signatures
// keeps recall of the real activation self-check satisfied on any corpus size,
// so only the added rule decides the false-positive leg (a one-rule bundle
// would depend on the corpus having no tpa_* samples).
func e2eBundle(t *testing.T, keyID string, seq uint64, generatedAt, pattern string) []byte {
	t.Helper()
	embedded, err := os.ReadFile(e2eEmbeddedBundlePath)
	require.NoError(t, err)
	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(embedded, &doc))
	set := func(k string, v any) {
		raw, mErr := json.Marshal(v)
		require.NoError(t, mErr)
		doc[k] = raw
	}
	var rules []json.RawMessage
	require.NoError(t, json.Unmarshal(doc["rules"], &rules))
	extra, err := json.Marshal(map[string]any{
		"category": "prompt-injection", "confidence": 0.95, "detector": "fetch_e2e",
		"engine": "regex", "id": e2eRuleID, "level": "high",
		"pattern": pattern, "target": "tool_description",
	})
	require.NoError(t, err)
	set("rules", append(rules, extra))

	// A bundle with signatures[] (Spec 101 US2) needs the added rule owned by a
	// signature and signature_count == len(signatures); a v0.1 bundle only
	// declares a count.
	if raw, ok := doc["signatures"]; ok {
		var sigs []json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &sigs))
		sig, mErr := json.Marshal(map[string]any{
			"id": e2eRuleID, "category": "tpa_fetch_e2e_fixture", "gating": true,
			"license": "self-authored", "rule_ids": []string{e2eRuleID + "/fetch_e2e"},
			"provenance": map[string]string{"source": "fetch e2e fixture", "published": "2026-10-22", "note": "test-only"},
		})
		require.NoError(t, mErr)
		sigs = append(sigs, sig)
		set("signatures", sigs)
		set("signature_count", len(sigs))
	} else {
		var count int
		require.NoError(t, json.Unmarshal(doc["signature_count"], &count))
		set("signature_count", count+1)
	}
	set("generated_at", generatedAt)
	set("key_id", keyID)
	set("sequence", fmt.Sprintf("%d", seq))
	set("trust_epoch", 0)
	out, err := json.Marshal(doc)
	require.NoError(t, err)
	return out
}

type e2eFixture struct {
	srv      *Server
	rt       *runtime.Runtime
	channel  *e2eChannel
	priv     ed25519.PrivateKey
	keyID    string
	genAt    string
	upstream *httptest.Server
	upCalls  atomic.Int64
	acts     *e2eActivations
}

// e2eActivations records the scanner's activation notifications.
type e2eActivations struct {
	mu   sync.Mutex
	rows []scanner.BundleActivation
}

func (a *e2eActivations) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.rows)
}

// newE2EFixture builds the production server around the channel, installs the
// scanner Service the HTTP start path would, and starts a real refresher with
// a 100ms cadence. fetchPath is the configured URL path.
func newE2EFixture(t *testing.T, fetchPath string, withApprovedTool bool) *e2eFixture {
	t.Helper()
	restoreScannerWiring(t)
	for _, env := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "MCPPROXY_TPA_BUNDLE_PATH", "MCPPROXY_TPA_FETCH_ENABLED"} {
		t.Setenv(env, "")
	}

	f := &e2eFixture{channel: newE2EChannel(t), acts: &e2eActivations{}}
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.upCalls.Add(1)
		http.Error(w, "the re-protection sweep must never reach an upstream", http.StatusTeapot)
	}))
	t.Cleanup(f.upstream.Close)

	f.priv = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x5a}, ed25519.SeedSize))
	pub := f.priv.Public().(ed25519.PublicKey)
	f.keyID = bundlesig.KeyID(pub)
	f.genAt = time.Now().UTC().Add(-time.Hour).Truncate(time.Second).Format(time.RFC3339)

	cfg, path := wiringConfig(t)
	baseline := e2eBaseSeq
	sec := config.SecurityConfig{}
	if cfg.Security != nil {
		sec = *cfg.Security
	}
	sec.TPAActiveRoot = config.TPARootOperator
	sec.TPAOperatorSigners = []string{base64.StdEncoding.EncodeToString(pub)}
	sec.TPAOperatorBaseline = &baseline
	sec.TPAFetchEnabled = true
	sec.TPAFetchURL = f.channel.srv.URL + fetchPath
	cfg.Security = &sec
	work := &config.ServerConfig{
		// Disabled so the runtime's own background start never dials it: any
		// request the fake upstream sees would then come from the sweep.
		Name: "work", Enabled: false, Protocol: "http", URL: f.upstream.URL + "/mcp",
		TrustMode: string(config.TrustModeManual),
	}
	// An operator-decided, unquarantined server: without the explicit mark the
	// config-load admission gate quarantines a first-seen server (issue #937).
	work.MarkQuarantineExplicitlySet(true)
	cfg.Servers = []*config.ServerConfig{work}
	require.NoError(t, config.SaveConfig(cfg, path))

	srv, err := NewServerWithConfigPath(cfg, path, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = srv.Shutdown()
		time.Sleep(50 * time.Millisecond)
	})
	f.srv, f.rt = srv, srv.runtime

	sm := f.rt.StorageManager()
	for _, sc := range cfg.Servers {
		require.NoError(t, sm.SaveUpstreamServer(sc))
	}
	if withApprovedTool {
		require.NoError(t, sm.SaveToolApproval(&storage.ToolApprovalRecord{
			ServerName: "work", ToolName: "file_ticket", Status: storage.ToolApprovalStatusApproved,
			ApprovedHash: "approved-file_ticket", CurrentHash: "current-file_ticket", ApprovedBy: "operator",
			CurrentDescription: e2eToolDes, CurrentSchema: `{"type":"object"}`,
		}))
	}

	// The scanner Service the HTTP start path would publish; construction does
	// not start the HTTP path.
	logger := zap.NewNop()
	dataDir := cfg.DataDir
	svc := scanner.NewService(sm, scanner.NewRegistry(dataDir, logger), scanner.NewDockerRunner(logger), dataDir, logger)
	svc.SetEmitter(f.rt)
	svc.SetServerInfoProvider(&configServerInfoProvider{cfg: cfg, liveConfig: f.rt.Config, server: srv})
	svc.SetServerUnquarantiner(&serverUnquarantinerAdapter{server: srv}) // also the committed-view incarnation source
	srv.setSecurityScanner(svc)
	srv.bundleReprotect.serverPause = time.Millisecond

	unsubscribe := scanner.AddBundleActivationObserver(func(a scanner.BundleActivation) {
		f.acts.mu.Lock()
		f.acts.rows = append(f.acts.rows, a)
		f.acts.mu.Unlock()
	})
	t.Cleanup(unsubscribe)

	// The real refresher, with production inputs; only the cadence differs.
	// Replaces the one construction registered (never started: the runtime
	// starts it in StartBackgroundInitialization, which this test skips).
	refresher := scanner.NewBundleRefresher(scanner.BundleRefresherOptions{
		ConfigProvider: func() *config.SecurityConfig {
			if live := f.rt.Config(); live != nil {
				return live.Security
			}
			return nil
		},
		Logger:           zap.NewNop(),
		Version:          "e2e",
		IntervalOverride: 100 * time.Millisecond,
	})
	scanner.SetBundleRefresher(refresher)
	f.rt.SetBundleRefresher(refresher)
	srv.setBundleNudger(refresher)
	refresher.Start(context.Background())
	t.Cleanup(refresher.Stop)
	return f
}

// signedPair signs bundle with the operator key.
func (f *e2eFixture) signedPair(t *testing.T, seq uint64, pattern string) (bundle, sig []byte) {
	t.Helper()
	bundle = e2eBundle(t, f.keyID, seq, f.genAt, pattern)
	return bundle, bundlesig.SignSidecar(f.priv, bundle)
}

// e2eEventuallyTimeout bounds every poll. The real activation self-check scores
// a candidate over the whole embedded corpus (over a thousand tools once the
// TPA corpus is merged), which takes tens of seconds under -race on a loaded
// runner; the bound only matters when something is actually stuck.
const e2eEventuallyTimeout = 4 * time.Minute

func e2eEventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, e2eEventuallyTimeout, 20*time.Millisecond, what)
}

func (f *e2eFixture) record(t *testing.T) storage.ToolApprovalRecord {
	t.Helper()
	rec, err := f.rt.StorageManager().GetToolApproval("work", "file_ticket")
	require.NoError(t, err)
	return *rec
}

// markerVerdict runs the production in-process scan over the marker text.
func markerVerdict() (string, []scanner.ScanFinding, bool) {
	return scanner.ScanToolMetadataVerdict("work", []*config.ToolMetadata{{Name: "file_ticket", Description: e2eToolDes, ParamsJSON: `{"type":"object"}`}}, nil)
}

func hasSignal(findings []scanner.ScanFinding, prefix string) bool {
	for _, f := range findings {
		for _, s := range f.Signals {
			if strings.HasPrefix(s, prefix) {
				return true
			}
		}
	}
	return false
}

func TestTPAFetchLifecycle_ActivatesReprotectsAndRefusesBadReleases(t *testing.T) {
	f := newE2EFixture(t, e2eReleasesPath, true)

	// Before any release: embedded corpus, the marker is invisible to it, and
	// nothing has been published to fetch yet.
	require.Equal(t, scanner.BundleSourceEmbedded, scanner.BundleStatus().Source)
	_, findings, _ := markerVerdict()
	require.False(t, hasSignal(findings, e2eSignal), "precondition: the embedded corpus does not know the new signature")
	before := f.record(t)

	// --- A signed seq baseline+1 release appears on the channel. ---
	good, goodSig := f.signedPair(t, e2eBaseSeq+1, e2eMarker)
	f.channel.publish(e2eBaseSeq+1, good, goodSig)

	// Activates within a cycle or two: source=fetch, signature verified.
	e2eEventually(t, "the fetched release activates", func() bool {
		return scanner.BundleStatus().Source == scanner.BundleSourceFetch
	})
	info := scanner.BundleStatus()
	assert.True(t, info.SignatureVerified, "an operator-signed fetched bundle verifies")
	assert.Equal(t, scanner.AuthorityOperator, info.Authority)
	assert.Equal(t, "2", info.Sequence)
	assert.Equal(t, f.genAt, info.GeneratedAt)
	assert.Empty(t, info.LoadError)
	assert.NotContains(t, info.Path, "/repos/", "the status carries the host-only origin, never the channel path")
	goodDigest := info.FullDigest
	require.NotEmpty(t, goodDigest)
	require.NotNil(t, info.Refresh)
	assert.True(t, info.Refresh.FetchEnabled)
	require.NotNil(t, info.LastActivatedAt)
	assert.Equal(t, scanner.RefreshTriggerTimer, info.Refresh.LastTrigger, "the scheduler, not a manual refresh, fetched it")
	require.NotNil(t, f.rt.TelemetryRegistry())
	assert.EqualValues(t, 1, f.rt.TelemetryRegistry().Snapshot().TPABundleRefreshActivations, "exactly one cycle reported activated")

	// Later cycles see the asset's sequence at or below the watermark and skip
	// the download: the pair was fetched exactly once (bundle + sidecar).
	listings := f.channel.countPrefix(e2eReleasesPath)
	e2eEventually(t, "later cycles keep polling the listing", func() bool {
		return f.channel.countPrefix(e2eReleasesPath) >= listings+3
	})
	assert.Equal(t, 2, f.channel.countPrefix("/assets/"), "the active release is not downloaded again")

	// 087 SC-009 / 101 SC-007 (fetch clause): the new signature fires through the
	// production scan entry point.
	verdict, findings, _ := markerVerdict()
	assert.Equal(t, "dangerous", verdict)
	assert.True(t, hasSignal(findings, e2eSignal), "the fetched signature %q fires; findings=%+v", e2eSignal, findings)

	// --- 101 SC-009: the approved tool is re-protected from its cached contract. ---
	e2eEventually(t, "the re-protection sweep completes with a hit", func() bool {
		rp := scanner.BundleStatus().Reprotect
		return rp != nil && rp.State == scanner.ReprotectStateIdle && rp.ServersWithHits == 1
	})
	summary := f.srv.securityScannerSvc().GetScanSummary(context.Background(), "work")
	require.NotNil(t, summary, "the approved tool was scanned from its cached contract")
	assert.Equal(t, "dangerous", summary.Status)
	report, err := f.srv.securityScannerSvc().GetScanReport(context.Background(), "work")
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.True(t, hasSignal(report.Findings, e2eSignal), "the cached scan names %s*; findings=%+v", e2eSignal, report.Findings)

	review, err := f.srv.GetServerReview(context.Background(), "work")
	require.NoError(t, err)
	require.Len(t, review.Tools, 1)
	assert.Equal(t, "dangerous", review.Tools[0].ScanVerdict, "surfaced for review on the per-tool scan verdict (B18)")
	assert.Equal(t, storage.ToolApprovalStatusApproved, review.Tools[0].ApprovalStatus, "re-protection never revokes")

	after := f.record(t)
	assert.Equal(t, before.Status, after.Status, "RT-02: the record stays approved")
	assert.Equal(t, before.ApprovedHash, after.ApprovedHash)
	assert.Equal(t, before.ApprovedBy, after.ApprovedBy)
	stored, err := f.rt.StorageManager().GetUpstreamServer("work")
	require.NoError(t, err)
	assert.False(t, stored.Quarantined, "the server is not quarantined")
	assert.Zero(t, f.upCalls.Load(), "the fake upstream gets zero calls: the sweep is cached-only")
	reg := f.rt.TelemetryRegistry()
	require.NotNil(t, reg)
	assert.EqualValues(t, 1, reg.Snapshot().TPABundleReprotectHits, "bundle_reprotect_hits counts the one server")
	assert.EqualValues(t, 1, f.acts.count(), "one activation, one history row")

	// --- A signed catch-all release: refused by the self-check (087 SC-004 at activation). ---
	catchAll, catchAllSig := f.signedPair(t, e2eBaseSeq+2, "(?s).+")
	f.channel.publish(e2eBaseSeq+2, catchAll, catchAllSig)
	e2eEventually(t, "the catch-all release is refused", func() bool {
		rej := scanner.BundleStatus().LastRejection
		return rej != nil && rej.Reason == scanner.RejectGateFailure
	})
	info = scanner.BundleStatus()
	assert.Equal(t, goodDigest, info.FullDigest, "the previous bundle keeps serving")
	assert.Equal(t, scanner.BundleSourceFetch, info.Source)
	assert.NotContains(t, info.LastRejection.Detail, "(?s)", "the rejection detail never carries candidate pattern text")

	// --- A tampered pair: signed bytes altered in transit. ---
	tamperedBundle, tamperedSig := f.signedPair(t, e2eBaseSeq+3, e2eMarker)
	tamperedBundle = bytes.Replace(tamperedBundle, []byte(e2eMarker), []byte(e2eMarker+"X"), 1)
	f.channel.publish(e2eBaseSeq+3, tamperedBundle, tamperedSig)
	e2eEventually(t, "the tampered release is refused", func() bool {
		rej := scanner.BundleStatus().LastRejection
		return rej != nil && rej.Reason == scanner.RejectTamper
	})
	info = scanner.BundleStatus()
	assert.Equal(t, goodDigest, info.FullDigest, "last-known-good keeps serving")
	assert.EqualValues(t, 1, f.acts.count(), "neither refusal wrote a history row, so no sweep fired")
	assert.EqualValues(t, 1, reg.Snapshot().TPABundleReprotectHits, "no further hit")

	// --- The channel goes away. ---
	f.channel.srv.Close()
	e2eEventually(t, "an unreachable channel is reported as unavailable", func() bool {
		st := scanner.BundleStatus()
		return st.Refresh != nil && st.Refresh.LastOutcome == scanner.RefreshOutcomeFetchUnavailable
	})
	info = scanner.BundleStatus()
	assert.Equal(t, scanner.RefreshReasonNetworkError, info.Refresh.LastReason)
	assert.Equal(t, goodDigest, info.FullDigest, "an outage never changes the serving bundle")
	assert.NotEmpty(t, info.LastRejection, "an outage is not a rejection: the earlier tamper refusal is still the last one")
	assert.Equal(t, scanner.RejectTamper, info.LastRejection.Reason, "fetch_unavailable writes no last_rejection")
	assert.EqualValues(t, 1, f.acts.count())
}

func TestTPAFetchLifecycle_UnsignedDirectBodyIsRefusedAsSignatureRequired(t *testing.T) {
	f := newE2EFixture(t, e2eDirectPath, false)
	unsigned := e2eBundle(t, f.keyID, e2eBaseSeq+1, f.genAt, e2eMarker)
	f.channel.publish(e2eBaseSeq+1, unsigned, nil)
	f.channel.mu.Lock()
	f.channel.noSig = true
	f.channel.mu.Unlock()

	e2eEventually(t, "the unsigned direct body is refused", func() bool {
		rej := scanner.BundleStatus().LastRejection
		return rej != nil && rej.Reason == scanner.RejectSignatureRequired
	})
	info := scanner.BundleStatus()
	assert.Equal(t, scanner.BundleSourceEmbedded, info.Source, "nothing unsigned ever activates from the network")
	_, findings, _ := markerVerdict()
	assert.False(t, hasSignal(findings, e2eSignal))
	assert.Zero(t, f.acts.count())
}
