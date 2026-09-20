// Command scope-latency-compare is the T112a merge-base gate for Spec 105
// FR-011: it compares the administrator p95 latency for each of the four
// scope-hardening operations (retrieve_tools, read_cache, prompts/list,
// tools/list) between two revisions and fails if HEAD regressed by more than
// max(10%, 5ms) on any operation, or if either side is missing a
// measurement for any operation.
//
// Input: two files, each produced by internal/server's scope_latency_test.go
// (via SCOPE_LATENCY_RESULTS_FILE) as newline-separated "operation=nanoseconds"
// pairs. No third-party dependency — stdlib only, per research D10.
//
// Usage: scope-latency-compare <merge-base-results-file> <head-results-file>
package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// requiredOperations is FR-011's fixed operation list; every result file
// MUST report a measurement for every one of these, or the comparison fails
// closed (T112a: "fails if either side yields no measurement for any
// operation").
var requiredOperations = []string{"retrieve_tools", "read_cache", "prompts/list", "tools/list"}

const minAbsoluteBudget = 5 * time.Millisecond
const relativeBudget = 0.10 // 10%

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: scope-latency-compare <merge-base-results-file> <head-results-file>")
		os.Exit(2)
	}
	baseFile, headFile := os.Args[1], os.Args[2]

	base, err := readResults(baseFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading merge-base results %s: %v\n", baseFile, err)
		os.Exit(1)
	}
	head, err := readResults(headFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading HEAD results %s: %v\n", headFile, err)
		os.Exit(1)
	}

	lines, failures := compare(base, head, baseFile, headFile)
	for _, line := range lines {
		fmt.Println(line)
	}
	if len(failures) > 0 {
		fmt.Fprintln(os.Stderr, "\nscope-latency-compare: FAIL")
		for _, f := range failures {
			fmt.Fprintln(os.Stderr, "  - "+f)
		}
		os.Exit(1)
	}
	fmt.Println("\nscope-latency-compare: PASS — every operation within FR-011's merge-base regression budget")
}

// compare evaluates every required operation and returns (a) one
// human-readable report line per operation that had a measurement on both
// sides, and (b) the list of failure reasons — missing measurements or a
// regression exceeding max(10%, 5ms). baseFile/headFile are used only to
// name the missing side in a failure message.
func compare(base, head map[string]int64, baseFile, headFile string) (lines, failures []string) {
	for _, op := range requiredOperations {
		baseNS, baseOK := base[op]
		headNS, headOK := head[op]
		if !baseOK {
			failures = append(failures, fmt.Sprintf("%s: no measurement on the merge-base side (%s)", op, baseFile))
			continue
		}
		if !headOK {
			failures = append(failures, fmt.Sprintf("%s: no measurement on the HEAD side (%s)", op, headFile))
			continue
		}

		baseDur := time.Duration(baseNS)
		headDur := time.Duration(headNS)
		budget := time.Duration(float64(baseDur) * relativeBudget)
		if budget < minAbsoluteBudget {
			budget = minAbsoluteBudget
		}
		regression := headDur - baseDur
		status := "OK"
		if regression > budget {
			status = "REGRESSION"
			failures = append(failures, fmt.Sprintf(
				"%s: administrator p95 regressed by %s (base=%s head=%s), exceeding the max(10%%, 5ms) budget of %s",
				op, regression, baseDur, headDur, budget))
		}
		lines = append(lines, fmt.Sprintf("%-16s base=%-12s head=%-12s delta=%-12s budget=%-10s %s",
			op, baseDur, headDur, regression, budget, status))
	}
	return lines, failures
}

// readResults parses a scope_latency_test.go results file: one
// "operation=nanoseconds" pair per line, blank lines ignored. Multiple lines
// for the same operation (a test rerun, or -count>1) keep the LAST value.
func readResults(path string) (map[string]int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := make(map[string]int64)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("malformed line %q (want operation=nanoseconds)", line)
		}
		ns, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("malformed duration in line %q: %w", line, err)
		}
		out[strings.TrimSpace(parts[0])] = ns
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no measurements found in %s", path)
	}
	return out, nil
}
