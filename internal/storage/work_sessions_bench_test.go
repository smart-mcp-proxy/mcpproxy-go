package storage

import (
	"fmt"
	"os"
	"testing"
	"time"

	"go.uber.org/zap"
)

// Spec 124 SC-006: the session list and one timeline page are served from the
// summaries and the per-session index, so their cost follows the number of
// sessions and the size of the page, not the size of the activity log. Each
// benchmark runs against two log sizes; compare the two ns/op.

func benchStore(b *testing.B) *Manager {
	b.Helper()
	dir, err := os.MkdirTemp("", "ws_bench_*")
	if err != nil {
		b.Fatal(err)
	}
	m, err := NewManager(dir, zap.NewNop().Sugar())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = m.Close()
		_ = os.RemoveAll(dir)
	})
	return m
}

// seedSessions writes `sessions` work sessions of `rowsEach` rows, plus
// `noise` rows that belong to no session (the rest of the log).
func seedSessions(b *testing.B, m *Manager, sessions, rowsEach, noise int) {
	b.Helper()
	base := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	n := 0
	save := func(ws string, failed bool) {
		n++
		status := ActivityStatusSuccess
		if failed {
			status = ActivityStatusError
		}
		if err := m.SaveActivity(&ActivityRecord{
			Type: ActivityTypeToolCall, Status: status, ServerName: "github", ToolName: "list_issues",
			Timestamp: base.Add(time.Duration(n) * time.Millisecond), WorkSessionID: ws, TokenName: "ci", TokenPrefix: "mcp_agt_aaaa",
		}); err != nil {
			b.Fatal(err)
		}
	}
	for s := 0; s < sessions; s++ {
		for r := 0; r < rowsEach; r++ {
			save(fmt.Sprintf("ws-%05d", s), r == rowsEach/2)
		}
	}
	for i := 0; i < noise; i++ {
		save("", false)
	}
}

func BenchmarkWorkSessionList1000(b *testing.B) {
	for _, noise := range []int{0, 20000} {
		b.Run(fmt.Sprintf("log+%d", noise), func(b *testing.B) {
			m := benchStore(b)
			seedSessions(b, m, 1000, 2, noise)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sums, err := m.ListWorkSessionSummaries()
				if err != nil || len(sums) != 1000 {
					b.Fatalf("got %d summaries, err %v", len(sums), err)
				}
			}
		})
	}
}

func BenchmarkWorkSessionTimelinePage(b *testing.B) {
	for _, noise := range []int{0, 50000} {
		b.Run(fmt.Sprintf("log+%d", noise), func(b *testing.B) {
			m := benchStore(b)
			seedSessions(b, m, 1, 50000, noise)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				refs, err := m.WorkSessionRowRefs("ws-00000")
				if err != nil || len(refs) != 50000 {
					b.Fatalf("got %d refs, err %v", len(refs), err)
				}
				// One page of 41 rows around the failure.
				at := 25000
				keys := make([][]byte, 0, 41)
				for _, ref := range refs[at-20 : at+21] {
					keys = append(keys, ref.ActivityKey)
				}
				rows, err := m.GetActivitiesByKeys(keys)
				if err != nil || len(rows) != 41 || rows[20] == nil {
					b.Fatalf("page read failed: %v", err)
				}
			}
		})
	}
}
