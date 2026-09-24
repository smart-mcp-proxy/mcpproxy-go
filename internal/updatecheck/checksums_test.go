package updatecheck

import (
	"strings"
	"testing"
)

const (
	digestA = "1111111111111111111111111111111111111111111111111111111111111111"
	digestB = "2222222222222222222222222222222222222222222222222222222222222222"
)

func TestParseChecksums_RealManifestShape(t *testing.T) {
	manifest := strings.Join([]string{
		"# a comment line",
		digestA + "  mcpproxy-latest-darwin-arm64.tar.gz",
		digestB + " *mcpproxy-latest-windows-amd64.zip", // binary-mode marker
		"garbage line without a digest",
		"deadbeef  too-short-digest.txt",
		"",
	}, "\n")

	got, err := ParseChecksums(strings.NewReader(manifest))
	if err != nil {
		t.Fatalf("ParseChecksums: %v", err)
	}
	if got["mcpproxy-latest-darwin-arm64.tar.gz"] != digestA {
		t.Errorf("darwin entry = %q, want %q", got["mcpproxy-latest-darwin-arm64.tar.gz"], digestA)
	}
	if got["mcpproxy-latest-windows-amd64.zip"] != digestB {
		t.Errorf("windows entry = %q (the '*' binary-mode marker must be stripped)", got["mcpproxy-latest-windows-amd64.zip"])
	}
	if _, ok := got["too-short-digest.txt"]; ok {
		t.Error("a malformed digest must be skipped, not accepted")
	}
	if len(got) != 2 {
		t.Errorf("parsed %d entries, want 2: %v", len(got), got)
	}
}

// A name containing spaces must be keyed by the WHOLE name. Keying on the last
// whitespace-separated field would file "decoy mcpproxy-latest-darwin-arm64.tar.gz"
// under "mcpproxy-latest-darwin-arm64.tar.gz", i.e. report an entry for an asset
// the manifest never named.
func TestParseChecksums_NameWithSpacesIsNotMisattributed(t *testing.T) {
	const target = "mcpproxy-latest-darwin-arm64.tar.gz"
	manifest := digestA + "  decoy " + target + "\n"

	got, err := ParseChecksums(strings.NewReader(manifest))
	if err != nil {
		t.Fatalf("ParseChecksums: %v", err)
	}
	if _, ok := got[target]; ok {
		t.Fatalf("%q must NOT be present: the manifest only names %q", target, "decoy "+target)
	}
	if got["decoy "+target] != digestA {
		t.Errorf("entry = %v, want the full name %q to carry the digest", got, "decoy "+target)
	}
}

func TestParseChecksums_ConflictingDuplicateIsRefused(t *testing.T) {
	manifest := digestA + "  mcpproxy.tar.gz\n" + digestB + "  mcpproxy.tar.gz\n"

	if _, err := ParseChecksums(strings.NewReader(manifest)); err == nil {
		t.Fatal("two different digests for the same name must be an error, not last-one-wins")
	}

	// An identical duplicate is unambiguous and stays acceptable.
	same := digestA + "  mcpproxy.tar.gz\n" + digestA + "  mcpproxy.tar.gz\n"
	got, err := ParseChecksums(strings.NewReader(same))
	if err != nil {
		t.Fatalf("identical duplicate should parse: %v", err)
	}
	if got["mcpproxy.tar.gz"] != digestA {
		t.Errorf("entry = %q, want %q", got["mcpproxy.tar.gz"], digestA)
	}
}

// Everything after the two separator bytes is the name, verbatim. A text-mode
// line whose name happens to start with '*' or "./" must NOT be filed under the
// stripped spelling: doing so would report a digest for an asset the manifest
// never named, letting those bytes satisfy verification for a different name.
func TestParseChecksums_NameIsVerbatimAfterTheSeparator(t *testing.T) {
	const target = "mcpproxy-latest-darwin-arm64.tar.gz"
	manifest := strings.Join([]string{
		digestA + "  *" + target,  // text mode, name really starts with '*'
		digestB + "  ./" + target, // text mode, name really starts with './'
		"",
	}, "\n")

	got, err := ParseChecksums(strings.NewReader(manifest))
	if err != nil {
		t.Fatalf("ParseChecksums: %v", err)
	}
	if _, ok := got[target]; ok {
		t.Errorf("%q must NOT be present: neither line names it", target)
	}
	if got["*"+target] != digestA {
		t.Errorf("entries = %v, want %q under its verbatim name", got, "*"+target)
	}
	if got["./"+target] != digestB {
		t.Errorf("entries = %v, want %q under its verbatim name", got, "./"+target)
	}
}

