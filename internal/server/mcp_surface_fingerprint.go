package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Surface fingerprints exist to stop a tool surface re-registering itself when
// nothing about it changed.
//
// mcp-go's SetTools notifies EVERY initialized client unconditionally — it
// replaces the registry and then sends notifications/tools/list_changed without
// comparing old and new content, despite its own comment saying "when the list
// of available tools changes". So any caller that rebuilds speculatively tells
// every client the tool set moved, whether or not it did.
//
// Measured on one ordinary 4-server startup before this guard: the
// code-execution surface published 9 times with tool_count 7 every time (9 of 9
// spurious), and the direct surface published 10 times across 4 distinct tool
// sets (at least 6 spurious). Both are driven by servers.changed, which has 17
// emitters including server_connected, server_disconnected and
// server_state_changed — so a reconnecting upstream re-notified every client on
// every attempt, and the 50ms coalescer does not collapse anything on that
// timescale.
//
// The cost is a client round-trip per notification, not a lost provider cache:
// mcp-go sorts tools/list, so a re-fetch of an unchanged set returns
// byte-identical content and a cached prefix still hits. The direct surface is
// the one where content genuinely moves, and there the rebuild must still
// happen — guarding must not become muting.

// toolSetFingerprint hashes the tool DEFINITIONS a surface would advertise.
//
// It covers exactly what a client can observe in tools/list — name, description,
// schemas, annotations — because that is also precisely what invalidates a
// provider's prompt cache. Sorted by name, since tools/list is sorted and
// registration order is therefore not observable.
func toolSetFingerprint(tools []mcpserver.ServerTool) string {
	encoded := make([]string, 0, len(tools))
	for i := range tools {
		blob, err := json.Marshal(tools[i].Tool)
		if err != nil {
			// A tool that will not marshal cannot be compared. Return a value
			// that never matches, so the caller rebuilds rather than skipping
			// on the strength of a comparison that did not happen.
			return fmt.Sprintf("unfingerprintable-%d-%v", i, err)
		}
		encoded = append(encoded, string(blob))
	}
	sort.Strings(encoded)

	sum := sha256.New()
	for _, e := range encoded {
		// Length-prefixed so two adjacent definitions cannot be re-split into a
		// different pair with the same concatenation.
		fmt.Fprintf(sum, "%d:%s", len(e), e)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// routingFingerprint hashes the catalog state that DISPATCH depends on, which is
// not the same as what the listing shows.
//
// The direct surface publishes registry and catalog as a pair — a handler closes
// over its catalog entry — so a rebuild may only be skipped when routing is
// unchanged as well. Routing can move while the listing stays byte-identical: a
// display-name collision resolves to one upstream or the other without changing
// what is listed, and RequiredPermission gates scoped tokens without appearing
// in the tool definition at all.
//
// RenderedDescription is deliberately EXCLUDED. It is captured at render time
// and the signature cache mutates independently of rebuilds, so including it
// would report a cache warm or evict as a catalog change — the distinction D13
// rule 5 exists to preserve. What the client sees is already covered by
// toolSetFingerprint, which the caller pairs with this.
func (c *directCatalog) routingFingerprint() string {
	if c == nil {
		return "nil-catalog"
	}
	sum := sha256.New()
	fmt.Fprintf(sum, "mode:%s\n", c.mode)

	names := make([]string, 0, len(c.byDisplayName))
	for name := range c.byDisplayName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		e := c.byDisplayName[name]
		if e == nil {
			fmt.Fprintf(sum, "entry:%s:nil\n", name)
			continue
		}
		fmt.Fprintf(sum, "entry:%s\x00%s\x00%s\x00%s\x00%s\n",
			e.DisplayName, e.ServerName, e.ToolName, e.Hash, e.RequiredPermission)
	}

	// Withheld collisions decide which names are absent AND why, so a change
	// there is a routing change even when the surviving listing is identical.
	withheld := make([]string, 0, len(c.withheld))
	for _, w := range c.withheld {
		origins := make([]string, 0, len(w.Origins))
		for _, o := range w.Origins {
			origins = append(origins, fmt.Sprintf("%v", o))
		}
		sort.Strings(origins)
		withheld = append(withheld, fmt.Sprintf("%s=%v", w.DisplayName, origins))
	}
	sort.Strings(withheld)
	for _, w := range withheld {
		fmt.Fprintf(sum, "withheld:%s\n", w)
	}

	return hex.EncodeToString(sum.Sum(nil))
}

// directSurfaceUnchanged reports whether a rebuild would be a no-op.
//
// It is a named predicate rather than an inline `&&` because BOTH halves are
// load-bearing and the second is the non-obvious one: the listing can be
// byte-identical while dispatch has moved underneath it, and a guard that
// compared only the listing would skip a rebuild that had to happen. Extracted
// so that requirement is covered by a test rather than by a comment.
func directSurfaceUnchanged(lastTools, lastRouting, toolsFP, routingFP string) bool {
	if lastTools == "" && lastRouting == "" {
		// Nothing has been published yet, so there is nothing to compare and a
		// first rebuild must always proceed.
		return false
	}
	return lastTools == toolsFP && lastRouting == routingFP
}
