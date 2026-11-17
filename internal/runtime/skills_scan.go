package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/skills"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 (skills over MCP), PR-2, plan D9: the in-process scan of a skill's
// text. The scanner was built for tool descriptions; a skill file is fed to it
// in bounded chunks so one huge file cannot stall a refresh.

const (
	skillScanChunkBytes   = 48 << 10
	skillScanOverlapBytes = 1 << 10
	skillScanMaxFindings  = 32
	skillScanEvidenceMax  = 256

	// The scanner needs about a second for 48 KiB of text (it runs every
	// signature of the bundle over it). skillsScanPerRefresh is how long one
	// refresh may spend scanning before it leaves the remaining skills to the
	// refresh that follows; skillScanPerSkill stops the scan of one skill.
	skillsScanPerRefresh  = 45 * time.Second
	skillScanPerSkill     = 2 * time.Minute
	skillsScanFollowUpGap = 10 * time.Second
)

type skillScanChunk struct {
	offset int64
	data   []byte
}

type skillTextFile struct {
	relPath string
	data    []byte
}

// skillScanFunc scans one chunk and returns the scanner's verdict. Tests
// replace it to inject verdicts.
type skillScanFunc func(serverName, name, text string) (verdict string, findings []scanner.ScanFinding, coverageOK bool)

func defaultSkillScan(serverName, name, text string) (string, []scanner.ScanFinding, bool) {
	return scanner.ScanToolMetadataVerdict(serverName, []*config.ToolMetadata{{Name: name, Description: text}}, nil)
}

// skillScanChunks splits text into chunks of at most 48 KiB. A text of at most
// that size is one chunk. Otherwise a chunk ends at the last newline inside
// its window, else at the last UTF-8 boundary; the next one starts up to 1 KiB
// (never more than half a chunk) earlier, on a UTF-8 boundary, so a pattern cut
// by a boundary is seen whole by one of the two. Every chunk starts after the
// previous start, so the loop ends on any input.
func skillScanChunks(text []byte) []skillScanChunk {
	if len(text) <= skillScanChunkBytes {
		return []skillScanChunk{{offset: 0, data: text}}
	}
	var out []skillScanChunk
	start := 0
	for {
		end := start + skillScanChunkBytes
		if end >= len(text) {
			out = append(out, skillScanChunk{offset: int64(start), data: text[start:]})
			return out
		}
		cut := end
		if i := lastIndexByte(text[start:end], '\n'); i >= 0 {
			cut = start + i + 1
		} else {
			for back := 0; back < utf8.UTFMax && cut > start && !utf8.RuneStart(text[cut]); back++ {
				cut--
			}
			if cut <= start {
				cut = end
			}
		}
		out = append(out, skillScanChunk{offset: int64(start), data: text[start:cut]})

		overlap := skillScanOverlapBytes
		if half := (cut - start) / 2; overlap > half {
			overlap = half
		}
		next := cut - overlap
		for next < cut && next < len(text) && !utf8.RuneStart(text[next]) {
			next++
		}
		if next <= start {
			next = cut
		}
		start = next
	}
}

func lastIndexByte(b []byte, c byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == c {
			return i
		}
	}
	return -1
}

func skillVerdictRank(v string) int {
	switch v {
	case "clean":
		return 0
	case "dangerous":
		return 2
	default: // "warnings" and anything the scanner may add
		return 1
	}
}

// scanSkillText scans the text files of one skill and returns the result to
// record. A cancelled ctx stops it between chunks and marks the result
// interrupted; it is never an error.
func (r *Runtime) scanSkillText(ctx context.Context, serverName, skillPath string, files []skillTextFile) *storage.SkillScan {
	scan := r.skills.scanFunc
	if scan == nil {
		scan = defaultSkillScan
	}
	sorted := append([]skillTextFile(nil), files...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i].relPath, sorted[j].relPath
		if (a == "SKILL.md") != (b == "SKILL.md") {
			return a == "SKILL.md"
		}
		return a < b
	})

	res := &storage.SkillScan{Verdict: "clean", ScannedAt: time.Now()}
	deadline := res.ScannedAt.Add(r.skills.limits.scanSkill())
	var interrupted, timedOut, degraded, capped, scannedAny bool
	budget := int64(skills.MaxScanBytesPerSkill)
	rank := 0

