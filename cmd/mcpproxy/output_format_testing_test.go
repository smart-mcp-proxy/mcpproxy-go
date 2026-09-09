package main

import "testing"

// setOutputFormat pins the process-global CLI output format for the duration of
// one test (or subtest) and restores the previous values when it finishes.
//
// globalOutputFormat and globalJSONOutput are read by ResolveOutputFormat on
// every command path, so a test that assigns them without restoring changes the
// behaviour of every test that runs after it. That is invisible in declaration
// order and shows up as an unrelated failure under `go test -shuffle`: leaving
// globalOutputFormat = "invalid-format" behind, for example, makes
// GetOutputFormatter fail inside outputActivityError, which then takes its
// early-return path and prints no "Hint:" line.
func setOutputFormat(t *testing.T, format string, jsonAlias bool) {
	t.Helper()
	prevFormat, prevJSON := globalOutputFormat, globalJSONOutput
	globalOutputFormat, globalJSONOutput = format, jsonAlias
	t.Cleanup(func() {
		globalOutputFormat, globalJSONOutput = prevFormat, prevJSON
	})
}
