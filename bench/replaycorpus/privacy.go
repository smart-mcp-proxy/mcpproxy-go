package replaycorpus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BodyPolicy decides whether a load reads recorded request and response bodies.
//
// The zero value is BodiesOff, and that is the whole design: an operator who
// constructs Options without thinking about this field gets the safe behaviour,
// and bodies-on is reachable only by naming it. Defaulting the other way would
// mean a forgotten flag silently loads raw, unmasked user traffic.
type BodyPolicy int

const (
	// BodiesOff loads sizes, statuses and identities only. This is the default
	// and the documented posture: it yields menu costs and the cross-mode delta
	// between the two direct cells, and NO absolute workload cost — which is
	// reported as unavailable, never as zero.
	BodiesOff BodyPolicy = iota

	// BodiesOnUnmasked loads recorded bodies so response cost can be measured
	// rather than estimated. The name says "unmasked" because that is the fact
	// an operator must weigh: the activity EXPORT PATH DOES NOT MASK —
	// maskActivityPayloads is wired into the list and detail handlers only — so
	// a bodies-on export is raw traffic, by design, as the compliance surface.
	// Selecting it prints a warning; the bodies still never leave this package.
	BodiesOnUnmasked
)

// String renders the policy for a report or a warning line.
func (p BodyPolicy) String() string {
	if p == BodiesOnUnmasked {
		return "bodies_on_unmasked"
	}
	return "bodies_off"
}

// bodiesOnWarning is printed once per bodies-on load. It names the unmasked
// export path explicitly, because "you asked for bodies" is not the part an
// operator needs to hear — "the export never masked them" is.
const bodiesOnWarning = "replay: bodies-on requested — the activity export path does NOT mask payloads (masking is wired into the list and detail handlers only), so this input is raw unmasked user traffic; bodies are tokenized inside the loader and only counts leave it, and the input file should be deleted when the run is finished"

// ErrInsideRepository is returned for an input path inside the repository
// working tree.
var ErrInsideRepository = errors.New("replay input is inside the repository working tree")

// ErrCSVInput is returned for a CSV export, by name or by content.
var ErrCSVInput = errors.New("csv activity export is not a valid replay input")

// csvRejection explains what the CSV export drops, because an operator who
// reached for CSV needs to know why JSONL is not merely preferred: the CSV
// projection carries no work_session_id, no arguments, no response and no byte
// fields, so it can neither group units of work nor account any cost.
func csvRejection(where string) error {
	return fmt.Errorf("%w (%s): the CSV projection drops work_session_id, arguments, response and every byte field, so it can neither group units of work nor account tokens — re-export with `mcpproxy activity export --format json`", ErrCSVInput, where)
}

// assertOutsideRepository refuses an input path inside the repository working
// tree, BEFORE any file I/O — so a path that does not even exist is still
// refused for the right reason rather than reported as missing.
//
// Why this is enforced in code and not left to documentation: replay inputs are
// raw recorded user traffic, and the one irreversible mistake available here is
// dropping such a file somewhere `git add -A` will sweep it up. A convention
// cannot prevent that; a refused load can. repoRoot is resolved from the
// process's working directory when Options does not override it.
func assertOutsideRepository(path, repoRoot string) error {
	if repoRoot == "" {
		return nil // no working tree found; nothing to be inside of
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve replay input path %q: %w", path, err)
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return fmt.Errorf("resolve repository root %q: %w", repoRoot, err)
	}
	if !isInside(abs, root) {
		return nil
	}
	return fmt.Errorf("%w (%s): replay inputs are raw recorded traffic, live outside the checkout and are never committed — move the export elsewhere and delete it when the run is finished", ErrInsideRepository, abs)
}

// isInside reports whether path is root or sits beneath it.
func isInside(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// detectRepositoryRoot walks up from dir looking for a `.git` entry and returns
// the directory holding it, or "" if there is none. It checks for EXISTENCE,
// not for a directory: in a git worktree — which is how this repo's agent
// sessions run — `.git` is a file containing a gitdir pointer, and a
// directory-only check would silently decide the worktree is not a repository
// and wave the refusal through.
func detectRepositoryRoot(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
