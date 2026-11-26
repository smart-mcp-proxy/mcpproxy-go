package progressrelay

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func progressNote(tok any, progress float64, total any, msg string) mcp.JSONRPCNotification {
	f := map[string]any{"progressToken": tok, "progress": progress}
	if total != nil {
		f["total"] = total
	}
	if msg != "" {
		f["message"] = msg
	}
	return mcp.JSONRPCNotification{
		JSONRPC: mcp.JSONRPC_VERSION,
		Notification: mcp.Notification{
			Method: string(mcp.MethodNotificationProgress),
			Params: mcp.NotificationParams{AdditionalFields: f},
		},
	}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }
func (c *clock) step()          { c.t = c.t.Add(MinInterval) }

func newRegistry() (*Registry, *clock) {
	c := &clock{t: time.Unix(1000, 0)}
	return &Registry{now: c.now}, c
}

func TestBegin_NoSinkRegistersNothing(t *testing.T) {
	r, _ := newRegistry()
	tok, end := r.Begin(context.Background())
	assert.Nil(t, tok)
	end()
	assert.Equal(t, 0, r.Len())
}

func TestDispatch_RoutesByProxyTokenOnly(t *testing.T) {
	r, clk := newRegistry()
	var a, b []Update
	tokA, endA := r.Begin(WithSink(context.Background(), func(u Update) bool { a = append(a, u); return true }))
	tokB, endB := r.Begin(WithSink(context.Background(), func(u Update) bool { b = append(b, u); return true }))
	defer endA()
	defer endB()
	require.NotEqual(t, tokA, tokB)

	assert.True(t, r.Dispatch(progressNote(tokA, 1, 10.0, "a")))
	clk.step()
	assert.True(t, r.Dispatch(progressNote(tokB, 5, nil, "")))
	// A token the proxy did not mint (e.g. the client's own) reaches nobody.
	assert.False(t, r.Dispatch(progressNote("client-token", 2, nil, "")))
	assert.False(t, r.Dispatch(progressNote(1, 2, nil, "")))

	require.Len(t, a, 1)
	require.Len(t, b, 1)
	assert.Equal(t, 1.0, a[0].Progress)
	require.NotNil(t, a[0].Total)
	assert.Equal(t, 10.0, *a[0].Total)
	assert.Equal(t, "a", a[0].Message)
	assert.Nil(t, b[0].Total)
}

func TestDispatch_TokensDifferAcrossRegistries(t *testing.T) {
	r1, _ := newRegistry()
	r2, _ := newRegistry()
	ctx := WithSink(context.Background(), func(Update) bool { return true })
	t1, e1 := r1.Begin(ctx)
	t2, e2 := r2.Begin(ctx)
	defer e1()
	defer e2()
	assert.NotEqual(t, t1, t2)
	assert.False(t, r2.Dispatch(progressNote(t1, 1, nil, "")))
}

func TestDispatch_AfterEndOrClearDropped(t *testing.T) {
	r, _ := newRegistry()
	n := 0
	ctx := WithSink(context.Background(), func(Update) bool { n++; return true })
	tok, end := r.Begin(ctx)
	end()
	assert.False(t, r.Dispatch(progressNote(tok, 1, nil, "")))
	assert.Equal(t, 0, r.Len())

	tok2, end2 := r.Begin(ctx)
	defer end2()
	r.Clear()
	assert.False(t, r.Dispatch(progressNote(tok2, 1, nil, "")))
	assert.Equal(t, 0, n)
}

func TestDispatch_AfterCallContextDoneDropped(t *testing.T) {
	r, _ := newRegistry()
	ctx, cancel := context.WithCancel(WithSink(context.Background(), func(Update) bool { t.Error("forwarded after the deadline"); return true }))
	tok, end := r.Begin(ctx)
	defer end()
	cancel()
	assert.False(t, r.Dispatch(progressNote(tok, 1, nil, "")))
}