scanning:
	for _, f := range sorted {
		data := f.data
		if int64(len(data)) > budget {
			data = data[:budget]
			capped = true
		}
		budget -= int64(len(data))
		for n, ch := range skillScanChunks(data) {
			if ctx.Err() != nil {
				interrupted = true
				break scanning
			}
			// The first chunk is always scanned, so a skill is never recorded
			// as out of time with nothing looked at.
			if scannedAny && !time.Now().Before(deadline) {
				timedOut = true
				break scanning
			}
			scannedAny = true
			text := strings.ToValidUTF8(string(ch.data), "\uFFFD")
			verdict, findings, coverageOK := scan(serverName, fmt.Sprintf("%s/%s#%d", skillPath, f.relPath, n), text)
			if !coverageOK {
				degraded = true
			}
			if rk := skillVerdictRank(verdict); rk > rank {
				rank, res.Verdict = rk, verdict
			}
			for _, fd := range findings {
				if len(res.Findings) >= skillScanMaxFindings {
					res.FindingsTruncated = true
					break
				}
				ev := fd.Evidence
				if ev == "" {
					ev = fd.Description
				}
				res.Findings = append(res.Findings, storage.SkillScanFinding{
					File: f.relPath, Offset: ch.offset, Rule: fd.RuleID, Severity: fd.Severity,
					Evidence: cutText(ev, skillScanEvidenceMax),
				})
			}
		}
		if budget <= 0 && capped {
			break
		}
	}
	// A reason that makes the scan worth repeating wins over one that does not.
	switch {
	case interrupted:
		res.Incomplete = storage.SkillScanIncompleteInterrupted
	case timedOut:
		res.Incomplete = storage.SkillScanIncompleteTimeLimit
	case degraded:
		res.Incomplete = storage.SkillScanIncompleteCoverage
	case capped:
		res.Incomplete = storage.SkillScanIncompleteSizeCap
	}
	res.Complete = res.Incomplete == ""
	return res
}

// reusableSkillScan reports whether an earlier scan still stands for content
// with this hash: the same content, and a scan that finished, hit the size cap
// or ran into its time limit (repeating any of them costs the same again and
// gives the same answer). An interrupted or degraded scan is retried.
func reusableSkillScan(prev *storage.SkillApprovalRecord, hash string) bool {
	if prev == nil || prev.Scan == nil || hash == "" || prev.Scan.Hash != hash {
		return false
	}
	switch prev.Scan.Incomplete {
	case "", storage.SkillScanIncompleteSizeCap, storage.SkillScanIncompleteTimeLimit:
		return true
	}
	return false
}

// scanStateOf reduces a scan to what decideSkill reads. A scan of other
// content says nothing about this one.
func scanStateOf(s *storage.SkillScan, currentHash string) skillScanState {
	if s == nil || s.Hash != currentHash {
		return skillScanState{}
	}
	return skillScanState{
		clean:    s.Verdict == "clean" && len(s.Findings) == 0 && !s.FindingsTruncated,
		complete: s.Complete,
	}
}

// scanSkillOutcomes scans the usable skills of one refresh that have no scan
// of their current content, reading each skill's text back from the store so
// only one skill is in memory at a time. It stops starting new scans once the
// refresh's scan time is spent (the first skill is always scanned).
//
// Skills that were never scanned come first; a scan that has to be repeated
// (degraded coverage) is retried only with the time that is left, and keeps
// its earlier result until then. Otherwise one skill whose scan stays degraded
// and takes the whole budget would be retried first on every refresh, and the
// skills listed after it would never be scanned.
//
// firstScans counts the skills that got their first scan of this content
// (whatever its result), unscanned the never-scanned skills it did not get
// to, and pending every skill left without a scan that stands (unscanned, a
// degraded result, a retry not reached). A follow-up refresh is worth it only
// while both firstScans and unscanned are above zero: each one then moves at
// least one skill out of the never-scanned set, so the chain ends, and
// retries alone never start one.
func (r *Runtime) scanSkillOutcomes(ctx context.Context, serverName string, outcomes []skillOutcome) (firstScans, unscanned, pending int) {
	sm := r.storageManager
	budget := r.skills.limits.scanRefresh()
	start := r.skills.limits.now()
	scanned := 0
	for _, retries := range []bool{false, true} {
		for i := range outcomes {
			o := &outcomes[i]
			if !o.usable || !o.needScan || o.scanRetry != retries {
				continue
			}
			skip := func() {
				pending++
				if !retries {
					unscanned++
				}
			}
			if sm == nil || ctx.Err() != nil || !r.skillsEligible(serverName) ||
				(scanned > 0 && r.skills.limits.now().Sub(start) >= budget) {
				skip()
				continue
			}
			texts := make([]skillTextFile, 0, len(o.files))
			readable := true
			for _, f := range o.files {
				if !f.Text || !f.Stored {
					continue
				}
				data, err := sm.GetSkillFile(serverName, f.Digest)
				if err != nil {
					readable = false
					break
				}
				texts = append(texts, skillTextFile{relPath: f.RelPath, data: data})
			}
			if !readable {
				skip()
				continue
			}
			res := r.scanSkillText(ctx, serverName, o.path, texts)
			if res.Incomplete == storage.SkillScanIncompleteInterrupted {
				// Stopped from outside (shutdown): nothing worth recording.
				skip()
				continue
			}
			res.Hash = o.hash
			o.scan = res
			scanned++
			if !retries {
				firstScans++
			}
			if res.Incomplete == storage.SkillScanIncompleteCoverage {
				pending++
			}
		}
	}
	return firstScans, unscanned, pending
}
