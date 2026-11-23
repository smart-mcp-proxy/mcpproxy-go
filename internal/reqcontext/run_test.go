package reqcontext

import (
	"context"
	"strings"
	"testing"
)

func TestValidRunName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"plain", "nightly-2026-11-15", true},
		{"spaces and unicode", "ночной прогон 7", true},
		{"max length", strings.Repeat("a", MaxRunNameLen), true},
		{"max length counted in characters", strings.Repeat("ж", MaxRunNameLen), true},
		{"too long", strings.Repeat("a", MaxRunNameLen+1), false},
		{"empty", "", false},
		{"newline", "a\nb", false},
		{"tab", "a\tb", false},
		{"nul", "a\x00b", false},
		{"invalid utf8", "a\xffb", false},
	}
	for _, tc := range cases {
		if got := ValidRunName(tc.in); got != tc.want {
			t.Errorf("%s: ValidRunName(%q) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestRunHeaderAndSurfaceRoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, ok := RunHeader(ctx); ok {
		t.Fatal("no header was stored")
	}
	if _, ok := RunHeader(WithRunHeader(ctx, "")); ok {
		t.Fatal("an empty header is absent")
	}
	if raw, ok := RunHeader(WithRunHeader(ctx, "bad\nname")); !ok || raw != "bad\nname" {
		t.Fatalf("the raw value must be kept for the reader to judge, got %q %v", raw, ok)
	}
	if got := ClientSurface(WithClientSurface(ctx, "cli")); got != "cli" {
		t.Fatalf("surface = %q", got)
	}
	if got := AttemptID(WithAttemptID(ctx, "req-1")); got != "req-1" {
		t.Fatalf("attempt id = %q", got)
	}
	if AttemptID(ctx) != "" || ClientSurface(ctx) != "" {
		t.Fatal("unset values read as empty")
	}
}
