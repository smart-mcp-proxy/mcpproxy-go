package storage

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// Spec 123 FR-016: the boot sweep names the bucket key and the reason for
// every plaintext row it skips, and never logs the row value.
func TestOAuthSweep_WarnsForEverySkippedPlaintextRow(t *testing.T) {
	k := sealTestKey(t, 1)
	core, logs := observer.New(zapcore.WarnLevel)
	m, err := NewManager(t.TempDir(), zap.New(core).Sugar(), WithOAuthTokenKey(k))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })

	const badKey = "badjson_0123456789abcdef"
	putRawOAuthValue(t, m.GetDB(), badKey, []byte(`{"access_token":"SWEEP-SECRET-MALFORMED", broken`))
	const bigKey = "big_0123456789abcdef"
	putRawOAuthValue(t, m.GetDB(), bigKey,
		[]byte(`{"server_name":"`+bigKey+`","access_token":"SWEEP-SECRET-BIG`+strings.Repeat("x", 1100*1024)+`"}`))

	if _, err := m.SealPlaintextOAuthTokens(context.Background()); err != nil {
		t.Fatal(err)
	}

	reasons := map[string]string{}
	for _, e := range logs.All() {
		f := e.ContextMap()
		if strings.Contains(fmt.Sprint(e.Message, f), "SWEEP-SECRET") {
			t.Fatalf("a row value reached the log: %v %v", e.Message, f)
		}
		key, _ := f["server_key"].(string)
		reason, _ := f["reason"].(string)
		reasons[key] = reason
	}
	if reasons[badKey] != "malformed" {
		t.Fatalf("malformed row: reason %q, want malformed (logged: %v)", reasons[badKey], reasons)
	}
	if reasons[bigKey] != "too_large" {
		t.Fatalf("oversized row: reason %q, want too_large (logged: %v)", reasons[bigKey], reasons)
	}
}
