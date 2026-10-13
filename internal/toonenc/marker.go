package toonenc

// Marker is the deterministic one-line header that precedes every
// TOON-encoded block (FR-005). It is part of the response contract with
// agents and is asserted byte-for-byte against
// specs/084-toon-output/contracts/marker-format.md.
//
// Strict ASCII by contract: the separator is " - " (space-hyphen-space), NOT
// an em dash, so the marker's byte cost is stable across tokenizers and fully
// counted in the size comparison (FR-003c, FR-004).
const Marker = "[mcpproxy:toon/v1] TOON-encoded JSON (toon-format.org); decode to JSON before reuse - tool arguments must still be sent as JSON."

// AssembleEmission builds the complete encoded emission for a TOON body:
// exactly Marker + "\n" + body. Passthrough blocks never go through this
// function — they carry no marker (FR-005).
func AssembleEmission(body string) string {
	return Marker + "\n" + body
}
