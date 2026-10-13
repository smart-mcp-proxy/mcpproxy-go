// Command qapilot is the quarantine/approval testing pilot's CI driver.
//
//	qapilot report --dir <ledger dir> [--mutation-report <file>] [--out <file>]
//	    merges the check ledger the pilot tests wrote (QAPILOT_REPORT_DIR)
//	    with the requirement and scenario inventory and writes a
//	    machine-readable coverage report; exits 1 when a mandatory
//	    requirement, scenario or decision cell is missing, skipped or failed,
//	    or when the mutation report records a surviving required mutation or
//	    an infrastructure failure.
//
//	qapilot mutate --repo <path> --mutations <file> [--out <file>] [--only id,...]
//	    runs the versioned mutation set: for each mutation it creates a
//	    disposable git worktree of HEAD, applies the mutation, builds and
//	    runs the mutation's designated detecting tests, and classifies the
//	    outcome. The baseline (unmutated) run of the same tests must pass
//	    first. It never touches the caller's checkout.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "report":
		err = runReport(os.Args[2:])
	case "mutate":
		err = runMutate(os.Args[2:])
	case "list":
		err = runList(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "qapilot:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: qapilot report --dir DIR [--mutation-report FILE] [--out FILE]\n       qapilot mutate --repo PATH --mutations FILE [--out FILE] [--only ID,...] [--timeout DURATION]\n       qapilot list")
}
