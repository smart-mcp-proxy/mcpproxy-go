package clientidentity

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFromRawKeepsTransformedAndFormerSuffixLookalikeDistinct(t *testing.T) {
	transformed := FromRaw("\x1bzed")
	digest := transformed.Key[strings.LastIndexByte(transformed.Key, ':')+1:]
	ordinary := FromRaw("zed-" + digest)
	sentinelLookalike := FromRaw(transformed.PublicID)
	transformedWithSentinelDisplay := FromRaw("\x1b~zed")
	sentinelDisplayLookalike := FromRaw(transformedWithSentinelDisplay.PublicID)

	require.NotEqual(t, transformed.Key, ordinary.Key, "serialized presence keys must not merge a transformed name with its printable lookalike")
	require.NotEqual(t, transformed.PublicID, ordinary.PublicID, "public client IDs must not merge a transformed name with its printable lookalike")
	require.Equal(t, "zed-"+digest, ordinary.PublicID, "ordinary printable names retain their readable ID")
	require.NotEqual(t, transformed.PublicID, sentinelLookalike.PublicID, "an ordinary name matching a transformed public ID must be escaped")
	require.Equal(t, "~p:"+transformed.PublicID, sentinelLookalike.PublicID)
	require.NotEqual(t, transformedWithSentinelDisplay.PublicID, sentinelDisplayLookalike.PublicID, "a transformed display beginning with the sentinel must not recreate an escaped ordinary ID")
}

func TestAliasCandidateStripsOneMCPRemoteBridgeSuffix(t *testing.T) {
	cases := map[string]string{
		"claude-ai (via mcp-remote 0.15.2)":               "claude-ai",
		" Claude-AI (via mcp-remote 0.1.29) ":             "claude-ai",
		"claude-ai (via mcp-remote)":                      "claude-ai",
		"codex-mcp-client":                                "codex-mcp-client",
		"Cursor":                                          "cursor",
		"claude-ai (via something-else 1.0)":              "claude-ai (via something-else 1.0)",
		"claude-ai (via mcp-remote 1) (via mcp-remote 2)": "claude-ai (via mcp-remote 1)",
		"(via mcp-remote 0.15.2)":                         "(via mcp-remote 0.15.2)",
		"claude-ai(via mcp-remote 0.15.2)":                "claude-ai(via mcp-remote 0.15.2)",
		"claude-ai (via mcp-remote 0.15.2) x":             "claude-ai (via mcp-remote 0.15.2) x",
	}
	for raw, want := range cases {
		require.Equal(t, want, AliasCandidate(raw), raw)
	}
	// Controls survive, so an escape-prefixed bridged name cannot pass as a
	// supported alias once the suffix is removed.
	require.Equal(t, "\x1bclaude-ai", AliasCandidate("\x1bclaude-ai (via mcp-remote 0.15.2)"))
}
