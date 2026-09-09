package main

import "testing"

// setOutputGlobals points the CLI's process-global output selectors at one
// format for the duration of a test, and restores whatever was there before.
//
// globalOutputFormat and globalJSONOutput are package-level flag targets, so a
// test that assigns them and never restores leaks its choice into every test
// that runs after it. In file order that is invisible; under
// `go test -shuffle=on` it is not. TestVersionCommandTableOutput and
// TestOutputActivityError_TableFormat both render through
// clioutput.ResolveFormat and assert the default table output, so a leaked
// "json" turns them red — seed 1788937037978355714 on the advisory shuffle
// lane added in #1230 reproduces exactly that.
//
// Use this instead of assigning the globals directly.
func setOutputGlobals(t *testing.T, format string, jsonOut bool) {
	t.Helper()
	prevFormat, prevJSON := globalOutputFormat, globalJSONOutput
	t.Cleanup(func() { globalOutputFormat, globalJSONOutput = prevFormat, prevJSON })
	globalOutputFormat, globalJSONOutput = format, jsonOut
}

// TestSetOutputGlobalsRestores pins the restore half of the helper: without the
// t.Cleanup the subtest's choice would still be installed when it returns, and
// every later test in a shuffled run would inherit it.
func TestSetOutputGlobalsRestores(t *testing.T) {
	before, beforeJSON := globalOutputFormat, globalJSONOutput

	t.Run("inner", func(t *testing.T) {
		setOutputGlobals(t, "json", true)
		if globalOutputFormat != "json" || !globalJSONOutput {
			t.Fatalf("helper did not install the format: %q/%v", globalOutputFormat, globalJSONOutput)
		}
	})

	if globalOutputFormat != before || globalJSONOutput != beforeJSON {
		t.Errorf("globals leaked out of the subtest: got %q/%v, want %q/%v",
			globalOutputFormat, globalJSONOutput, before, beforeJSON)
	}
}
