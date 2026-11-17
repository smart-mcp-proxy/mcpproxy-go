package runtime

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills/skillstest"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// checkChunks asserts the chunking contract on any input: every byte covered,
// each chunk bounded, offsets strictly increasing, overlap bounded.
func checkChunks(t *testing.T, text []byte) {
	t.Helper()
	chunks := skillScanChunks(text)
	require.NotEmpty(t, chunks)
	covered := 0
	prevOff := int64(-1)
	for i, c := range chunks {
		require.LessOrEqual(t, len(c.data), skillScanChunkBytes, "chunk %d", i)
		require.Greater(t, c.offset, prevOff, "offsets strictly increase")
		require.True(t, bytes.Equal(text[c.offset:int(c.offset)+len(c.data)], c.data), "chunk %d is the text at its offset", i)
		require.LessOrEqual(t, c.offset, int64(covered), "no gap before chunk %d", i)
		if i > 0 {
			overlap := covered - int(c.offset)
			require.LessOrEqual(t, overlap, skillScanOverlapBytes)
			require.LessOrEqual(t, overlap*2, len(chunks[i-1].data)+1, "overlap is at most half a chunk")
		}
		prevOff = c.offset
		if end := int(c.offset) + len(c.data); end > covered {
			covered = end
		}
	}
	require.Equal(t, len(text), covered, "every byte is covered")
}

func TestSkillScanChunks(t *testing.T) {
	t.Run("small is one chunk", func(t *testing.T) {
		text := bytes.Repeat([]byte("a\n"), skillScanChunkBytes/2)
		require.Len(t, skillScanChunks(text), 1)
		require.Len(t, skillScanChunks(nil), 1)
	})
	t.Run("cuts at a newline and overlaps", func(t *testing.T) {
		line := strings.Repeat("x", 99) + "\n"
		text := []byte(strings.Repeat(line, 2000)) // 200 KB
		checkChunks(t, text)
		chunks := skillScanChunks(text)
		require.Greater(t, len(chunks), 3)
		for _, c := range chunks[:len(chunks)-1] {
			assert.Equal(t, byte('\n'), c.data[len(c.data)-1], "a chunk ends on a newline when the window has one")
		}
	})
	t.Run("one 3 MiB line of invalid utf-8", func(t *testing.T) {
		text := bytes.Repeat([]byte{0xff, 0xfe, 0x80}, 1<<20)
		checkChunks(t, text)
	})
	t.Run("multi-byte text without newline cuts on a rune boundary", func(t *testing.T) {
		text := []byte(strings.Repeat("日本語", 100_000))
		checkChunks(t, text)
		for _, c := range skillScanChunks(text) {
			assert.True(t, utf8.RuneStart(c.data[0]), "chunk starts on a boundary")
		}
	})
	t.Run("random inputs", func(t *testing.T) {
		rng := rand.New(rand.NewSource(1))
		for i := 0; i < 60; i++ {
			n := skillScanChunkBytes/2 + rng.Intn(5*skillScanChunkBytes)
			text := make([]byte, n)
			switch i % 3 {
			case 0:
				rng.Read(text)
			case 1:
				for j := range text {
					text[j] = "ab \né"[rng.Intn(5)]
				}
			default:
				copy(text, bytes.Repeat([]byte("日\n"), n))
			}
			checkChunks(t, text)
		}
	})
}

func newScanRT(fn skillScanFunc) *Runtime {
	rt := &Runtime{}
	rt.skills.scanFunc = fn
	return rt
}

func TestScanSkillText_VerdictFindingsAndOrder(t *testing.T) {
	var seen []string
	rt := newScanRT(func(server, name, text string) (string, []scanner.ScanFinding, bool) {
		seen = append(seen, name)
		switch {
		case strings.HasSuffix(name, "z.md#0"):
			return "dangerous", []scanner.ScanFinding{{RuleID: "r.z", Severity: "high", Evidence: strings.Repeat("e", 600)}}, true
		case strings.HasSuffix(name, "a.md#0"):
			return "warnings", []scanner.ScanFinding{{RuleID: "r.a", Severity: "low", Description: "desc"}}, true
		}
		return "clean", nil, true
	})
	res := rt.scanSkillText(context.Background(), "srv", "acme/pdf", []skillTextFile{
		{"z.md", []byte("z")}, {"a.md", []byte("a")}, {"SKILL.md", []byte("s")},
	})
	assert.Equal(t, []string{"acme/pdf/SKILL.md#0", "acme/pdf/a.md#0", "acme/pdf/z.md#0"}, seen, "SKILL.md first, then by path")
	assert.Equal(t, "dangerous", res.Verdict, "the worst verdict wins")
	assert.True(t, res.Complete)
	assert.Empty(t, res.Incomplete)
	require.Len(t, res.Findings, 2)
	assert.Equal(t, storage.SkillScanFinding{File: "a.md", Offset: 0, Rule: "r.a", Severity: "low", Evidence: "desc"}, res.Findings[0])
	assert.LessOrEqual(t, len(res.Findings[1].Evidence), skillScanEvidenceMax)
	assert.False(t, res.ScannedAt.IsZero())
}

