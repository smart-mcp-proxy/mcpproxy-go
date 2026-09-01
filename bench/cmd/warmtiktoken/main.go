// Command warmtiktoken populates the tiktoken vocabulary cache before a
// parallel test run.
//
// tiktoken-go downloads the BPE on first use and finishes with an atomic
// rename into a shared cache directory. `go test ./...` runs packages in
// parallel, so several test binaries race to download the same file; on Windows
// the loser hits "Access is denied" on the rename and its whole package fails.
// It surfaced as bench/arms flaking with no code change anywhere near it.
//
// Setting TIKTOKEN_CACHE_DIR alone does not fix that — it names a cache, and an
// empty one still races. Running this once first does, because every later
// construction is a cache hit that renames nothing.
//
// It doubles as the SC-004 offline prerequisite: an outsider reproducing the
// deterministic figures needs the vocabulary present, and "warm the cache" is
// easier to follow as a command than as a paragraph.
package main

import (
	"fmt"
	"os"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
)

func main() {
	tk, err := bench.NewTokenizer(bench.DefaultEncoding)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warmtiktoken: %v\n", err)
		os.Exit(1)
	}
	// Encode something so a lazily-initialised encoder is actually exercised
	// rather than merely constructed.
	n := tk.Count("warm the cache")
	fmt.Printf("warmtiktoken: %s ready (%d tokens for the probe string), cache dir %q\n",
		bench.DefaultEncoding, n, os.Getenv("TIKTOKEN_CACHE_DIR"))
}
