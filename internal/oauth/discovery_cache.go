package oauth

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// Spec 113-b FR-026/FR-027: preflight OAuth discovery results (PRM, AS metadata,
// scopes) are cached per (server URL, endpoint overrides) so that repeated
// createOAuthConfigInternal calls and every new mcp-go handler stop re-fetching
// the same documents.
const (
	discoverySuccessTTL      = time.Hour
	discoveryFailureTTL      = 30 * time.Second
	discoveryCacheMaxEntries = 256
)

// discoveryKey is a hash of (kind, server URL, the four override fields, extras).
// A change to the server URL or any override yields a new key, so no hot-reload
// hook is needed; the old key ages out (FR-027).
type discoveryKey [32]byte

// discoveryOverrides are the config fields that change what discovery returns.
type discoveryOverrides struct {
	authz, token, registration, metadataURL string
}

func overridesFromConfig(sc *config.ServerConfig) discoveryOverrides {
	if sc == nil || sc.OAuth == nil {
		return discoveryOverrides{}
	}
	return discoveryOverrides{
		authz:        sc.OAuth.AuthorizationEndpoint,
		token:        sc.OAuth.TokenEndpoint,
		registration: sc.OAuth.RegistrationEndpoint,
		metadataURL:  sc.OAuth.AuthServerMetadataURL,
	}
}

func makeDiscoveryKey(kind, serverURL string, ov discoveryOverrides, extra ...string) discoveryKey {
	h := sha256.New()
	write := func(s string) {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	write(kind)
	write(serverURL)
	write(ov.authz)
	write(ov.token)
	write(ov.registration)
	write(ov.metadataURL)
	for _, e := range extra {
		write(e)
	}
	var k discoveryKey
	copy(k[:], h.Sum(nil))
	return k
}

type discoveryEntry struct {
	key       discoveryKey
	serverURL string
	val       any
	err       error
	expires   time.Time
	el        *list.Element
}

type discoveryFlight struct {
	done chan struct{}
	val  any
	err  error
}

type discoveryCache struct {
	mu       sync.Mutex
	now      func() time.Time
	entries  map[discoveryKey]*discoveryEntry
	lru      *list.List // front = most recently used
	inflight map[discoveryKey]*discoveryFlight
	prmURL   map[string]string // server URL -> last seen resource_metadata URL
}

func newDiscoveryCache(now func() time.Time) *discoveryCache {
	if now == nil {
		now = time.Now
	}
	return &discoveryCache{
		now:      now,
		entries:  make(map[discoveryKey]*discoveryEntry),
		lru:      list.New(),
		inflight: make(map[discoveryKey]*discoveryFlight),
		prmURL:   make(map[string]string),
	}
}

// globalDiscoveryCache is shared by every server in the process.
var globalDiscoveryCache = newDiscoveryCache(time.Now)

func (c *discoveryCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *discoveryCache) removeLocked(e *discoveryEntry) {
	c.lru.Remove(e.el)
	delete(c.entries, e.key)
}

func (c *discoveryCache) lookupLocked(key discoveryKey) (any, bool, error) {
	e, ok := c.entries[key]
	if !ok {
		return nil, false, nil
	}
	if !c.now().Before(e.expires) {
		c.removeLocked(e)
		return nil, false, nil
	}
	c.lru.MoveToFront(e.el)
	return e.val, true, e.err
}

// store records a result. ttl <= 0 selects the default for success/failure.
func (c *discoveryCache) store(key discoveryKey, serverURL string, val any, err error, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.storeLocked(key, serverURL, val, err, ttl)
}

func (c *discoveryCache) storeLocked(key discoveryKey, serverURL string, val any, err error, ttl time.Duration) {
	if ttl <= 0 {
		ttl = discoverySuccessTTL
		if err != nil {
			ttl = discoveryFailureTTL
		}
	}
	if old, ok := c.entries[key]; ok {
		c.removeLocked(old)
	}
	e := &discoveryEntry{key: key, serverURL: serverURL, val: val, err: err, expires: c.now().Add(ttl)}
	e.el = c.lru.PushFront(e)
	c.entries[key] = e
	for len(c.entries) > discoveryCacheMaxEntries {
		oldest := c.lru.Back()
		if oldest == nil {
			break
		}
		c.removeLocked(oldest.Value.(*discoveryEntry))
	}
}

// do returns the cached result for key or runs fn once (single-flight) and caches it.
// fn may return a non-zero ttl to override the default success/failure TTL.
func (c *discoveryCache) do(key discoveryKey, serverURL string, fn func() (any, time.Duration, error)) (any, error) {
	c.mu.Lock()
	if v, ok, err := c.lookupLocked(key); ok {
		c.mu.Unlock()
		return v, err
	}
	if fl, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		<-fl.done
		return fl.val, fl.err
	}
	fl := &discoveryFlight{done: make(chan struct{})}
	c.inflight[key] = fl
	c.mu.Unlock()

	val, ttl, err := fn()

	c.mu.Lock()
	c.storeLocked(key, serverURL, val, err, ttl)
	delete(c.inflight, key)
	c.mu.Unlock()
	fl.val, fl.err = val, err
	close(fl.done)
	return val, err
}

