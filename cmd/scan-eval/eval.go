package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
)

// detectorSensitiveData is the id of the deterministic, in-process
// sensitive-data/secret detector bridged in this PR (Gate-2 approved scope).
// Docker bundled scanners are a deferred opt-in extension point (--scanners).
const detectorSensitiveData = "sensitive-data"

// corpusEntry mirrors one item of contracts/security-corpus.schema.json.
type corpusEntry struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Label       string `json:"label"`
	Category    string `json:"category"`
	Provenance  struct {
		Source  string `json:"source"`
		License string `json:"license"`
	} `json:"provenance"`
}

// corpus is the D2 security corpus document. corpus_version/version are
// optional; the schema only mandates entries. Unknown fields are tolerated so
// the tool stays dataset-agnostic across corpus revisions.
type corpus struct {
	CorpusVersion string        `json:"corpus_version"`
	Version       string        `json:"version"`
	Entries       []corpusEntry `json:"entries"`
}

// resolvedVersion returns the corpus version for echoing into the verdict
// report, preferring corpus_version, then version, else "unknown".
func (c *corpus) resolvedVersion() string {
	switch {
	case c.CorpusVersion != "":
		return c.CorpusVersion
	case c.Version != "":
		return c.Version
	default:
		return "unknown"
	}
}

// detectionView is the per-detection projection emitted in verdicts. It drops
// detector-internal fields (location, is_likely_example) the scorer does not
// need, keeping the contract minimal.
type detectionView struct {
	Type     string `json:"type"`
	Category string `json:"category"`
	Severity string `json:"severity"`
}

// detectorVerdict is one detector's call on one entry.
type detectorVerdict struct {
	Detector    string          `json:"detector"`
	Flagged     bool            `json:"flagged"`
	MaxSeverity string          `json:"max_severity"`
	Detections  []detectionView `json:"detections"`
}

// verdictEntry echoes ground truth and carries every detector's verdict.
type verdictEntry struct {
	ID       string            `json:"id"`
	Label    string            `json:"label"`
	Category string            `json:"category"`
	Verdicts []detectorVerdict `json:"verdicts"`
}

// verdictReport is the top-level output (contracts/scan-verdict.schema.json),
// the contract consumed by the Python SecurityScorer (B3).
type verdictReport struct {
	CorpusVersion string         `json:"corpus_version"`
	Detectors     []string       `json:"detectors"`
	Entries       []verdictEntry `json:"entries"`
}

// loadCorpus reads and decodes a D2 security corpus JSON file. A read/parse
// failure or an empty entry set is a config error (callers map it to exit 4).
func loadCorpus(path string) (*corpus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading corpus %q: %w", path, err)
	}
	var c corpus
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing corpus %q: %w", path, err)
	}
	if len(c.Entries) == 0 {
		return nil, fmt.Errorf("corpus %q has no entries", path)
	}
	return &c, nil
}

// evaluate runs every corpus entry's description through the detector and
// projects the result into the verdict contract. Output ordering follows the
// corpus order and the detector's deterministic pattern order, so repeated
// runs over an unchanged corpus are byte-identical (INV-5).
func evaluate(c *corpus, detector *security.Detector) *verdictReport {
	report := &verdictReport{
		CorpusVersion: c.resolvedVersion(),
		Detectors:     []string{detectorSensitiveData},
		Entries:       make([]verdictEntry, 0, len(c.Entries)),
	}

	for _, e := range c.Entries {
		// The corpus stores the tool description text; scan it as a response
		// payload (the detector treats arguments/response identically).
		res := detector.Scan("", e.Description)

		v := detectorVerdict{
			Detector:    detectorSensitiveData,
			Flagged:     res.Detected,
			MaxSeverity: res.MaxSeverity(),
			Detections:  make([]detectionView, 0, len(res.Detections)),
		}
		for _, d := range res.Detections {
			v.Detections = append(v.Detections, detectionView{
				Type:     d.Type,
				Category: d.Category,
				Severity: d.Severity,
			})
		}

		report.Entries = append(report.Entries, verdictEntry{
			ID:       e.ID,
			Label:    e.Label,
			Category: e.Category,
			Verdicts: []detectorVerdict{v},
		})
	}

	return report
}