func TestDispatch_RateLimitedAndMonotonic(t *testing.T) {
	r, clk := newRegistry()
	var got []float64
	tok, end := r.Begin(WithSink(context.Background(), func(u Update) bool { got = append(got, u.Progress); return true }))
	defer end()

	assert.True(t, r.Dispatch(progressNote(tok, 1, 10.0, "")))
	// Too soon: dropped.
	for i := 2; i < 50; i++ {
		assert.False(t, r.Dispatch(progressNote(tok, float64(i)/10, 10.0, "")))
	}
	clk.step()
	assert.True(t, r.Dispatch(progressNote(tok, 3, 10.0, "")))
	clk.step()
	// Not increasing: dropped.
	assert.False(t, r.Dispatch(progressNote(tok, 3, 10.0, "")))
	assert.False(t, r.Dispatch(progressNote(tok, 2, 10.0, "")))
	// Completion passes the interval.
	assert.True(t, r.Dispatch(progressNote(tok, 4, 10.0, "")))
	assert.True(t, r.Dispatch(progressNote(tok, 10, 10.0, "")))
	// The completion bypass is used once: a flood past total is limited.
	assert.False(t, r.Dispatch(progressNote(tok, 11, 10.0, "")))
	assert.False(t, r.Dispatch(progressNote(tok, 12, 10.0, "")))
	assert.Equal(t, []float64{1, 3, 4, 10}, got)
}

// An update the client could not take does not use up the interval or the
// completion bypass.
func TestDispatch_UnsentUpdateKeepsState(t *testing.T) {
	r, _ := newRegistry()
	accept := false
	var got []float64
	tok, end := r.Begin(WithSink(context.Background(), func(u Update) bool {
		if accept {
			got = append(got, u.Progress)
		}
		return accept
	}))
	defer end()
	assert.False(t, r.Dispatch(progressNote(tok, 10, 10.0, "")))
	accept = true
	assert.True(t, r.Dispatch(progressNote(tok, 10, 10.0, "")), "same progress again: nothing was sent before")
	assert.Equal(t, []float64{10}, got)
}

func TestDispatch_Malformed(t *testing.T) {
	r, _ := newRegistry()
	n := 0
	tok, end := r.Begin(WithSink(context.Background(), func(Update) bool { n++; return true }))
	defer end()
	bad := progressNote(tok, 1, nil, "")
	delete(bad.Params.AdditionalFields, "progress")
	assert.False(t, r.Dispatch(bad))
	bad.Params.AdditionalFields["progress"] = "1"
	assert.False(t, r.Dispatch(bad))
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		assert.False(t, r.Dispatch(progressNote(tok, v, nil, "")), "progress %v", v)
		assert.False(t, r.Dispatch(progressNote(tok, 1, v, "")), "total %v", v)
	}
	assert.Equal(t, 0, n)
}

func TestDispatch_OverlongMessageLeftOut(t *testing.T) {
	r, _ := newRegistry()
	var got []Update
	tok, end := r.Begin(WithSink(context.Background(), func(u Update) bool { got = append(got, u); return true }))
	defer end()
	require.True(t, r.Dispatch(progressNote(tok, 1, nil, strings.Repeat("x", MaxRawMessageBytes))))
	r.now = func() time.Time { return time.Unix(2000, 0) }
	require.True(t, r.Dispatch(progressNote(tok, 2, nil, strings.Repeat("x", MaxRawMessageBytes+1))))
	require.Len(t, got, 2)
	assert.Len(t, got[0].Message, MaxRawMessageBytes, "read whole, cut only after cleaning")
	assert.Empty(t, got[1].Message)
	assert.Equal(t, 2.0, got[1].Progress, "the numbers are still forwarded")
}

func TestCapMessage_RuneBoundary(t *testing.T) {
	long := "x" + strings.Repeat("é", MaxMessageBytes) // 2 bytes each
	got := CapMessage(long)
	assert.LessOrEqual(t, len(got), MaxMessageBytes)
	assert.True(t, strings.HasPrefix(got, "xé"))
	assert.True(t, strings.HasSuffix(got, "é"), "cut on a rune boundary")
}

// Two tokens of one registry share nothing an upstream could extend.
func TestBegin_TokensUnguessable(t *testing.T) {
	r, _ := newRegistry()
	ctx := WithSink(context.Background(), func(Update) bool { return true })
	t1, e1 := r.Begin(ctx)
	t2, e2 := r.Begin(ctx)
	defer e1()
	defer e2()
	s1, s2 := t1.(string), t2.(string)
	assert.Len(t, s1, len("mcpproxy-1-")+24)
	assert.NotEqual(t, s1[len(s1)-24:], s2[len(s2)-24:])
}

func TestSessionSink_NoTokenOrNoSession(t *testing.T) {
	assert.Nil(t, SessionSink(context.Background(), "t", nil))
	assert.Nil(t, SessionSink(context.Background(), nil, nil))
}