// invalidateServer drops every entry (all override combinations) for serverURL.
func (c *discoveryCache) invalidateServer(serverURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		if e.serverURL == serverURL {
			c.removeLocked(e)
		}
	}
	delete(c.prmURL, serverURL)
}

// noteResourceMetadataURL records the resource_metadata URL a server advertised.
// When it differs from the one previously seen, the server's cached discovery
// results are stale and are dropped (FR-026). Reports whether it invalidated.
func (c *discoveryCache) noteResourceMetadataURL(serverURL, prmURL string) bool {
	if prmURL == "" {
		return false
	}
	c.mu.Lock()
	prev, seen := c.prmURL[serverURL]
	c.prmURL[serverURL] = prmURL
	c.mu.Unlock()
	if seen && prev != prmURL {
		c.mu.Lock()
		for _, e := range c.entries {
			if e.serverURL == serverURL {
				c.removeLocked(e)
			}
		}
		c.mu.Unlock()
		return true
	}
	return false
}

// cachedDiscover is the typed front end of discoveryCache.do.
func cachedDiscover[T any](c *discoveryCache, key discoveryKey, serverURL string, fn func() (T, error)) (T, error) {
	v, err := c.do(key, serverURL, func() (any, time.Duration, error) {
		val, err := fn()
		return val, 0, err
	})
	if v == nil {
		var zero T
		return zero, err
	}
	return v.(T), err
}

// InvalidateDiscovery drops every cached discovery result for serverURL.
func InvalidateDiscovery(serverURL string) {
	globalDiscoveryCache.invalidateServer(serverURL)
}

// maxASMetadataBytes bounds an authorization server metadata document.
const maxASMetadataBytes = 1 << 20

// asMetadataDoc is one fetched RFC 8414 document: the raw bytes (so the wrapper
// can serve every field mcp-go needs, not only the ones mcpproxy models) and
// the typed view the preflight reads scopes and grant types from.
type asMetadataDoc struct {
	raw  []byte
	meta *OAuthServerMetadata
}

func parseASMetadataDoc(raw []byte) (*asMetadataDoc, error) {
	var m OAuthServerMetadata
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("failed to parse metadata: %w", err)
	}
	return &asMetadataDoc{raw: raw, meta: &m}, nil
}

// httpFetchRaw GETs a metadata document with the OAuth redirect policy and a
// size bound. Only a 200 is a success.
func httpFetchRaw(metadataURL string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	c := &http.Client{Timeout: timeout, CheckRedirect: oauthCheckRedirect}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	return readBoundedDoc(resp.Body)
}

func readBoundedDoc(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxASMetadataBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata: %w", err)
	}
	if len(b) > maxASMetadataBytes {
		return nil, fmt.Errorf("metadata document exceeds %d bytes", maxASMetadataBytes)
	}
	return b, nil
}

// asDocKey is the cache key shared by the preflight and the transport wrapper,
// so one GET serves both.
func asDocKey(serverURL string, ov discoveryOverrides, metadataURL string) discoveryKey {
	return makeDiscoveryKey("as-doc", serverURL, ov, metadataURL)
}

// cachedASMetadataDoc returns the AS metadata document at metadataURL through
// the cache, fetching with fetch on a miss.
func cachedASMetadataDoc(c *discoveryCache, serverURL string, ov discoveryOverrides, metadataURL string, fetch func() ([]byte, error)) (*asMetadataDoc, error) {
	return cachedDiscover(c, asDocKey(serverURL, ov, metadataURL), serverURL, func() (*asMetadataDoc, error) {
		raw, err := fetch()
		if err != nil {
			return nil, err
		}
		return parseASMetadataDoc(raw)
	})
}

// oauthCheckRedirect is the redirect policy of every OAuth HTTP client
// (Spec 113 FR-027a): credentials (code, refresh token, client secret) are never
// re-sent in clear text or to another origin.
func oauthCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if len(via) == 0 {
		return nil
	}
	first := via[0]
	prev := via[len(via)-1]
	if strings.EqualFold(prev.URL.Scheme, "https") && strings.EqualFold(req.URL.Scheme, "http") &&
		!config.IsLoopbackHost(req.URL.Hostname()) {
		return errors.New("refusing OAuth redirect from https to http on a non-loopback host")
	}
	if first.Method == http.MethodPost && !sameOrigin(first.URL, req.URL) {
		return errors.New("refusing cross-origin redirect of an OAuth POST")
	}
	return nil
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}
