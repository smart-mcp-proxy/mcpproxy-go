package arms

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/bench"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// TronName is the registry key of the TRON named-class schema-dedup arm.
const TronName = "tron_dedup"

// TronArm is a minimal, honest in-tree implementation of TRON's named-class
// schema deduplication (research D2, arXiv:2605.29676 — no Go implementation
// exists upstream). Mechanism: each tool's input schema is canonicalized
// (CanonicalJSON — sorted keys, compact); byte-identical canonical schemas
// share ONE class definition, emitted once in the EncodeListing preamble as
//
//	class C<id> = {…canonical schema…}
//
// and each tool entry references its class by name:
//
//	name|description|C<id>
//
// Deliberate deviations from the paper, documented for honesty:
//
//   - Dedup key is the EXACT canonical schema bytes, not a structural "shape"
//     with per-property descriptions abstracted away. This is lossless (no
//     description is dropped or truncated — LowerBound stays false) but
//     conservative: schemas that differ only in embedded descriptions do not
//     merge, so measured savings are what exact dedup achieves, not the
//     paper's upper bound.
//   - Class names are content-addressed (C + first 8 hex of SHA-256 of the
//     canonical schema) rather than sequential C1/C2, so the per-tool index
//     mapping (EncodeIndexMetadata) and the listing agree without shared
//     state, independent of tool order. Content addressing costs a few more
//     tokens per reference than sequential names would — a conservative bias.
//   - Every distinct schema gets a class, including schemas used by a single
//     tool. Singletons pay the class overhead ("class … = " plus the
//     reference) instead of inlining; this keeps the format uniform and the
//     honest cost visible rather than optimized away.
//   - Field delimiters (| and newlines) are not escaped: the arm measures
//     token cost of the format shape, and descriptions are preserved
//     verbatim because escaping would perturb the very token counts under
//     measurement.
//
// Amortization lives ONLY in EncodeListing (contract rule 6): EncodeTool
// returns the non-deduped single form name|description|<canonical schema>, so
// per-tool means remain comparable across arms.
type TronArm struct{}

// NewTron returns the tron_dedup arm.
func NewTron() *TronArm { return &TronArm{} }

// Name implements Arm.
func (*TronArm) Name() string { return TronName }

// IndexAltering implements Arm: the schema body moves into a shared class
// definition, so the per-tool params text the index ingests becomes a class
// reference instead of the schema JSON (see EncodeIndexMetadata).
func (*TronArm) IndexAltering() bool { return true }

// LowerBound implements Arm: descriptions (tool-level and inside schemas) are
// preserved verbatim.
func (*TronArm) LowerBound() bool { return false }

// tronClassName returns the content-addressed class name for a canonical
// schema: "C" + first 8 hex chars of its SHA-256. 32 bits of content address
// make accidental collisions negligible at tool-corpus scale, and EncodeListing
// detects and rejects any collision explicitly (contract rule 2) rather than
// ever merging distinct schemas silently.
func tronClassName(canonicalSchema string) string {
	sum := sha256.Sum256([]byte(canonicalSchema))
	return "C" + hex.EncodeToString(sum[:4])
}

// tronCanonicalSchema canonicalizes a tool's schema, wrapping errors with the
// tool identity for skip accounting.
func tronCanonicalSchema(t bench.Tool) (string, error) {
	canon, err := CanonicalJSON(t.Schema)
	if err != nil {
		return "", fmt.Errorf("tool %s: %w", t.ToolID, err)
	}
	return canon, nil
}

// EncodeTool implements Arm: the non-deduped single form
// name|description|<canonical schema> (name|description when the tool has no
// schema). Class machinery appears only in EncodeListing.
func (*TronArm) EncodeTool(t bench.Tool) (string, error) {
	s := t.Name + "|" + t.Description
	if len(t.Schema) > 0 {
		canon, err := tronCanonicalSchema(t)
		if err != nil {
			return "", err
		}
		s += "|" + canon
	}
	return s, nil
}

// EncodeListing implements Arm: a class-definition preamble (one line per
// distinct canonical schema, in first-appearance order) followed by tool
// entries referencing their class by name. Determinism: class identity is
// content-addressed, preamble order is input order — no map iteration.
func (*TronArm) EncodeListing(ts []bench.Tool) (string, error) {
	classOrder := make([]string, 0, len(ts))      // class names, first-appearance order
	classBody := make(map[string]string, len(ts)) // class name -> canonical schema

	entries := make([]string, 0, len(ts))
	for _, t := range ts {
		entry := t.Name + "|" + t.Description
		if len(t.Schema) > 0 {
			canon, err := tronCanonicalSchema(t)
			if err != nil {
				return "", err
			}
			name := tronClassName(canon)
			if body, seen := classBody[name]; seen {
				if body != canon {
					// Two distinct schemas hashing to one class name would
					// silently merge tools — fail explicitly instead.
					return "", fmt.Errorf("tron: class name collision on %s (tool %s)", name, t.ToolID)
				}
			} else {
				classBody[name] = canon
				classOrder = append(classOrder, name)
			}
			entry += "|" + name
		}
		entries = append(entries, entry)
	}

	tools := strings.Join(entries, listingSeparator)
	if len(classOrder) == 0 {
		return tools, nil
	}
	preamble := make([]string, 0, len(classOrder))
	for _, name := range classOrder {
		preamble = append(preamble, "class "+name+" = "+classBody[name])
	}
	return strings.Join(preamble, "\n") + listingSeparator + tools, nil
}

// EncodeIndexMetadata implements Arm: under TRON the schema body lives in a
// shared class definition that is not attached to any single tool, so the
// per-tool params text the production index ingests is the content-addressed
// class reference — Name/ServerName/Description are unchanged. Content
// addressing makes this mapping deterministic per tool, with no listing
// context, and guarantees it names the same class EncodeListing defines.
func (*TronArm) EncodeIndexMetadata(t bench.Tool) (config.ToolMetadata, error) {
	meta := config.ToolMetadata{
		Name:        t.Name,
		ServerName:  t.Server,
		Description: t.Description,
	}
	if len(t.Schema) > 0 {
		canon, err := tronCanonicalSchema(t)
		if err != nil {
			return config.ToolMetadata{}, err
		}
		meta.ParamsJSON = `{"$class":"` + tronClassName(canon) + `"}`
	}
	return meta, nil
}

func init() {
	MustRegister(NewTron())
}
