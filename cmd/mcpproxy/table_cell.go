package main

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/stringutil"
)

// truncateCell bounds one CLI table cell to maxLen bytes, marking a shortened
// value with an ellipsis. It is the single cut used by every table in this
// package: `shorten` (profile), `truncateStr` (registry, catalog) and two
// inline `s[:n] + "..."` sites (upstream tool list, token list) each carried
// an independent copy of the same arithmetic, and all of them cut at a raw
// byte offset.
//
// The cut goes through stringutil.UTF8Prefix, finishing the sweep of #1647 /
// #1664 / #1665 in the part of cmd/mcpproxy those PRs set aside. Table cells
// carry upstream- and operator-authored text — registry and tool
// descriptions, catalog titles, server names — so a non-ASCII value longer
// than its column can put a multi-byte rune astride the cut, and a raw byte
// slice then writes a partial rune to the terminal.
//
// The result never exceeds maxLen; ASCII behaviour is unchanged.
func truncateCell(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return stringutil.UTF8Prefix(s, maxLen)
	}
	return stringutil.UTF8Prefix(s, maxLen-3) + "..."
}
