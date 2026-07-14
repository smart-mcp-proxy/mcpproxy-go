package toonenc

import "testing"

func TestParseMode(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   Mode
		wantOK bool
	}{
		{name: "empty string is off", input: "", want: ModeOff, wantOK: true},
		{name: "off", input: "off", want: ModeOff, wantOK: true},
		{name: "adaptive", input: "adaptive", want: ModeAdaptive, wantOK: true},
		{name: "always", input: "always", want: ModeAlways, wantOK: true},
		{name: "invalid value", input: "bogus", want: "", wantOK: false},
		{name: "case sensitive", input: "Adaptive", want: "", wantOK: false},
		{name: "whitespace not trimmed", input: " off", want: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseMode(tt.input)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("ParseMode(%q) = (%q, %v), want (%q, %v)", tt.input, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestModeConstants(t *testing.T) {
	if ModeOff != "off" || ModeAdaptive != "adaptive" || ModeAlways != "always" {
		t.Fatalf("mode constants must match config enum strings: got %q %q %q", ModeOff, ModeAdaptive, ModeAlways)
	}
}