func TestScanSkillText_FindingCap(t *testing.T) {
	rt := newScanRT(func(string, string, string) (string, []scanner.ScanFinding, bool) {
		f := make([]scanner.ScanFinding, 40)
		for i := range f {
			f[i] = scanner.ScanFinding{RuleID: fmt.Sprintf("r%d", i)}
		}
		return "warnings", f, true
	})
	res := rt.scanSkillText(context.Background(), "srv", "p", []skillTextFile{{"SKILL.md", []byte("x")}})
	assert.Len(t, res.Findings, skillScanMaxFindings)
	assert.True(t, res.FindingsTruncated)
}

func TestScanSkillText_ChunkOffsetsAndSizeCap(t *testing.T) {
	var offsets []int
	rt := newScanRT(func(_, _ string, text string) (string, []scanner.ScanFinding, bool) {
		return "clean", nil, true
	})
	line := strings.Repeat("y", 1023) + "\n"
	big := []byte(strings.Repeat(line, 3<<10)) // 3 MiB
	res := rt.scanSkillText(context.Background(), "srv", "p", []skillTextFile{{"SKILL.md", big}})
	_ = offsets
	assert.False(t, res.Complete)
	assert.Equal(t, storage.SkillScanIncompleteSizeCap, res.Incomplete)
	assert.Equal(t, "clean", res.Verdict)

	// Offsets of findings are the chunk's byte offset in the file.
	var calls int
	rt2 := newScanRT(func(_, _ string, text string) (string, []scanner.ScanFinding, bool) {
		calls++
		if calls == 2 {
			return "warnings", []scanner.ScanFinding{{RuleID: "x"}}, true
		}
		return "clean", nil, true
	})
	res = rt2.scanSkillText(context.Background(), "srv", "p", []skillTextFile{{"SKILL.md", []byte(strings.Repeat(line, 200))}})
	require.Len(t, res.Findings, 1)
	chunks := skillScanChunks([]byte(strings.Repeat(line, 200)))
	assert.Equal(t, chunks[1].offset, res.Findings[0].Offset)
	assert.Greater(t, res.Findings[0].Offset, int64(0))
	assert.True(t, res.Complete)
}

func TestScanSkillText_CoverageAndInterruption(t *testing.T) {
	rt := newScanRT(func(string, string, string) (string, []scanner.ScanFinding, bool) { return "clean", nil, false })
	res := rt.scanSkillText(context.Background(), "srv", "p", []skillTextFile{{"SKILL.md", []byte("x")}})
	assert.False(t, res.Complete)
	assert.Equal(t, storage.SkillScanIncompleteCoverage, res.Incomplete)

	ctx, cancel := context.WithCancel(context.Background())
	var n int
	rt = newScanRT(func(string, string, string) (string, []scanner.ScanFinding, bool) {
		n++
		cancel() // the context ends after the first chunk
		return "clean", nil, true
	})
	line := strings.Repeat("y", 99) + "\n"
	res = rt.scanSkillText(ctx, "srv", "p", []skillTextFile{{"SKILL.md", []byte(strings.Repeat(line, 2000))}})
	assert.Equal(t, 1, n)
	assert.Equal(t, storage.SkillScanIncompleteInterrupted, res.Incomplete)
	assert.False(t, res.Complete)
}

func TestScanStateOf(t *testing.T) {
	assert.Equal(t, skillScanState{}, scanStateOf(nil, "h"))
	assert.Equal(t, skillScanState{}, scanStateOf(&storage.SkillScan{Verdict: "clean", Complete: true, Hash: "other"}, "h"))
	assert.Equal(t, skillScanState{clean: true, complete: true}, scanStateOf(&storage.SkillScan{Verdict: "clean", Complete: true, Hash: "h"}, "h"))
	assert.Equal(t, skillScanState{complete: true}, scanStateOf(&storage.SkillScan{Verdict: "clean", Complete: true, Hash: "h", Findings: []storage.SkillScanFinding{{}}}, "h"))
	assert.Equal(t, skillScanState{clean: true}, scanStateOf(&storage.SkillScan{Verdict: "clean", Hash: "h"}, "h"))
}

// --- through a refresh ---

func countingScan(n *atomic.Int32, verdict string, coverage bool) skillScanFunc {
	return func(string, string, string) (string, []scanner.ScanFinding, bool) {
		n.Add(1)
		return verdict, nil, coverage
	}
}

