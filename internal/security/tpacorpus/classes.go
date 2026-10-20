// Package tpacorpus builds the embedded TPA signature bundle from per-signature
// sources and enforces the corpus policy (spec 101 US2): provenance, licence
// allowlist, class vocabulary and the eval pair every gating signature owes.
//
// The load-time integrity checks (referential integrity, gating derivation)
// live in the scanner package so operators can still load private corpora; the
// checks here run at build and in CI only.
package tpacorpus

// Classes is the signature class vocabulary: the value of signatures[].category
// and the suffix of the eval dataset's hard-negative id prefix hn_<class>_.
// No class may be a prefix of another, or hn_<class>_ prefixes would collide.
//
// tpa_agent_config_persistence is the deterministic negative control (spec 101
// D-US2-7): the gate must breach when its signature is removed.
var Classes = []string{
	"tpa_hidden_instruction_block",
	"tpa_comment_directive",
	"tpa_tool_shadowing",
	"tpa_cross_server_override",
	"tpa_exfil_redirect",
	"tpa_sensitive_file_coaxing",
	"tpa_rug_pull_phrasing",
	"tpa_concealment_directive",
	"tpa_agent_config_persistence",
	"tpa_markdown_image_beacon",
}

// LicenceAllowlist is the set of SPDX ids a signature may carry (FR-012).
// CC-BY-SA-4.0 is deliberately absent: its share-alike terms could attach to the
// MIT-licensed binary that embeds the corpus.
var LicenceAllowlist = []string{"CC0-1.0", "CC-BY-4.0", "MIT", "Apache-2.0"}

func isClass(c string) bool {
	for _, k := range Classes {
		if k == c {
			return true
		}
	}
	return false
}

func isAllowedLicence(l string) bool {
	for _, k := range LicenceAllowlist {
		if k == l {
			return true
		}
	}
	return false
}
