// Package replaycorpus loads exported mcpproxy activity JSONL
// (`mcpproxy activity export --format json`) into grouped units of work for the
// token benchmark's deterministic replay (Spec 103, US1). It groups records by
// work session, joins code-execution sub-calls to the call that issued them,
// and classifies every record's usability ONCE, at load.
//
// Two invariants define this package and must survive every future change:
//
//  1. IT IMPORTS NOTHING FROM `bench`. The sibling loader `bench/corpusio`
//     imports `bench`, and `bench/replay.go` — the consumer of this package —
//     lives in `bench`. If this package mirrored corpusio, that import would be
//     a cycle and replay could not use it at all. So the replay domain types
//     (ReplayCall, ReplaySession, Cost, Flags) are DEFINED HERE rather than
//     borrowed, and the tokenizer is the tiktoken library used directly rather
//     than bench.Tokenizer. Adding a `bench` import here does not produce a
//     compile error in this package — it breaks `bench/replay.go`, one package
//     away, which is why the rule is stated rather than merely enforced by the
//     build.
//
//  2. NO CONTENT CROSSES THIS BOUNDARY — ONLY COUNTS. Replay inputs are raw
//     recorded user traffic, and the activity EXPORT PATH DOES NOT MASK:
//     maskActivityPayloads is wired into the list and detail handlers only, so a
//     bodies-on export is unmasked by design. Therefore no exported type in this
//     package has a field that holds a request body, a response body, arguments,
//     or metadata; bodies exist only as local variables inside the load call
//     that reads them. That is also why tokenization happens HERE
//     (tokenize.go) rather than in the scoring layer: handing bodies out to be
//     counted elsewhere would defeat the invariant, and the two cannot both
//     hold. Bodies-off is the zero value, so an operator who never thinks about
//     the flag gets the safe behaviour (privacy.go).
//
// A third rule is a property of the data rather than of the package, and
// flags.go is where it lives: A TRUNCATED RECORD MUST NEVER CONTRIBUTE
// SILENTLY. Understating cost is the one direction of error the whole benchmark
// exists to prevent, so a truncated record either carries an explicitly
// annotated estimate derived from its pre-truncation byte length, or is
// excluded and counted. Every exclusion is counted and reportable (FR-002,
// FR-003, SC-008): silence is never success. Note that the first branch is a
// real one: codeexec.go's BaselineDirect COUNTS a truncated sub-call, at its
// full pre-truncation byte length, and stays inside this rule only because
// CodeExecSaving.TruncatedSubCalls and TruncatedBaseline publish how many such
// components contributed and how much of the baseline they are. Without that
// annotation direct mode would be outside the invariant, which is why those two
// fields are load-bearing rather than decorative.
//
// The binding contract for the input shape is
// specs/103-token-bench/contracts/replay-input.md.
package replaycorpus