func TestSkillsRefresh_ScanIsRecordedAndNotRepeated(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	var n atomic.Int32
	g.rt.skills.scanFunc = countingScan(&n, "clean", true)
	g.fx.AddSkill(skillstest.Skill{Path: "pdf", Files: map[string][]byte{"a.md": []byte("alpha"), "b.bin": {0, 1, 2, 0xff}}, Binary: []string{"b.bin"}})
	require.NoError(t, g.refresh(t))
	rec := g.records(t)["pdf"]
	require.NotNil(t, rec.Scan)
	assert.Equal(t, rec.CurrentHash, rec.Scan.Hash)
	assert.True(t, rec.Scan.Complete)
	assert.Equal(t, int32(2), n.Load(), "SKILL.md and a.md; the binary file is not scanned")

	require.NoError(t, g.refresh(t))
	require.NoError(t, g.refresh(t))
	assert.Equal(t, int32(2), n.Load(), "a finished scan of unchanged content is never repeated")

	g.fx.Mutate("pdf", "a.md", []byte("beta"))
	require.NoError(t, g.refresh(t))
	assert.Equal(t, int32(4), n.Load(), "changed content is scanned again")
	assert.Equal(t, g.records(t)["pdf"].CurrentHash, g.records(t)["pdf"].Scan.Hash)
}

func TestSkillsRefresh_DegradedScanIsRetried(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	var n atomic.Int32
	g.rt.skills.scanFunc = countingScan(&n, "clean", false)
	g.fx.AddSkill(skillstest.Skill{Path: "pdf"})
	require.NoError(t, g.refresh(t))
	first := n.Load()
	require.NotZero(t, first)
	assert.Equal(t, storage.SkillScanIncompleteCoverage, g.records(t)["pdf"].Scan.Incomplete)
	require.NoError(t, g.refresh(t))
	assert.Greater(t, n.Load(), first, "a scan with degraded coverage is retried")

	g.rt.skills.scanFunc = countingScan(&n, "clean", true)
	require.NoError(t, g.refresh(t))
	assert.True(t, g.records(t)["pdf"].Scan.Complete)
	at := n.Load()
	require.NoError(t, g.refresh(t))
	assert.Equal(t, at, n.Load())
}

func TestSkillsRefresh_SizeCapScanIsKeptNotRepeated(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	var n atomic.Int32
	g.rt.skills.scanFunc = countingScan(&n, "clean", true)
	huge := []byte(strings.Repeat(strings.Repeat("z", 1023)+"\n", 2100)) // > 2 MiB
	g.fx.AddSkill(skillstest.Skill{Path: "big", Files: map[string][]byte{"a.md": huge}})
	require.NoError(t, g.refresh(t))
	rec := g.records(t)["big"]
	require.NotNil(t, rec.Scan)
	assert.Equal(t, storage.SkillScanIncompleteSizeCap, rec.Scan.Incomplete)
	at := n.Load()
	require.NoError(t, g.refresh(t))
	assert.Equal(t, at, n.Load())
	_ = skills.MaxScanBytesPerSkill
}

func TestSkillsRefresh_UnusableSkillIsNotScanned(t *testing.T) {
	g := newSkillsRig(t, skillsRigOpts{enable: true})
	var n atomic.Int32
	g.rt.skills.scanFunc = countingScan(&n, "clean", true)
	g.fx.AddSkill(skillstest.Skill{Path: "wd", Files: map[string][]byte{"a.md": []byte("a")}, WrongDigest: []string{"a.md"}})
	require.NoError(t, g.refresh(t))
	rec := g.records(t)["wd"]
	assert.Equal(t, storage.SkillStatusUnusable, rec.Status)
	assert.Nil(t, rec.Scan)
	assert.Zero(t, n.Load())
}

// BenchmarkScanSkillChunk measures one 48 KiB chunk of ordinary markdown
// through the real in-process scanner, to size the 2 MiB per-skill cap
// against the refresh timeout.
func BenchmarkScanSkillChunk(b *testing.B) {
	para := "## Usage\n\nRun the formatter on the changed files, then open the report in your editor. " +
		"The helper reads `config.yaml` from the project root and writes a summary next to it.\n\n" +
		"- Step one: install the dependencies.\n- Step two: run `make check` and read the output.\n\n"
	var sb strings.Builder
	for sb.Len() < skillScanChunkBytes {
		sb.WriteString(para)
	}
	for _, size := range []int{2 << 10, 8 << 10, skillScanChunkBytes} {
		text := sb.String()[:size]
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			b.SetBytes(int64(len(text)))
			for i := 0; i < b.N; i++ {
				defaultSkillScan("srv", "p/SKILL.md#0", text)
			}
		})
	}
}
