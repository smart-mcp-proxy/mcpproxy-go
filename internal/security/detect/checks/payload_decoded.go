package checks

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"unicode/utf8"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// PayloadDecoded is a HARD check (FR-008) that decodes base64/hex blobs embedded
// in a tool's description or schema and flags ONLY when the decoded bytes are a
// shell/exfiltration command — `curl … | sh`, `wget … | sh`, `chmod`, `rm -rf`,
// a pipe-to-shell, or a raw IP:port reverse-shell target. Benign encoded data
// (an icon, a JSON config) decodes to non-matching/non-printable bytes and is
// never flagged, so the false-positive rate stays near zero. Evidence is the
// decoded content, surfaced so an operator sees exactly what was hidden.
type PayloadDecoded struct{}

// ID implements detect.Check.
func (*PayloadDecoded) ID() string { return "payload.decoded" }

var (
	// base64 run long enough to carry a command (≥24 chars ≈ ≥18 bytes); shorter
	// tokens are skipped to avoid flagging ordinary identifiers.
	base64Re = regexp.MustCompile(`[A-Za-z0-9+/]{24,}={0,2}`)
	// hex run ≥16 nibbles (≥8 bytes); even length enforced at decode time.
	hexRe = regexp.MustCompile(`(?:[0-9a-fA-F]{2}){8,}`)

	// shellRe matches a decoded payload that is an install/exfil command. IP:port
	// digits are unaffected by the case-insensitive flag.
	shellRe = regexp.MustCompile(`(?i)\bcurl\b.*\|\s*(?:ba)?sh\b|` +
		`\bwget\b.*\|\s*(?:ba)?sh\b|` +
		`\|\s*(?:ba)?sh\b|` +
		`\bchmod\b|` +
		`\brm\s+-rf\b|` +
		`/bin/(?:ba)?sh\b|` +
		`\b(?:\d{1,3}\.){3}\d{1,3}:\d{2,5}\b`)
)

// Inspect implements detect.Check.
func (c *PayloadDecoded) Inspect(tool detect.ToolView, _ detect.RegistryView) []detect.Signal {
	text := tool.Description
	if len(tool.InputSchema) > 0 {
		text += " " + string(tool.InputSchema)
	}
	if len(tool.OutputSchema) > 0 {
		text += " " + string(tool.OutputSchema)
	}
	if text == "" {
		return nil
	}

	for _, cand := range base64Re.FindAllString(text, -1) {
		if dec, ok := decodeBase64(cand); ok {
			if sig, hit := c.matchPayload(string(dec)); hit {
				return []detect.Signal{sig}
			}
		}
	}
	for _, cand := range hexRe.FindAllString(text, -1) {
		if len(cand)%2 != 0 {
			cand = cand[:len(cand)-1]
		}
		if raw, err := hex.DecodeString(cand); err == nil {
			if sig, hit := c.matchPayload(string(raw)); hit {
				return []detect.Signal{sig}
			}
		}
	}
	return nil
}

// matchPayload returns a hard signal when decoded text is printable and matches
// a shell/exfil pattern.
func (c *PayloadDecoded) matchPayload(decoded string) (detect.Signal, bool) {
	return shellPayloadSignal(c.ID(), "decoded payload", "An encoded blob decodes to", decoded)
}

// shellPayloadSignal is the decode-then-confirm rule shared with the
// declared-marker reconstruction check (Spec 122 FR-035): a HARD signal when
// the recovered text is printable and is a shell/exfiltration command. label
// prefixes the evidence ("decoded payload", "reconstructed payload") and lead
// opens the detail sentence.
func shellPayloadSignal(checkID, label, lead, decoded string) (detect.Signal, bool) {
	return shellSignalWith(isPrintableText, checkID, label, lead, decoded)
}

// shellPayloadSignalText is shellPayloadSignal for TEXT the author wrote
// (a reconstructed payload): any valid UTF-8 without control runes is
// printable, so a non-ASCII comment ("# café") never hides the command
// (Sol r3 P2). Decoded blobs keep the stricter ASCII rule.
func shellPayloadSignalText(checkID, label, lead, text string) (detect.Signal, bool) {
	return shellSignalWith(isPrintableUTF8, checkID, label, lead, text)
}

func shellSignalWith(printable func(string) bool, checkID, label, lead, decoded string) (detect.Signal, bool) {
	if !printable(decoded) || !shellRe.MatchString(decoded) {
		return detect.Signal{}, false
	}
	return detect.Signal{
		CheckID:    checkID,
		Tier:       detect.TierHard,
		ThreatType: detect.ThreatMaliciousCode,
		Confidence: 0.97,
		Evidence:   detect.CapEvidence(label + ": " + decoded),
		Detail:     fmt.Sprintf("%s a shell/exfiltration command: %q", lead, truncateForDetail(decoded)),
	}, true
}

// decodeBase64 tries standard then raw (unpadded) base64.
func decodeBase64(s string) ([]byte, bool) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, true
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return b, true
	}
	return nil, false
}

// isPrintableText reports whether decoded bytes are plausible printable ASCII
// text (so binary blobs like images/icons are skipped, holding FP near zero).
func isPrintableText(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		printable := (b >= 0x20 && b <= 0x7E) || b == '\t' || b == '\n' || b == '\r'
		if !printable {
			return false
		}
	}
	return true
}

// isPrintableUTF8 reports non-empty valid UTF-8 text. Control runes do not
// veto it: an author can put an ESC sequence in a shell comment after the
// command ("… | sh # \x1b[0m", Sol r9 P2); the shell rule decides on the
// command itself, and evidence rendering escapes the control runes anyway.
func isPrintableUTF8(s string) bool {
	return s != "" && utf8.ValidString(s)
}

func truncateForDetail(s string) string {
	const n = 80
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
