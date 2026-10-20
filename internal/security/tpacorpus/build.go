package tpacorpus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// ManifestFile is the builder's per-corpus settings file inside the source tree.
const ManifestFile = "manifest.json"

var (
	signatureIDPattern = regexp.MustCompile(`^TPA-[0-9]{4}-[0-9]{4}$`)
	sourceFilePattern  = regexp.MustCompile(`^TPA-[0-9]{4}-[0-9]{4}\.json$`)

	// reservedKeys are the top-level keys the builder owns; a manifest header
	// must not shadow them.
	reservedKeys = []string{
		"bundle_version", "generated_at", "generated_from", "rules",
		"schema_version", "signature_count", "signatures", "skipped",
	}
)

// Options tune a build.
type Options struct {
	// Exclude drops the named signatures (and their rules and skipped entries)
	// from the output; signature_count is recomputed. Used by the gate's negative
	// control. An id that matches no source is an error.
	Exclude []string
	// SourceDateEpoch, when set, overrides the manifest's source_date_epoch pin
	// (the CLI feeds the SOURCE_DATE_EPOCH environment variable through here).
	// The build never reads the wall clock.
	SourceDateEpoch *int64
}

type manifestDoc struct {
	BundleVersion   string         `json:"bundle_version"`
	SchemaVersion   string         `json:"schema_version"`
	GeneratedFrom   string         `json:"generated_from"`
	SourceDateEpoch *int64         `json:"source_date_epoch"`
	Header          map[string]any `json:"header"`
}

type sourceProvenance struct {
	Source    string `json:"source"`
	Published string `json:"published"`
	Note      string `json:"note"`
}

// sourceDoc is one signatures/TPA-YYYY-NNNN.json file: the signature metadata
// plus the rules and skipped entries that implement it. Rules and skipped are
// kept as generic maps so they re-encode with sorted keys and verbatim numbers.
type sourceDoc struct {
	ID         string           `json:"id"`
	Category   string           `json:"category"`
	Gating     bool             `json:"gating"`
	Provenance sourceProvenance `json:"provenance"`
	License    string           `json:"license"`
	Rules      []map[string]any `json:"rules"`
	Skipped    []map[string]any `json:"skipped"`
}

// Build compiles a source tree (manifest.json plus one TPA-YYYY-NNNN.json per
// signature) into the bundle bytes. The output is deterministic: rules[] sort by
// (id, detector), signatures[] by id, object keys alphabetically, and
// generated_at comes from a pinned epoch, never the wall clock. It refuses
// signatures without provenance or with a licence outside the allowlist or an
// unknown class, and the result must pass the production loader.
func Build(fsys fs.FS, opts Options) ([]byte, error) {
	var m manifestDoc
	if err := decodeFile(fsys, ManifestFile, &m); err != nil {
		return nil, err
	}
	if m.BundleVersion == "" || m.SchemaVersion == "" {
		return nil, fmt.Errorf("%s: bundle_version and schema_version are required", ManifestFile)
	}
	for k := range m.Header {
		if slices.Contains(reservedKeys, k) {
			return nil, fmt.Errorf("%s: header key %q is owned by the builder", ManifestFile, k)
		}
	}
	epoch := m.SourceDateEpoch
	if opts.SourceDateEpoch != nil {
		epoch = opts.SourceDateEpoch
	}
	if epoch == nil {
		return nil, fmt.Errorf("%s: source_date_epoch is required (the build never uses the wall clock)", ManifestFile)
	}
	if *epoch < 0 {
		return nil, fmt.Errorf("source_date_epoch %d is negative", *epoch)
	}

	docs, err := loadSources(fsys)
	if err != nil {
		return nil, err
	}
	docs, err = applyExclude(docs, opts.Exclude)
	if err != nil {
		return nil, err
	}

	var rules, skipped, signatures []any
	type ruleKey struct{ id, detector string }
	seen := map[ruleKey]bool{}
	for _, d := range docs {
		ruleIDs := make([]string, 0, len(d.Rules))
		for i, r := range d.Rules {
			detector, err := stampOwner(r, d.ID, "detector")
			if err != nil {
				return nil, fmt.Errorf("%s rules[%d]: %w", d.ID, i, err)
			}
			k := ruleKey{d.ID, detector}
			if seen[k] {
				return nil, fmt.Errorf("%s: duplicate rule detector %q", d.ID, detector)
			}
			seen[k] = true
			ruleIDs = append(ruleIDs, d.ID+"/"+detector)
			rules = append(rules, r)
		}
		sk := map[string]bool{}
		for i, s := range d.Skipped {
			detector, err := stampOwner(s, d.ID, "detector")
			if err != nil {
				return nil, fmt.Errorf("%s skipped[%d]: %w", d.ID, i, err)
			}
			if sk[detector] {
				return nil, fmt.Errorf("%s: duplicate skipped detector %q", d.ID, detector)
			}
			sk[detector] = true
			skipped = append(skipped, s)
		}
		slices.Sort(ruleIDs)
		prov := map[string]any{"source": d.Provenance.Source, "published": d.Provenance.Published}
		if d.Provenance.Note != "" {
			prov["note"] = d.Provenance.Note
		}
		signatures = append(signatures, map[string]any{
			"id":         d.ID,
			"category":   d.Category,
			"gating":     d.Gating,
			"provenance": prov,
			"license":    d.License,
			"rule_ids":   ruleIDs,
		})
	}
	sortByIDDetector(rules)
	sortByIDDetector(skipped)
	if rules == nil {
		rules = []any{}
	}
	if skipped == nil {
		skipped = []any{}
	}

	top := make(map[string]any, len(reservedKeys)+len(m.Header))
	for k, v := range m.Header {
		top[k] = v
	}
	top["bundle_version"] = m.BundleVersion
	top["schema_version"] = m.SchemaVersion
	if m.GeneratedFrom != "" {
		top["generated_from"] = m.GeneratedFrom
	}
	top["generated_at"] = time.Unix(*epoch, 0).UTC().Format(time.RFC3339)
	top["signature_count"] = len(signatures)
	top["signatures"] = signatures
	top["rules"] = rules
	top["skipped"] = skipped

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(top); err != nil {
		return nil, fmt.Errorf("encode bundle: %w", err)
	}
	out := buf.Bytes()

	if _, _, err := scanner.LoadBundleCheck(out); err != nil {
		return nil, fmt.Errorf("built bundle is rejected by the production loader: %w", err)
	}
	return out, nil
}