// Binary mode is the one place a '*' is syntax: "<digest> *<name>" — a single
// separator space, then the mode character.
func TestParseChecksums_BinaryModeMarkerIsSyntax(t *testing.T) {
	got, err := ParseChecksums(strings.NewReader(digestA + " *mcpproxy.zip\n"))
	if err != nil {
		t.Fatalf("ParseChecksums: %v", err)
	}
	if got["mcpproxy.zip"] != digestA {
		t.Errorf("entries = %v, want mcpproxy.zip -> %s", got, digestA)
	}
}

// The separator coreutils emits is a literal space; a tab-separated line is
// one `sha256sum -c` rejects as improperly formatted, so it is not a checksum
// record and must not produce a name -> digest mapping.
func TestParseChecksums_TabSeparatorIsRejected(t *testing.T) {
	manifest := digestA + "\t mcpproxy-latest-darwin-arm64.tar.gz\n" + digestB + "  other.tar.gz\n"

	got, err := ParseChecksums(strings.NewReader(manifest))
	if err != nil {
		t.Fatalf("ParseChecksums: %v", err)
	}
	if _, ok := got["mcpproxy-latest-darwin-arm64.tar.gz"]; ok {
		t.Errorf("entries = %v, want the tab-separated line skipped", got)
	}
	if len(got) != 1 {
		t.Errorf("parsed %d entries, want only the well-formed one: %v", len(got), got)
	}
}

func TestParseChecksums_CRLFManifest(t *testing.T) {
	got, err := ParseChecksums(strings.NewReader(digestA + "  mcpproxy.tar.gz\r\n"))
	if err != nil {
		t.Fatalf("ParseChecksums: %v", err)
	}
	if got["mcpproxy.tar.gz"] != digestA {
		t.Errorf("entries = %v, want the CR stripped from the name", got)
	}
}

func TestParseChecksums_OversizedManifestIsRefused(t *testing.T) {
	// A manifest at the read cap may have been cut mid-line, which could parse
	// as a shorter (different) filename than the real one.
	var b strings.Builder
	b.WriteString(digestA + "  real-asset.tar.gz\n")
	for b.Len() <= maxChecksumsManifestBytes {
		b.WriteString(digestB + "  filler-asset-with-a-reasonably-long-name.tar.gz\n")
	}

	if _, err := ParseChecksums(strings.NewReader(b.String())); err == nil {
		t.Fatal("an over-cap manifest must be refused, not silently truncated")
	}
}

func TestParseChecksums_EmptyManifestIsAnError(t *testing.T) {
	if _, err := ParseChecksums(strings.NewReader("# only comments\n")); err == nil {
		t.Fatal("expected an error for a manifest with no usable entries")
	}
}

// coreutils writes a leading backslash before the digest when the name needed
// escaping, and escapes exactly backslash, newline and carriage return.
func TestParseChecksums_EscapedNames(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantName string
		wantSkip bool
	}{
		{name: "backslash", line: `\` + digestA + `  weird\\name.tar.gz`, wantName: `weird\name.tar.gz`},
		{name: "newline", line: `\` + digestA + `  weird\nname.tar.gz`, wantName: "weird\nname.tar.gz"},
		{name: "carriage return", line: `\` + digestA + `  weird\rname.tar.gz`, wantName: "weird\rname.tar.gz"},
		// Sequences coreutils never emits: the real name is unknowable, so the
		// line is dropped rather than decoded into a name nobody published.
		{name: "unknown escape", line: `\` + digestA + `  weird\qname.tar.gz`, wantSkip: true},
		{name: "trailing backslash", line: `\` + digestA + `  weirdname.tar.gz\`, wantSkip: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A second, ordinary entry keeps the manifest non-empty so a skipped
			// line shows up as a missing key rather than a parse error.
			manifest := tt.line + "\n" + digestB + "  other.tar.gz\n"
			got, err := ParseChecksums(strings.NewReader(manifest))
			if err != nil {
				t.Fatalf("ParseChecksums: %v", err)
			}
			if tt.wantSkip {
				if len(got) != 1 {
					t.Fatalf("entries = %v, want only the ordinary entry", got)
				}
				return
			}
			if got[tt.wantName] != digestA {
				t.Errorf("entries = %v, want %q -> %s", got, tt.wantName, digestA)
			}
		})
	}
}
