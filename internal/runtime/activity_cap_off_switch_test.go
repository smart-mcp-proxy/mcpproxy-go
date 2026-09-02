package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// The 64KB per-record cap needs an escape hatch. Without one it permanently
// forecloses the token benchmark's measured-token basis for any response above
// it: bodies-on replay tokenizes the STORED text, and a cut body may not be
// tokenized as if whole. An operator running a --bodies=on-unmasked capture has
// to be able to say "store responses whole" and have it actually happen.
//
// The setter convention alone is not enough to deliver that. storage's
// truncateResponse reads a non-positive maxSize as "use the 64KB default" —
// behaviour its own table test pins by name — so a service that simply
// forwarded an explicit 0 would map it back onto 65536 and the documented
// setting would silently do nothing. That is worse than having no off switch:
// the docs promise measurement and the benchmark quietly keeps estimating.

// TestZeroCapStoresTheResponseWhole is the end-to-end half of the contract.
func TestZeroCapStoresTheResponseWhole(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := NewActivityService(store, zap.NewNop())
	svc.SetMaxResponseSize(0) // documented as "store responses whole"

	body := strings.Repeat("w", 200_000)
	svc.handleEvent(Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "upstream",
			"tool_name":   "huge_tool",
			"status":      "success",
			"response":    body,
		},
	})

	rec := onlyRecord(t, store)
	assert.Equal(t, body, rec.Response,
		"an explicit activity_max_response_size of 0 must store the response whole")
	assert.False(t, rec.ResponseStorageTruncated,
		"nothing was cut, so nothing may claim it was")
}

// And the cap must still bite when it is set, so the test above cannot pass by
// truncation having been disabled outright.
func TestNonZeroCapStillTruncates(t *testing.T) {
	store, cleanup := setupTestStorage(t)
	defer cleanup()

	svc := NewActivityService(store, zap.NewNop())
	svc.SetMaxResponseSize(1024)

	svc.handleEvent(Event{
		Type:      EventTypeActivityToolCallCompleted,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"server_name": "upstream",
			"tool_name":   "huge_tool",
			"status":      "success",
			"response":    strings.Repeat("w", 200_000),
		},
	})

	rec := onlyRecord(t, store)
	assert.LessOrEqual(t, len(rec.Response), 1024+len("...[truncated]"))
	assert.True(t, rec.ResponseStorageTruncated)
}

// The off switch is only safe because an ABSENT key is distinguishable from an
// explicit 0 on the path that feeds the setter: config.LoadFromFile unmarshals
// over DefaultConfig(), so a config that never mentions the setting arrives
// carrying 65536. If that default were ever dropped, every such install would
// silently lose its per-record bound.
func TestDefaultConfigKeepsTheCapOn(t *testing.T) {
	cfg := config.DefaultConfig()
	require.Equal(t, DefaultActivityMaxResponseSize, cfg.ActivityMaxResponseSize,
		"absence must arrive as the documented 64KB default, not as 0")

	svc := NewActivityService(nil, zap.NewNop())
	svc.SetMaxResponseSize(cfg.ActivityMaxResponseSize)
	assert.Equal(t, DefaultActivityMaxResponseSize, svc.maxResponseSize)
}