// loadSources reads and policy-checks every per-signature source, sorted by id.
func loadSources(fsys fs.FS) ([]*sourceDoc, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read source tree: %w", err)
	}
	var docs []*sourceDoc
	seen := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == ManifestFile || !strings.HasSuffix(name, ".json") {
			continue
		}
		if !sourceFilePattern.MatchString(name) {
			return nil, fmt.Errorf("%s: unexpected JSON file (sources are named TPA-YYYY-NNNN.json)", name)
		}
		var d sourceDoc
		if err := decodeFile(fsys, name, &d); err != nil {
			return nil, err
		}
		if want := strings.TrimSuffix(name, ".json"); d.ID != want {
			return nil, fmt.Errorf("%s: id %q does not match the file name", name, d.ID)
		}
		if err := checkSource(&d); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if seen[d.ID] {
			return nil, fmt.Errorf("%s: duplicate signature id", name)
		}
		seen[d.ID] = true
		docs = append(docs, &d)
	}
	if len(docs) == 0 {
		return nil, errors.New("source tree holds no TPA-YYYY-NNNN.json signatures")
	}
	slices.SortFunc(docs, func(a, b *sourceDoc) int { return strings.Compare(a.ID, b.ID) })
	return docs, nil
}

// checkSource enforces the corpus policy a single source must meet to ship.
func checkSource(d *sourceDoc) error {
	if !signatureIDPattern.MatchString(d.ID) {
		return fmt.Errorf("id %q does not match TPA-YYYY-NNNN", d.ID)
	}
	if !isClass(d.Category) {
		return fmt.Errorf("unknown class %q (see tpacorpus.Classes)", d.Category)
	}
	src := strings.TrimSpace(d.Provenance.Source)
	if src == "" {
		return errors.New("provenance.source is empty (every signature needs a public disclosure)")
	}
	if u, err := url.Parse(src); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("provenance.source %q is not an http(s) URL", src)
	}
	if _, err := time.Parse("2006-01-02", d.Provenance.Published); err != nil {
		return fmt.Errorf("provenance.published %q is not YYYY-MM-DD", d.Provenance.Published)
	}
	if strings.TrimSpace(d.License) == "" {
		return errors.New("license is missing (an unlicensed pattern is not redistributable)")
	}
	if !isAllowedLicence(d.License) {
		return fmt.Errorf("license %q is not in the redistributable allowlist %v", d.License, LicenceAllowlist)
	}
	if len(d.Rules) == 0 {
		return errors.New("signature has no rules")
	}
	return nil
}

func applyExclude(docs []*sourceDoc, exclude []string) ([]*sourceDoc, error) {
	if len(exclude) == 0 {
		return docs, nil
	}
	drop := make(map[string]bool, len(exclude))
	for _, id := range exclude {
		drop[id] = true
	}
	kept := docs[:0:0]
	for _, d := range docs {
		if drop[d.ID] {
			delete(drop, d.ID)
			continue
		}
		kept = append(kept, d)
	}
	if len(drop) > 0 {
		missing := make([]string, 0, len(drop))
		for id := range drop {
			missing = append(missing, id)
		}
		slices.Sort(missing)
		return nil, fmt.Errorf("exclude: no source for %v", missing)
	}
	if len(kept) == 0 {
		return nil, errors.New("exclude removed every signature")
	}
	return kept, nil
}

// stampOwner makes the entry's id the owning signature's id (filling it when the
// source omits it) and returns the entry's detector.
func stampOwner(entry map[string]any, owner, detectorKey string) (string, error) {
	if v, ok := entry["id"]; ok {
		if s, _ := v.(string); s != owner {
			return "", fmt.Errorf("id %v does not match the owning signature %s", v, owner)
		}
	}
	entry["id"] = owner
	d, _ := entry[detectorKey].(string)
	if d == "" {
		return "", fmt.Errorf("missing %s", detectorKey)
	}
	return d, nil
}

// sortByIDDetector orders rules[] / skipped[] entries by (id, detector), the
// order the load layer requires.
func sortByIDDetector(entries []any) {
	key := func(v any) (string, string) {
		m := v.(map[string]any)
		id, _ := m["id"].(string)
		det, _ := m["detector"].(string)
		return id, det
	}
	slices.SortStableFunc(entries, func(a, b any) int {
		ai, ad := key(a)
		bi, bd := key(b)
		if c := strings.Compare(ai, bi); c != 0 {
			return c
		}
		return strings.Compare(ad, bd)
	})
}

// decodeFile strictly decodes one JSON file: unknown fields are errors (a typo
// such as "licence" must not silently drop a required field) and numbers keep
// their source spelling so the bundle bytes stay stable.
func decodeFile(fsys fs.FS, name string, v any) error {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: trailing data after the JSON document", name)
	}
	return nil
}
