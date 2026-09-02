package config

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// docs/configuration.md's table of contents drifted twice: sections were added
// to the body (Routing Mode, Tool Response Mode, Direct Tool Response Mode,
// Server Instructions, Tool-Level Quarantine) without a TOC entry, and a later
// pass renumbered only the tail, so the numbers stopped matching document order
// in the middle. A hand-maintained TOC drifts by default; this makes it a build
// failure instead.
//
// It checks ORDER and COVERAGE, not formatting: every `## ` heading below the
// TOC must appear, exactly once, in document order, with a numbering that
// counts from 1 and an anchor GitHub would actually resolve.

var (
	tocEntryRE = regexp.MustCompile(`^(\d+)\.\s+\[([^\]]+)\]\(#([^)]+)\)\s*$`)
	headingRE  = regexp.MustCompile(`^##\s+(.+?)\s*$`)
	nonAnchor  = regexp.MustCompile(`[^a-z0-9\- ]`)
)

// githubAnchor mirrors GitHub's heading-slug rules for the subset this document
// uses: lowercase, strip punctuation, spaces to hyphens. "TLS/HTTPS
// Configuration" becomes "tlshttps-configuration".
func githubAnchor(heading string) string {
	s := strings.ToLower(heading)
	s = nonAnchor.ReplaceAllString(s, "")
	return strings.ReplaceAll(s, " ", "-")
}

func TestConfigurationDocTOCMatchesDocumentOrder(t *testing.T) {
	raw, err := os.ReadFile("../../docs/configuration.md")
	require.NoError(t, err)
	lines := strings.Split(string(raw), "\n")

	var (
		toc      []string
		anchors  []string
		headings []string
		inTOC    bool
	)
	for _, line := range lines {
		if h := headingRE.FindStringSubmatch(line); h != nil {
			if h[1] == "Table of Contents" {
				inTOC = true
				continue
			}
			inTOC = false
			headings = append(headings, h[1])
			continue
		}
		if !inTOC {
			continue
		}
		if e := tocEntryRE.FindStringSubmatch(line); e != nil {
			require.Equal(t, len(toc)+1, mustAtoi(t, e[1]),
				"TOC entry %q is numbered %s but is item %d in the list", e[2], e[1], len(toc)+1)
			toc = append(toc, e[2])
			anchors = append(anchors, e[3])
		}
	}

	require.NotEmpty(t, headings, "the document must have ## sections to check")
	require.NotEmpty(t, toc, "the Table of Contents must have entries")

	assert.Equal(t, headings, toc,
		"every ## section must appear in the TOC, once, in document order")

	for i, anchor := range anchors {
		if i >= len(toc) {
			break
		}
		assert.Equal(t, githubAnchor(toc[i]), anchor,
			"TOC entry %q links to #%s, which does not resolve to its own heading", toc[i], anchor)
	}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, r := range s {
		require.True(t, r >= '0' && r <= '9', "not a number: %q", s)
		n = n*10 + int(r-'0')
	}
	return n
}
