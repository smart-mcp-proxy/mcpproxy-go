package truncate

import (
	"fmt"
	"strings"
	"testing"
)

// Two equally good record arrays must resolve to the same record path on
// every call, or identical upstream responses get cache handles paging
// different arrays.
func TestTruncate_TiedArraysPickStableRecordPath(t *testing.T) {
	row := func(i int) string { return fmt.Sprintf(`{"id":%d,"name":"%s"}`, i, strings.Repeat("x", 40)) }
	rows := make([]string, 20)
	for i := range rows {
		rows[i] = row(i)
	}
	arr := "[" + strings.Join(rows, ",") + "]"
	content := fmt.Sprintf(`{"zeta":%s,"alpha":%s,"mid":%s}`, arr, arr, arr)

	tr := NewTruncator(500)
	for i := 0; i < 50; i++ {
		res := tr.Truncate(content, "tool", nil)
		if !res.CacheAvailable {
			t.Fatalf("expected a cache handle, got simple truncation")
		}
		if res.RecordPath != "alpha" {
			t.Fatalf("call %d: record path %q, want %q", i, res.RecordPath, "alpha")
		}
	}
}
