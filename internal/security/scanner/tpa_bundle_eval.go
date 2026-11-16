package scanner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Signature is the exported view of one bundle signatures[] entry, for the
// corpus builder, scan-eval and the activation self-check (spec 101 D-US2-3).
type Signature struct {
	ID         string
	Category   string
	Gating     bool
	Provenance SignatureProvenance
	License    string
	// RuleIDs are "<TPA-id>/<detector>" references into rules[].
	RuleIDs []string
}

// SignatureProvenance is the public disclosure behind a Signature.
type SignatureProvenance struct {
	Source    string
	Published string
	Note      string
}

// LoadBundleCheck compiles raw bundle bytes through the production loader and
// returns the check with its operator-facing info. It is pure: it never touches
// the process-wide active bundle, so tools can score a candidate corpus without
// installing it. Source and Path are left empty for the caller to set.
func LoadBundleCheck(data []byte) (*BundleCheck, BundleInfo, error) {
	b, err := parseBundleManifest(data)
	if err != nil {
		return nil, BundleInfo{}, err
	}
	check, stats, err := compileBundle(b)
	if err != nil {
		return nil, BundleInfo{}, err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	return check, BundleInfo{
		BundleVersion:   b.BundleVersion,
		SchemaVersion:   b.SchemaVersion,
		SignatureCount:  b.SignatureCount,
		GeneratedAt:     b.GeneratedAt,
		RunnableRules:   stats.Runnable,
		SkippedRules:    stats.Skipped,
		DeclaredSkipped: stats.Declared,
		FullDigest:      digest,
		Fingerprint:     shortDigest(digest),
		LoadedAt:        time.Now(),
	}, nil
}

// EmbeddedBundle returns a copy of the corpus compiled into this build.
func EmbeddedBundle() []byte {
	out := make([]byte, len(bundledScannerBundle))
	copy(out, bundledScannerBundle)
	return out
}

// ParseBundleSignatures decodes only the signatures[] section of a bundle,
// without compiling or validating rules. A bundle without the section yields no
// signatures and no error.
func ParseBundleSignatures(data []byte) ([]Signature, error) {
	var b struct {
		Signatures []rawSignature `json:"signatures"`
	}
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parse scanner bundle signatures: %w", err)
	}
	out := make([]Signature, 0, len(b.Signatures))
	for _, s := range b.Signatures {
		out = append(out, Signature{
			ID:         s.ID,
			Category:   s.Category,
			Gating:     s.Gating,
			Provenance: SignatureProvenance(s.Provenance),
			License:    s.License,
			RuleIDs:    append([]string(nil), s.RuleIDs...),
		})
	}
	return out, nil
}
