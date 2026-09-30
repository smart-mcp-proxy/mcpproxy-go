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
