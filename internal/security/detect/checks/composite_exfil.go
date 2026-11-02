// SPDX-License-Identifier: Apache-2.0
//
// The indicator vocabulary below is adapted from NVIDIA SkillSpector
// (src/skillspector/yara_rules/agent_skills.yar, rule
// agent_skill_credential_exfiltration_webhook, and the prose patterns of
// src/skillspector/nodes/analyzers/static_patterns_data_exfiltration.py,
// reviewed at commit 5cb9385ab0dffa974088ee21c4376c7e560b2a52).
// Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// Modifications Copyright (c) 2026 the mcpproxy-go authors.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not
// use this file except in compliance with the License. You may obtain a copy
// of the License at http://www.apache.org/licenses/LICENSE-2.0. Unless
// required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//
// Changes from upstream: the YARA conjunction (any secret_* AND any send_* AND
// any collector_* anywhere in a file) is rewritten in Go with LOCALITY — the
// three indicators must sit in one metadata field within 400 bytes and at
// most one sentence boundary — and with an access relation for credential
// nouns, so authentication ("using your API token") is not credential access.

package checks

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// CompositeExfil is a SOFT check (Spec 122 US5): credential access + a
// transmission action + an external collection destination in one coherent
// region of one field. Co-occurrence is not proof of data flow, so the
// finding is a review warning that names the three indicators.
type CompositeExfil struct{}

// ID implements detect.Check.
func (*CompositeExfil) ID() string { return "exfil.composite" }

const (
	compositeBase          = 0.55
	compositeCommodityBase = 0.65
	compositeMinConfidence = 0.35 // every descriptive or quoted match (≤ 0.325) is dropped (FR-053)
	compositeWindowBytes   = 400
	compositeAccessWords   = 6
	// compositeMaxIndicators caps the indicators collected per class and
	// field (document order): the first undiscounted commodity triple ends
	// the search anyway, and the cap keeps a field dense in indicators linear.
	compositeMaxIndicators = 256
)

// Indicator classes, matched on NORMALIZED text (lowercased, stemmed: "sends"
// → "send", "credentials" → "credential", "services" → "servic").
var (
	// credentialEnvRe always counts as access: an environment or credential
	// FILE read needs no verb.
	credentialEnvRe = regexp.MustCompile(`(?:os\.environ|process\.env|printenv|\benv \||getenv|\$\{?[a-z_]*(?:key|secret|token|password|passwd|credential)[a-z_]*\}?|\b(?:aws_secret_access_key|openai_api_key|anthropic_api_key|github_token|hf_token|nvidia_inference_key)\b|~?/?\.ssh\b|\bid_rsa\b|\bid_ed25519\b|~?/?\.aws\b|\.env\b|\benv file|\.netrc\b|\.pgpass\b|\bkeychain\b|\bkube ?config\b|\.docker/config|\bauthorized_key)`)
	// credentialNounRe counts only with an access relation (FR-051).
	credentialNounRe     = regexp.MustCompile(`\b(?:environment variabl\w*|env var\w*|api[ _-]?key\w*|access key\w*|secret key\w*|secret access key\w*|access token\w*|auth token\w*|bearer token\w*|session token\w*|refresh token\w*|token\w*|password\w*|passwd\w*|passphras\w*|secret\w*|credential\w*|private key\w*|ssh key\w*|cooki\w*|client secret\w*|signing key\w*)\b`)
	credentialAccessVerb = regexp.MustCompile(`^(?:read|load|collect|gather|harvest|extract|dump|grab|enumerat|list|scan|find|print|echo|cat|fetch|retriev|obtain|copy|steal|exfiltrat|leak|captur|log|record|inclu\w*|attach|append|forward|send|post|upload|transmit|submit|email|ship|deliver|relay|beacon|smuggle|expos|reveal|disclos|export|save|store|stash|pull|snapshot|serializ)\w*$`)
	credentialAuthPrep   = regexp.MustCompile(`^(?:using|with|via|through|authenticat\w*|authorization|authoriz\w*|bearer|header\w*|configur\w*|provid\w*|suppli\w*|requir\w*|valid|validat\w*|verif\w*|check\w*|rotat\w*|refresh\w*|expir\w*|revok\w*|issu\w*|generat\w*|creat\w*)$`)
	transmissionRe       = regexp.MustCompile(`\b(?:send\w*|post\w*|upload\w*|transmit\w*|forward\w*|exfiltrat\w*|submit\w*|beacon\w*|ship\w*|deliver\w*|relay\w*|smuggle\w*|leak\w*|curl (?:-x post|-d|--data\w*|-f |--form)|wget --post\w*|requests\.(?:post|put)|httpx\.(?:post|put)|axios\.post|fetch\(|http post|post request|webhook\w*)\b`)
	commodityHostRe      = regexp.MustCompile(`(?:discord(?:app)?\.com/api/webhook\w*|api\.telegram\.org/bot|hooks\.slack\.com/servic\w*|webhook\.site|requestbin|pipedream\.net|ngrok(?:-free)?\.(?:app|io|dev)|burpcollaborator|oastify\.com|oast\.(?:fun|live|me|pro|site|online)|interact\.sh|pastebin\.com|transfer\.sh|requestcatcher|beeceptor|hookbin|mockbin|canarytokens|dnslog|postb\.in|localtunnel|serveo\.net|loca\.lt|trycloudflare\.com|webhookrelay|hookdeck|smee\.io)`)
	destinationRe        = regexp.MustCompile(`(?:h(?:tt|xx)ps?://(?:\d{1,3}\.){3}\d{1,3}|\bhxxps?://|\[\.\]|\b(?:external|remote|attacker[\w']*|adversar[\w']*|third[- ]?part[\w']*|collector[\w']*|off-?site|c2|command[- ]and[- ]control|exfil[\w']*|malicious|rogue|unknown|untrust\w*) (?:server|host|endpoint|url|collector|machine|servic\w*|api|address|location|sink|site|webhook|listener|domain)\b|\b(?:our|their|my|his|her) (?:server|endpoint|collector|webhook|url|api|listener)\b)`)
	compositeSentenceEnd = regexp.MustCompile(`[.!?;](?:\s|$)`)
)

