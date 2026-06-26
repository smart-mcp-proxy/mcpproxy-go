package checks

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// CapabilityMismatch is a SOFT check (FR-009, US2) that flags a gap between what
// a tool *declares* it does and what it *implies* it touches:
//
//   - Declared-vs-implied: a tool whose declared purpose is pure computation or
//     string manipulation (name/lead like "add", "to_uppercase") that
//     nevertheless references a sensitive resource it has no business touching
//     (~/.ssh, /etc/passwd, an external URL, a shell). A calculator reading
//     id_rsa is a classic capability-mismatch exfiltration tell.
//   - Unexplained data-sink param: a free-form input named like an exfiltration
//     channel ("sidenote", "scratchpad") that the description never explains —
//     the model is steered to stuff stolen data into it.
//
// The declared category is taken from the tool NAME and its leading sentence,
// NOT the full description, so an attacker's benign cover sentence still anchors
// the declaration while the smuggled access in the rest of the text is treated
// as implied. Tools that legitimately declare file/network/system access are
// therefore NOT flagged for touching those resources (FR-009 MUST-NOT).
//
// Being soft, a hit raises a finding for review and never auto-quarantines.
type CapabilityMismatch struct{}

// ID implements detect.Check.
func (*CapabilityMismatch) ID() string { return "capability.mismatch" }

const (
	mismatchConfidence = 0.55
	dataSinkConfidence = 0.5
)

// Category keyword sets. IO categories (file/network/system) take precedence so
// a tool that genuinely declares resource access is never flagged for using it.
var (
	fileWords    = []string{"file", "path", "dir", "folder", "read", "write", "load", "save", "open", "document", "filesystem"}
	networkWords = []string{"http", "url", "fetch", "download", "upload", "request", "web", "api", "curl", "wget"}
	systemWords  = []string{"exec", "shell", "command", "process", "terminal", "spawn", "subprocess", "script"}
	computeWords = []string{"add", "sum", "subtract", "minus", "multiply", "divide", "calc", "math", "arithmetic", "average", "count", "modulo", "power", "sqrt", "mean", "round", "compute"}
	stringWords  = []string{"string", "upper", "lower", "concat", "reverse", "trim", "replace", "encode", "decode", "length", "substring", "split", "join", "format", "case", "slug"}
)

// sensitiveMarkers are concrete resource references a pure compute/string tool
// has no reason to touch. Written to match NORMALIZED text (lowercased, lightly
// stemmed — e.g. ".aws/credentials" → ".aws/credential").
var sensitiveMarkers = []string{
	".ssh", "id_rsa", "id_ed25519", "/etc/passwd", "/etc/shadow", ".aws/credential",
	".aws", "private key", "keychain", ".netrc", ".npmrc", ".git-credential",
	"authorized_key", ".pgpass", "kube/config", "/.config/gcloud",
	"http://", "https://", "/bin/sh", "/bin/bash", "subprocess", "exfiltrat",
}

// sinkParamNames are input parameter names that read as free-form exfiltration
// channels rather than genuine tool inputs.
var sinkParamNames = map[string]struct{}{
	"sidenote": {}, "side_note": {}, "scratchpad": {}, "scratch": {},
	"thoughts": {}, "thought": {}, "reasoning": {}, "memo": {}, "exfil": {},
	"secret_note": {}, "debug_info": {}, "extra_context": {}, "notes_to_self": {},
	"hidden_note": {}, "annotation": {}, "annotations": {},
}

// Inspect implements detect.Check. It emits at most one signal per tool,
// preferring the capability-mismatch signal over an unexplained data-sink.
func (c *CapabilityMismatch) Inspect(tool detect.ToolView, _ detect.RegistryView) []detect.Signal {
	declared := declaredCategory(tool)
	text := tool.NormalizedText

	// Declared-vs-implied mismatch: a compute/string tool touching a sensitive
	// resource.
	if declared == "compute" || declared == "string" {
		if marker, ok := firstMarker(text); ok {
			return []detect.Signal{{
				CheckID:    c.ID(),
				Tier:       detect.TierSoft,
				ThreatType: detect.ThreatExfiltration,
				Confidence: mismatchConfidence,
				Evidence:   detect.CapEvidence(marker),
				Detail: fmt.Sprintf("Tool declares a %s capability yet references %q — a resource it has no declared reason to access.",
					declared, marker),
			}}
		}
	}

	// Unexplained data-sink parameter.
	if param, ok := unexplainedSinkParam(tool); ok {
		return []detect.Signal{{
			CheckID:    c.ID(),
			Tier:       detect.TierSoft,
			ThreatType: detect.ThreatExfiltration,
			Confidence: dataSinkConfidence,
			Evidence:   detect.CapEvidence(param),
			Detail: fmt.Sprintf("Input parameter %q reads as a free-form data sink and is never explained in the description — a likely exfiltration channel.",
				param),
		}}
	}

	return nil
}

// declaredCategory infers the tool's declared purpose from its name first, then
// its leading sentence. Returns "" when unknown.
func declaredCategory(tool detect.ToolView) string {
	if cat := categoryFromText(strings.ToLower(tool.Name)); cat != "" {
		return cat
	}
	lead := strings.ToLower(tool.Description)
	if i := strings.IndexByte(lead, '.'); i > 0 {
		lead = lead[:i]
	}
	return categoryFromText(lead)
}

// categoryFromText classifies free text into a capability category. IO
// categories are checked first so they win over an incidental compute word.
func categoryFromText(s string) string {
	switch {
	case containsAny(s, fileWords):
		return "file"
	case containsAny(s, networkWords):
		return "network"
	case containsAny(s, systemWords):
		return "system"
	case containsAny(s, computeWords):
		return "compute"
	case containsAny(s, stringWords):
		return "string"
	default:
		return ""
	}
}

func containsAny(hay string, subs []string) bool {
	for _, s := range subs {
		if strings.Contains(hay, s) {
			return true
		}
	}
	return false
}

// firstMarker returns the first sensitive marker present in text, scanning in
// declaration order for determinism.
func firstMarker(text string) (string, bool) {
	for _, m := range sensitiveMarkers {
		if strings.Contains(text, m) {
			return m, true
		}
	}
	return "", false
}

// unexplainedSinkParam returns the first (alphabetically) input parameter whose
// name reads as a data sink AND is not mentioned in the description. Parsing is
// total: a malformed schema yields no parameters rather than an error.
func unexplainedSinkParam(tool detect.ToolView) (string, bool) {
	if len(tool.InputSchema) == 0 {
		return "", false
	}
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(tool.InputSchema, &doc); err != nil {
		return "", false
	}
	names := make([]string, 0, len(doc.Properties))
	for name := range doc.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	desc := strings.ToLower(tool.Description)
	for _, name := range names {
		if _, isSink := sinkParamNames[strings.ToLower(name)]; !isSink {
			continue
		}
		// "Explained" = the description references the param name. Checked against
		// the description only (NOT the schema, which always contains the name).
		if strings.Contains(desc, strings.ToLower(name)) {
			continue
		}
		return name, true
	}
	return "", false
}
