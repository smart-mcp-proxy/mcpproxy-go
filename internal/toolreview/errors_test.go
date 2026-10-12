package toolreview

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestStaleToolReviewError_Message(t *testing.T) {
	e := NewStaleToolReviewError("notes2", []string{"read_note", "a", "read_note"}, []string{"drop_db"}, nil)
	if e == nil {
		t.Fatal("expected an error")
	}
	if got := e.Changed; len(got) != 2 || got[0] != "a" || got[1] != "read_note" {
		t.Fatalf("changed not sorted/deduplicated: %v", got)
	}
	msg := e.Error()
	for _, want := range []string{"out of date", "notes2", "definition changed since review: a, read_note", "not in the review: drop_db", "fetch the review again"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	if strings.Contains(strings.ToLower(msg), "dangerous") {
		t.Errorf("message must never contain \"dangerous\": %q", msg)
	}
	var target *StaleToolReviewError
	if !errors.As(fmt.Errorf("wrapped: %w", e), &target) || target != e {
		t.Error("errors.As must find the error through wrapping")
	}
}

func TestStaleToolReviewError_NilWhenEmpty(t *testing.T) {
	if e := NewStaleToolReviewError("s", nil, []string{}, nil); e != nil {
		t.Fatalf("expected nil, got %v", e)
	}
	if e := NewStaleToolReviewError("s", nil, nil, []string{"gone"}); e == nil || !strings.Contains(e.Error(), "block target no longer exists: gone") {
		t.Fatalf("missing-block message wrong: %v", e)
	}
}