type exfilIndicator struct {
	start, end int
	text       string
	commodity  bool
}

// Inspect implements detect.Check.
func (c *CompositeExfil) Inspect(tool detect.ToolView, _ detect.RegistryView) []detect.Signal {
	fields, _ := detect.FieldStrings(tool)
	bestConf := 0.0
	var best [3]exfilIndicator
	bestPath := ""
	for _, f := range fields {
		if strings.TrimSpace(f.Text) == "" {
			continue
		}
		// Public-key files and credential basenames that are only part of a
		// longer name are blanked first, as the phrase-injection families
		// do ("upload ~/.ssh/id_ed25519.pub", Sol r3 P2).
		// … and so is a secret noun qualified as metadata ("the token expiry",
		// "the key length"): metadata is not the credential (Sol r9 P2).
		t := maskSecretMetadataNouns(maskPublicKeyFiles(detect.Normalize(f.Text)))
		dests := destinationIndicators(t)
		if len(dests) == 0 {
			continue
		}
		creds := credentialIndicators(t)
		if len(creds) == 0 {
			continue
		}
		posText := strings.ReplaceAll(t, ";", ".")
		sortIndicators(creds)
		sortIndicators(dests)
		for _, loc := range transmissionRe.FindAllStringIndex(t, compositeMaxIndicators) {
			if bestConf >= compositeCommodityBase {
				break // nothing can score higher
			}
			tr := exfilIndicator{start: loc[0], end: loc[1], text: t[loc[0]:loc[1]]}
			cr, ok1 := nearestIndicator(creds, tr)
			de, ok2 := nearestIndicator(dests, tr)
			if !ok1 || !ok2 {
				continue
			}
			lo := min(cr.start, min(tr.start, de.start))
			hi := max(cr.end, max(tr.end, de.end))
			if hi-lo > compositeWindowBytes || len(compositeSentenceEnd.FindAllStringIndex(t[lo:hi], -1)) > 1 {
				continue
			}
			base := compositeBase
			if de.commodity {
				base = compositeCommodityBase
			}
			conf := base * detect.ClassifyPosition(posText, tr.start).Discount()
			if conf < compositeMinConfidence {
				continue
			}
			if conf > bestConf {
				bestConf = conf
				best = [3]exfilIndicator{cr, tr, de}
				bestPath = f.Path
			}
		}
	}
	if bestConf < compositeMinConfidence {
		return nil
	}
	where := ""
	if bestPath != string(detect.SpanFieldDescription) {
		where = fmt.Sprintf(" [field %s]", bestPath)
	}
	return []detect.Signal{{
		CheckID:    c.ID(),
		Tier:       detect.TierSoft,
		ThreatType: detect.ThreatExfiltration,
		Confidence: detect.ClampConfidence(bestConf),
		Evidence:   detect.CapEvidence(fmt.Sprintf("credential access (%q) + transmission (%q) + external destination (%q)", best[0].text, best[1].text, best[2].text)),
		Detail:     fmt.Sprintf("Description combines credential access (%q), a transmission action (%q) and an external destination (%q) in one passage — co-occurrence, not proven data flow; surfaced for review.%s", best[0].text, best[1].text, best[2].text, where),
	}}
}

