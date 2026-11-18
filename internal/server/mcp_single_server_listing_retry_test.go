package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// fakeDirectDiscovery answers the direct rebuild's discovery: while failing,
// the named server is connected but its tools/list fails, so its tools are
// missing and it is reported as unlisted.
type fakeDirectDiscovery struct {
	mu      sync.Mutex
	tools   []*config.ToolMetadata
	failing string
	calls   int
}

func (f *fakeDirectDiscovery) discover(context.Context) ([]*config.ToolMetadata, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failing == "" {
		return f.tools, nil, nil
	}
	var out []*config.ToolMetadata
	for _, t := range f.tools {
		if t.ServerName != f.failing {
			out = append(out, t)
		}
	}
	return out, []string{f.failing}, nil
}

func (f *fakeDirectDiscovery) setFailing(name string) {
	f.mu.Lock()
	f.failing = name
	f.mu.Unlock()
}

func (f *fakeDirectDiscovery) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// Release QA (GH #1929 integration 2965f50b2): a catalog rebuild that ran while
// a CONNECTED server's tools/list failed transiently (its OAuth token refresh
// was failing) published the catalog without that server's tools, and nothing
// republished it once the server recovered on the same connection: /mcp/s/<server>
// listed only describe_tool and its tools answered "tool not found" while
// /mcp could still call them. Reproduced on main 6370f1f45. The rebuild must
// be retried while a connected server's listing fails, so the surface comes
// back on its own.
func TestSingleServerSurface_RecoversAfterATransientListingFailure(t *testing.T) {
	p := newDirectDescribeProxy(t)
	require.NotNil(t, p.directServer, "direct mode is initialised")
	fake := &fakeDirectDiscovery{tools: describeDirectFixture()}
	p.directDiscovery = fake.discover
	p.directRetryBase, p.directRetryMax = 20*time.Millisecond, 80*time.Millisecond

	p.RefreshDirectModeTools()
	s := newSingleServerSurfaceForTest(t, p, "github")
	require.Contains(t, listSingleServerTools(t, s, singleServerAdminCtx()), "read_file")

	// A rebuild runs while github's listing fails: its tools leave the surface.
	fake.setFailing("github")
	p.RefreshDirectModeTools()
	require.NotContains(t, listSingleServerTools(t, s, singleServerAdminCtx()), "read_file",
		"the failed listing is reflected (nothing stale is served)")

	// github recovers on the same connection; no servers.changed follows.
	fake.setFailing("")
	require.Eventually(t, func() bool {
		_, ok := listSingleServerTools(t, s, singleServerAdminCtx())["read_file"]
		return ok
	}, 5*time.Second, 10*time.Millisecond, "the retried rebuild republishes the server's tools")

	// Once every connected server listed, no further retry is scheduled.
	settled := fake.callCount()
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, settled, fake.callCount(), "no retry once the listing succeeded")
}

// While the listing keeps failing the retries back off (base, doubling, capped)
// instead of polling, and closing the proxy stops them.
func TestDirectRebuildRetry_BacksOffAndStopsOnClose(t *testing.T) {
	p := newDirectDescribeProxy(t)
	fake := &fakeDirectDiscovery{tools: describeDirectFixture(), failing: "github"}
	p.directDiscovery = fake.discover
	p.directRetryBase, p.directRetryMax = 20*time.Millisecond, 80*time.Millisecond

	p.RefreshDirectModeTools()
	time.Sleep(500 * time.Millisecond)
	calls := fake.callCount()
	// 1 + retries at ~20, 40, 80, 80, 80… ms: well under one per 20 ms.
	assert.GreaterOrEqual(t, calls, 3, "it retries")
	assert.LessOrEqual(t, calls, 10, "with backoff: %d calls in 500ms", calls)

	require.NoError(t, p.Close())
	after := fake.callCount()
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, after, fake.callCount(), "no retry after Close")
}
