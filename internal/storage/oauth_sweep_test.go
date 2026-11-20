package storage

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 T014 (FR-016): the boot sweep seals every plaintext row in one
// transaction, collecting before writing, and leaves sealed and
// undecryptable rows alone.

const sweepRows = 400 // ~1 KiB each: spans many leaf pages

func newSweepManager(t *testing.T, k *tokenseal.Key) *Manager {
	t.Helper()
	m, err := NewManager(t.TempDir(), zap.NewNop().Sugar(), WithOAuthTokenKey(k))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

type sweepFixture struct {
	plainKeys []string
	untouched map[string][]byte // sealed and undecryptable rows
}

func seedSweep(t *testing.T, m *Manager, k *tokenseal.Key) sweepFixture {
	t.Helper()
	f := sweepFixture{untouched: map[string][]byte{}}
	pad := strings.Repeat("x", 1024)
	for i := 0; i < sweepRows; i++ {
		key := fmt.Sprintf("srv%03d_0123456789abcdef", i)
		b, err := (&OAuthTokenRecord{ServerName: key, AccessToken: "at-" + pad, RefreshToken: fmt.Sprintf("rt-%d", i)}).MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		putRawOAuthValue(t, m.GetDB(), key, b)
		f.plainKeys = append(f.plainKeys, key)
	}
	other := sealTestKey(t, 50)
	if k != nil {
		f.untouched["sealed_0123456789abcdef"] = sealedValue(t, k, "sealed_0123456789abcdef", &OAuthTokenRecord{ServerName: "sealed_0123456789abcdef", AccessToken: "s"})
	}
	f.untouched["otherkey_0123456789abcdef"] = sealedValue(t, other, "otherkey_0123456789abcdef", &OAuthTokenRecord{ServerName: "otherkey_0123456789abcdef"})
	f.untouched["badjson_0123456789abcdef"] = []byte("{broken")
	for key, v := range f.untouched {
		putRawOAuthValue(t, m.GetDB(), key, v)
	}
	return f
}

func classCounts(t *testing.T, m *Manager) map[OAuthRowClass]int {
	t.Helper()
	rows, err := m.ListOAuthTokenRows()
	if err != nil {
		t.Fatal(err)
	}
	c := map[OAuthRowClass]int{}
	for _, r := range rows {
		c[r.Class]++
	}
	return c
}

func TestOAuthSweep_SealsEveryPlaintextRow(t *testing.T) {
	k := sealTestKey(t, 1)
	m := newSweepManager(t, k)
	f := seedSweep(t, m, k)

	n, err := m.SealPlaintextOAuthTokens(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != sweepRows {
		t.Fatalf("sealed %d, want %d", n, sweepRows)
	}
	c := classCounts(t, m)
	if c[OAuthRowPlaintext] != 0 || c[OAuthRowSealed] != sweepRows+1 || c[OAuthRowUndecryptable] != 2 {
		t.Fatalf("after sweep: %v", c)
	}
	for _, key := range f.plainKeys {
		raw := rawOAuthValue(t, m.GetDB(), key)
		if shape, kid := tokenseal.Classify(raw); shape != tokenseal.ShapeSealedV1 || kid != k.ID() {
			t.Fatalf("%s: shape %v kid %q", key, shape, kid)
		}
		rec, err := m.GetOAuthToken(key)
		if err != nil || rec.ServerName != key || !strings.HasPrefix(rec.AccessToken, "at-x") {
			t.Fatalf("%s did not survive the sweep: %+v, %v", key, rec, err)
		}
	}
	for key, v := range f.untouched {
		if got := rawOAuthValue(t, m.GetDB(), key); !bytes.Equal(got, v) {
			t.Fatalf("sweep touched %s", key)
		}
	}
	if compactAdvised(t, m.GetDB()) == "" {
		t.Fatal("sweep sealed rows but did not set the compaction marker")
	}

	// A second sweep finds nothing and does not set the marker again.
	clearCompactAdvised(t, m.GetDB())
	if n, err := m.SealPlaintextOAuthTokens(context.Background()); err != nil || n != 0 {
		t.Fatalf("second sweep: n=%d err=%v", n, err)
	}
	if v := compactAdvised(t, m.GetDB()); v != "" {
		t.Fatalf("marker %q set by a sweep that sealed nothing", v)
	}
}

func TestOAuthSweep_OneTransaction(t *testing.T) {
	k := sealTestKey(t, 1)
	m := newSweepManager(t, k)
	f := seedSweep(t, m, k)
	before := map[string][]byte{}
	for _, key := range f.plainKeys {
		before[key] = rawOAuthValue(t, m.GetDB(), key)
	}

	writes := 0
	putOAuthRecordHook = func(string) error {
		writes++
		if writes == 2 {
			return fmt.Errorf("injected write failure")
		}
		return nil
	}
	t.Cleanup(func() { putOAuthRecordHook = nil })

	if _, err := m.SealPlaintextOAuthTokens(context.Background()); err == nil {
		t.Fatal("injected failure not reported")
	}
	putOAuthRecordHook = nil
	for key, v := range before {
		if got := rawOAuthValue(t, m.GetDB(), key); !bytes.Equal(got, v) {
			t.Fatalf("%s changed although the sweep transaction failed", key)
		}
	}
	if c := classCounts(t, m); c[OAuthRowPlaintext] != sweepRows {
		t.Fatalf("after a failed sweep: %v", c)
	}
	if v := compactAdvised(t, m.GetDB()); v != "" {
		t.Fatalf("marker %q survived the rolled-back sweep", v)
	}
}

func TestOAuthSweep_NoKeyIsNoOp(t *testing.T) {
	m := newSweepManager(t, nil)
	seedSweep(t, m, nil)
	n, err := m.SealPlaintextOAuthTokens(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("sweep without a key: n=%d err=%v", n, err)
	}
	if c := classCounts(t, m); c[OAuthRowPlaintext] != sweepRows {
		t.Fatalf("rows changed without a key: %v", c)
	}
	if v := compactAdvised(t, m.GetDB()); v != "" {
		t.Fatalf("marker %q set without a key", v)
	}
}
