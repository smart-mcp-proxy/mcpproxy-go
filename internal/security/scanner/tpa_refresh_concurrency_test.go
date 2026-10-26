package scanner

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// T329 (Spec 087 FR-010, SC-006): scans are never blocked and never see a
// half-swapped corpus while the fetch leg hangs or an activation runs a slow
// self-check.

// scanTools are two tools: A matches only the OLD bundle's marker, B only the
// NEW one's. A scan against exactly one bundle flags exactly one of them, so a
// torn read (both or neither flagged) is detectable.
func scanTools() []*config.ToolMetadata {
	return []*config.ToolMetadata{
		{Name: "tool_a", Description: "carries bundle-marker-2-old"},
		{Name: "tool_b", Description: "carries bundle-marker-3-new"},
	}
}

// scanGeneration says which bundle a scan ran against: "old", "new", or a
// description of the torn result.
func scanGeneration() (gen string, took time.Duration) {
	start := time.Now()
	_, findings, _ := ScanToolMetadataVerdict("srv", scanTools(), nil)
	took = time.Since(start)
	var a, b bool
	for _, f := range findings {
		switch {
		case strings.HasSuffix(f.Location, ":tool_a"):
			a = true
		case strings.HasSuffix(f.Location, ":tool_b"):
			b = true
		}
	}
	switch {
	case a && !b:
		return "old", took
	case b && !a:
		return "new", took
	}
	return "torn(a=" + boolStr(a) + ",b=" + boolStr(b) + ")", took
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func median(ds []time.Duration) time.Duration {
	return percentile(ds, 0.5)
}

// percentile returns the q-quantile (0 < q <= 1) of ds. The latency guards
// below bound p90 relative to the baseline rather than the single worst scan:
// one GC pause or a descheduled goroutine on a loaded CI runner makes one
// scan take 100 ms without saying anything about the lock the guard is
// about. The single worst scan keeps an absolute ceiling (scanWorstCeiling)
// instead: a scan that really waited on the refresh waits for the gate or a
// multi-second fetch deadline, which no scheduler hiccup reaches.
// scanWorstCeiling is the absolute bound on any single scan while a refresh
// is blocked: well above a scheduler stall, well below the shortest wait a
// scan could spend behind the refresh (the test gate, or a fetch deadline).
const scanWorstCeiling = 2 * time.Second

func percentile(ds []time.Duration, q float64) time.Duration {
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	i := int(float64(len(s)) * q)
	if i >= len(s) {
		i = len(s) - 1
	}
	return s[i]
}

// scanLoop runs n scans, returning the generations seen and the slowest scan.
func scanLoop(n int) (gens map[string]int, worst time.Duration, durations []time.Duration) {
	gens = map[string]int{}
	for i := 0; i < n; i++ {
		g, took := scanGeneration()
		gens[g]++
		durations = append(durations, took)
		if took > worst {
			worst = took
		}
	}
	return gens, worst, durations
}

func TestRefreshConcurrency_ScansIgnoreABlackholedFetchLeg(t *testing.T) {
	ch := newTestChannel(t)
	f := newChannelFixture(t, ch)
	old := fetchedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "old"})
	old = rebundleMarker(t, old, "bundle-marker-2-old")
	require.Equal(t, digestOf(old.Bundle), runOffered(old, f.log).FullDigest)

	_, _, baseline := scanLoop(100)
	base := median(baseline)

	reached := make(chan struct{})
	var once sync.Once
	ch.handle(releasesPath, func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(reached) })
		<-r.Context().Done() // the channel accepts the request and never answers
	})
	h := newRefresherHarness(t, f, nil)
	h.start()
	h.clock.loop(t).Fire()
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the fetch leg never reached the channel")
	}

	gens, worst, during := scanLoop(300)

	assert.Equal(t, map[string]int{"old": 300}, gens, "every scan runs against the serving (old) bundle")
	if !raceDetectorEnabled {
		assert.Less(t, percentile(during, 0.9), 50*base+50*time.Millisecond, "a hung fetch must not slow scans (baseline median %s)", base)
		assert.Less(t, worst, scanWorstCeiling, "no scan waited behind the hung fetch")
	}
}

func TestRefreshConcurrency_ScansDuringASlowSelfCheckSeeOldOrNew(t *testing.T) {
	f := newFetchFixture(t, nil)
	old := rebundleMarker(t, fetchedCandidate(t, keyOperator, bundleOpts{Seq: 2, Tag: "old"}), "bundle-marker-2-old")
	require.Equal(t, digestOf(old.Bundle), runOffered(old, f.log).FullDigest)
	_, _, baseline := scanLoop(100)
	base := median(baseline)

	entered := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	withSelfCheck(t, func(*BundleCheck) error {
		once.Do(func() { close(entered) })
		<-gate // a deliberately slow activation self-check, bundleRT.mu held
		return nil
	})
	newer := rebundleMarker(t, fetchedCandidate(t, keyOperator, bundleOpts{Seq: 3, Tag: "new"}), "bundle-marker-3-new")
	done := make(chan BundleInfo, 1)
	go func() { info, _ := offerCandidate(newer, f.log); done <- info }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the activation never reached its self-check")
	}

	// While the activation is mid-flight every scan still sees the old bundle.
	during, worst, took := scanLoop(300)
	assert.Equal(t, map[string]int{"old": 300}, during)
	if !raceDetectorEnabled {
		assert.Less(t, percentile(took, 0.9), 50*base+50*time.Millisecond, "a slow self-check must not slow scans (baseline median %s)", base)
		assert.Less(t, worst, scanWorstCeiling, "no scan waited behind the self-check")
	}

	// Scans racing the swap itself see exactly the old or the new bundle.
	stop := make(chan struct{})
	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				g, _ := scanGeneration()
				mu.Lock()
				seen[g]++
				mu.Unlock()
			}
		}()
	}
	close(gate)
	select {
	case info := <-done:
		assert.Equal(t, digestOf(newer.Bundle), info.FullDigest)
	case <-time.After(10 * time.Second):
		t.Fatal("the activation never completed")
	}
	// Wait for a scanner to observe the swap rather than for a fixed time: under
	// -race on a loaded CI runner one scan can take longer than any short sleep.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return seen["new"] > 0
	}, 10*time.Second, 5*time.Millisecond, "scans after the swap see the new bundle")
	close(stop)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	for gen := range seen {
		assert.Contains(t, []string{"old", "new"}, gen, "a scan saw a torn corpus")
	}
	assert.Positive(t, seen["new"], "scans after the swap see the new bundle")
	gen, _ := scanGeneration()
	assert.Equal(t, "new", gen)
}

// rebundleMarker re-renders c's bundle with pattern as its single rule and
// re-signs it, so each generation flags a different tool.
func rebundleMarker(t *testing.T, c Candidate, pattern string) Candidate {
	t.Helper()
	b := []byte(strings.Replace(string(c.Bundle), extractMarker(t, c.Bundle), pattern, 1))
	c.Bundle = b
	c.Sidecar = signBundle(t, keyOperator.priv, b)
	return c
}

func extractMarker(t *testing.T, bundle []byte) string {
	t.Helper()
	s := string(bundle)
	i := strings.Index(s, "bundle-marker-")
	require.GreaterOrEqual(t, i, 0)
	j := strings.Index(s[i:], `"`)
	require.Greater(t, j, 0)
	return s[i : i+j]
}