// credentialIndicators returns the credential-access indicators of t:
// environment/file reads unconditionally, credential nouns only with an
// access verb closer than any authentication word in the six words before.
func credentialIndicators(t string) []exfilIndicator {
	var out []exfilIndicator
	for _, loc := range credentialEnvRe.FindAllStringIndex(t, compositeMaxIndicators) {
		// An environment or credential-file read is access on its own, unless
		// the words before it make it an authentication source ("using an
		// API token from process.env", Sol r2 P2).
		if credentialGovernedByAuth(t, loc[0]) {
			continue
		}
		out = append(out, exfilIndicator{start: loc[0], end: loc[1], text: t[loc[0]:loc[1]]})
	}
	for _, loc := range credentialNounRe.FindAllStringIndex(t, compositeMaxIndicators) {
		if credentialNounHasAccess(t, loc[0]) {
			out = append(out, exfilIndicator{start: loc[0], end: loc[1], text: t[loc[0]:loc[1]]})
		}
	}
	return out
}

// credentialNounHasAccess walks the words before the noun, nearest first:
// an authentication word reached first rules the noun out; an access verb
// reached first rules it in.
// credentialGovernedByAuth reports an authentication word before the
// indicator that is closer than any access verb.
func credentialGovernedByAuth(t string, start int) bool {
	before := t[:start]
	if i := strings.LastIndexAny(before, ".!?;"); i >= 0 {
		before = before[i+1:]
	}
	words := strings.FieldsFunc(before, func(r rune) bool { return r == ' ' || r == ',' || r == '(' || r == ')' })
	for k := len(words) - 1; k >= 0 && len(words)-k <= compositeAccessWords; k-- {
		if credentialAuthPrep.MatchString(words[k]) {
			return true
		}
		if credentialAccessVerb.MatchString(words[k]) {
			return false
		}
	}
	return false
}

func credentialNounHasAccess(t string, nounStart int) bool {
	before := t[:nounStart]
	if i := strings.LastIndexAny(before, ".!?;"); i >= 0 {
		before = before[i+1:]
	}
	words := strings.FieldsFunc(before, func(r rune) bool { return r == ' ' || r == ',' || r == '(' || r == ')' })
	for k := len(words) - 1; k >= 0 && len(words)-k <= compositeAccessWords; k-- {
		w := words[k]
		if credentialAuthPrep.MatchString(w) {
			return false
		}
		if credentialAccessVerb.MatchString(w) {
			return true
		}
	}
	return false
}

func destinationIndicators(t string) []exfilIndicator {
	var out []exfilIndicator
	for _, loc := range commodityHostRe.FindAllStringIndex(t, compositeMaxIndicators) {
		out = append(out, exfilIndicator{start: loc[0], end: loc[1], text: t[loc[0]:loc[1]], commodity: true})
	}
	for _, loc := range destinationRe.FindAllStringIndex(t, compositeMaxIndicators) {
		out = append(out, exfilIndicator{start: loc[0], end: loc[1], text: t[loc[0]:loc[1]]})
	}
	return out
}

func sortIndicators(set []exfilIndicator) {
	sort.Slice(set, func(i, j int) bool {
		if set[i].start != set[j].start {
			return set[i].start < set[j].start
		}
		return set[i].end < set[j].end
	})
}

// nearestIndicator picks the indicator closest to ref from a set sorted by
// start offset (binary search, then the few neighbours on either side),
// commodity hosts first on ties. Linear per call would make a field dense in
// indicators quadratic.
func nearestIndicator(set []exfilIndicator, ref exfilIndicator) (exfilIndicator, bool) {
	if len(set) == 0 {
		return exfilIndicator{}, false
	}
	i := sort.Search(len(set), func(k int) bool { return set[k].start >= ref.start })
	best, bestDist := exfilIndicator{}, -1
	consider := func(in exfilIndicator) {
		d := 0
		switch {
		case in.end <= ref.start:
			d = ref.start - in.end
		case in.start >= ref.end:
			d = in.start - ref.end
		}
		if bestDist < 0 || d < bestDist || (d == bestDist && in.commodity && !best.commodity) {
			best, bestDist = in, d
		}
	}
	// Up to four neighbours on each side cover overlapping and commodity
	// ties; anything farther is farther by construction.
	for k := i - 4; k <= i+3; k++ {
		if k >= 0 && k < len(set) {
			consider(set[k])
		}
	}
	return best, bestDist >= 0
}
