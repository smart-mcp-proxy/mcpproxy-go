package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestContractsInSync fails when frontend/src/types/contracts.ts has drifted
// from what cmd/generate-types would produce today. Catches the failure mode
// where a contributor hand-edits contracts.ts (or hand-edits the generator's
// hardcoded string literals) without updating the other side: the next
// `make build` / `go run ./cmd/generate-types` silently reverts their work
// and leaves a dirty working tree.
//
// To fix a failure of this test:
//   1. Decide which side is correct (usually: the generator).
//   2. Run `go run ./cmd/generate-types` from the module root, OR update the
//      string literals in main.go to match contracts.ts.
//   3. Commit both files in the same change.
func TestContractsInSync(t *testing.T) {
	// cmd/generate-types tests run with cwd = the package directory.
	// Walk up two levels to reach the module root.
	contractsPath := filepath.Join("..", "..", contractsRelPath)

	committed, err := os.ReadFile(contractsPath)
	if err != nil {
		t.Fatalf("reading %s: %v", contractsPath, err)
	}

	generated := []byte(generateFileContent())

	if string(committed) == string(generated) {
		return
	}

	t.Fatalf(
		"%s is out of sync with cmd/generate-types/main.go.\n"+
			"\nThe TypeScript string literals in main.go must produce a byte-identical\n"+
			"copy of contracts.ts. To fix: either run `go run ./cmd/generate-types`\n"+
			"from the module root (if the generator is the source of truth) or update\n"+
			"the string literals in main.go (if contracts.ts is the source of truth).\n"+
			"\ncommitted size: %d bytes\ngenerated size: %d bytes",
		contractsRelPath, len(committed), len(generated),
	)
}
