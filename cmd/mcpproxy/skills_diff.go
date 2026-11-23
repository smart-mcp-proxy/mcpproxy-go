package main

import (
	"fmt"
	"strings"
)

// A small line diff for `skills show --diff`. The repository has no unified
// diff helper the CLI can use (the review diff in internal/runtime is a
// whole-file replace and unexported), and no new dependency is allowed.

const (
	// diffContext is the number of unchanged lines shown around a change.
	diffContext = 3
	// diffMaxCells bounds the LCS table. A pair of files whose differing
	// middle needs more cells is shown as one replaced block instead.
	diffMaxCells = 4_000_000
)

type diffOp struct {
	kind byte // ' ', '-' or '+'
	text string
}

// splitDiffLines splits text into lines without their line feeds. A final line
// feed does not make an extra empty line.
func splitDiffLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// diffLines returns the edit script turning a into b: common prefix and suffix
// are peeled off, the middle goes through a longest-common-subsequence table.
func diffLines(a, b []string) []diffOp {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	am, bm := a[pre:len(a)-suf], b[pre:len(b)-suf]

	ops := make([]diffOp, 0, len(a)+len(b))
	for _, l := range a[:pre] {
		ops = append(ops, diffOp{' ', l})
	}
	ops = append(ops, diffMiddle(am, bm)...)
	for _, l := range a[len(a)-suf:] {
		ops = append(ops, diffOp{' ', l})
	}
	return ops
}

func diffMiddle(a, b []string) []diffOp {
	if len(a) == 0 || len(b) == 0 || (len(a)+1)*(len(b)+1) > diffMaxCells {
		ops := make([]diffOp, 0, len(a)+len(b))
		for _, l := range a {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range b {
			ops = append(ops, diffOp{'+', l})
		}
		return ops
	}
	// lcs[i][j] is the LCS length of a[i:] and b[j:].
	w := len(b) + 1
	lcs := make([]int32, (len(a)+1)*w)
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
			} else if lcs[(i+1)*w+j] >= lcs[i*w+j+1] {
				lcs[i*w+j] = lcs[(i+1)*w+j]
			} else {
				lcs[i*w+j] = lcs[i*w+j+1]
			}
		}
	}
	ops := make([]diffOp, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case lcs[(i+1)*w+j] >= lcs[i*w+j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < len(b); j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

// unifiedDiff renders the difference between two texts as unified-diff hunks
// with diffContext lines of context. It returns "" when the texts are equal.
// The result is raw: the caller prints it through the terminal escaping
// helpers.
func unifiedDiff(oldName, newName, oldText, newText string) string {
	if oldText == newText {
		return ""
	}
	a := splitDiffLines(oldText)
	b := splitDiffLines(newText)
	ops := diffLines(a, b)

	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", oldName, newName)

	// aLine[i], bLine[i] are the 1-based line numbers of ops[i] on each side.
	aLine := make([]int, len(ops))
	bLine := make([]int, len(ops))
	ai, bi := 1, 1
	var changes []int
	for i, op := range ops {
		aLine[i], bLine[i] = ai, bi
		if op.kind != '+' {
			ai++
		}
		if op.kind != '-' {
			bi++
		}
		if op.kind != ' ' {
			changes = append(changes, i)
		}
	}
	if len(changes) == 0 {
		out.WriteString("@@ only the final line feed differs @@\n")
		return out.String()
	}
	for g := 0; g < len(changes); {
		first, last := changes[g], changes[g]
		g++
		for g < len(changes) && changes[g]-last-1 <= 2*diffContext {
			last = changes[g]
			g++
		}
		start, end := first-diffContext, last+diffContext+1
		if start < 0 {
			start = 0
		}
		if end > len(ops) {
			end = len(ops)
		}
		oldN, newN := 0, 0
		for k := start; k < end; k++ {
			if ops[k].kind != '+' {
				oldN++
			}
			if ops[k].kind != '-' {
				newN++
			}
		}
		oldStart, newStart := aLine[start], bLine[start]
		if oldN == 0 {
			oldStart--
		}
		if newN == 0 {
			newStart--
		}
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", hunkRange(oldStart, oldN), hunkRange(newStart, newN))
		for k := start; k < end; k++ {
			out.WriteByte(ops[k].kind)
			out.WriteString(ops[k].text)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func hunkRange(start, n int) string {
	if n == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, n)
}
