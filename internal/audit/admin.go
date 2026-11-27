package audit

// admin.go — Spec 133: the fourth audit event kind, `admin_action` (one line
// per committed administrative effect). The line contract is
// specs/133-admin-action-audit/contracts/admin-action-line.md; the schema
// block is the `event == admin_action` branch of
// docs/schemas/audit-line-v1.schema.json.
//
// Structural guarantees (FR-016): a value reaches a line only as a sealed
// LiteralValue (admin_fields.go); every label (a name an administrator or a
// caller chose) passes the private-key check, then maskCredential, then the
// 64-character cap; the builder enforces the contract's iff rules and the
// 16 KiB budget and returns an error for any violation.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/mail"
	"sort"
	"strings"
	"time"
)

// MaxAdminLineBytes is the serialized-line budget (contract §Size), not
// counting the newline the sink appends.
const MaxAdminLineBytes = 16 * 1024

// maxLabelRunes is the cap on every name and label (contract §Size).
const maxLabelRunes = 64

// Caps on the lists a line carries (contract §Size).
const (
	maxChanges    = 128
	maxBatchNames = 64
)

// Actions (contract §Actions). The set is closed; NewAdminAction rejects any
// other value. Only some are emitted today; the rest are reserved by the
// schema so later slices add emitters, not vocabulary.
const (
	ActionTokenCreate          = "token.create"
	ActionTokenRotate          = "token.rotate"
	ActionTokenRevoke          = "token.revoke"
	ActionTokenDelete          = "token.delete"
	ActionTokenRepin           = "token.repin"
	ActionClientCreate         = "client.create"
	ActionClientBind           = "client.bind"
	ActionClientKeyRevoke      = "client.key_revoke"
	ActionClientRotateStage    = "client.rotate_stage"
	ActionClientRotateFinalize = "client.rotate_finalize"
	ActionClientRotateRollback = "client.rotate_rollback"
	ActionClientForget         = "client.forget"
	ActionLoginRegister        = "login.register"
	ActionLoginApprove         = "login.approve"
	ActionLoginDeny            = "login.deny"
	ActionLoginIssue           = "login.issue"
	ActionLoginRevoke          = "login.revoke"
	ActionLoginCancel          = "login.cancel"
	ActionSecretSet            = "secret.set"
	ActionSecretDelete         = "secret.delete"
)

// AdminActions is the closed action vocabulary, identical to the schema's
// `action` enum (pinned by TestAdminActionVocabularyMatchesSchema).
var AdminActions = []string{
	"bundle.activate", "bundle.reload", "bundle.rollback", "bundle.unpin",
	"client.bind", "client.create", "client.forget", "client.key_revoke",
	"client.rotate_finalize", "client.rotate_rollback", "client.rotate_stage",
	"config.update", "jwt.issue",
	"login.approve", "login.cancel", "login.deny", "login.issue", "login.register", "login.revoke",
	"profile.classify", "profile.create", "profile.delete", "profile.rename", "profile.set_anonymous", "profile.update",
	"prompt.approve", "prompt.hold",
	"registry.add", "registry.remove", "registry.update",
	"scanner.configure", "scanner.disable", "scanner.enable",
	"secret.delete", "secret.set",
	"server.add", "server.baseline_clear", "server.baseline_set", "server.disable", "server.enable",
	"server.quarantine", "server.remove", "server.review_approve", "server.share", "server.unquarantine", "server.update",
	"skill.approve", "skill.block", "skill.hold", "skill.unblock",
	"token.create", "token.delete", "token.repin", "token.revoke", "token.rotate",
	"tool.approve", "tool.block", "tool.disable", "tool.enable", "tool.hold",
	"user.disable", "user.enable", "user.groups_change", "user.provision", "user.sessions_revoke",
	"user_credential.connect", "user_credential.delete",
	"user_server.add", "user_server.enable", "user_server.remove", "user_server.update",
}

var adminActionSet = func() map[string]bool {
	m := make(map[string]bool, len(AdminActions))
	for _, a := range AdminActions {
		m[a] = true
	}
	return m
}()

// Triggers (contract §Keys): required iff caller.kind is none or internal.
const (
	TriggerConfigFile = "config_file"
	TriggerAdmission  = "admission"
	TriggerPolicy     = "policy"
	TriggerStartup    = "startup"
	TriggerSchedule   = "schedule"
)

var adminTriggers = map[string]bool{
	TriggerConfigFile: true, TriggerAdmission: true, TriggerPolicy: true, TriggerStartup: true, TriggerSchedule: true,
}

// Policies (contract §Keys): required iff trigger is policy.
const (
	PolicyChangedDefinition      = "changed_definition"
	PolicyTrustedBaseline        = "trusted_baseline"
	PolicyScanClean              = "scan_clean"
	PolicyAutoApproveToolChanges = "auto_approve_tool_changes"
	PolicyHashRestored           = "hash_restored"
	PolicyQuarantineNotEnforced  = "quarantine_not_enforced"
	PolicyBaselineInferred       = "baseline_inferred"
	PolicySkillsAuto             = "skills_auto"
	PolicyPromptsAuto            = "prompts_auto"
	PolicyLoginExchange          = "login_exchange"
	PolicyLoginReplaced          = "login_replaced"
	PolicyLoginReplay            = "login_replay"
	PolicyRotationReconcile      = "rotation_reconcile"
	PolicyScannerPull            = "scanner_pull"
)

var adminPolicies = map[string]bool{
	PolicyChangedDefinition: true, PolicyTrustedBaseline: true, PolicyScanClean: true,
	PolicyAutoApproveToolChanges: true, PolicyHashRestored: true, PolicyQuarantineNotEnforced: true,
	PolicyBaselineInferred: true, PolicySkillsAuto: true, PolicyPromptsAuto: true,
	PolicyLoginExchange: true, PolicyLoginReplaced: true, PolicyLoginReplay: true,
	PolicyRotationReconcile: true, PolicyScannerPull: true,
}

// adminCallerKinds are the caller kinds legal on an admin_action line.
var adminCallerKinds = map[string]bool{
	"api_key": true, "socket": true, "stdio": true, "anonymous": true, "agent_token": true,
	"session_user": true, "session_admin": true, "internal": true, "none": true, "cli_offline": true,
}

// backgroundKinds carry no identity key.
var backgroundKinds = map[string]bool{"internal": true, "none": true, "cli_offline": true}

// adminClientSurfaces is the #1842 surface vocabulary (client.surface).
var adminClientSurfaces = map[string]bool{"web": true, "macos": true, "cli": true, "mcp": true, "api": true}

// AdminTarget is the line's `target` object. Build it with a per-kind
// constructor (AgentTokenTarget, ...); the zero value is rejected.
type AdminTarget struct {
	kind string
	// name is a label: private-key check, mask, cap. withheldName forces
	// name_withheld regardless of the label (derived-label rule).
	name         string
	withheldName bool
	ref          string
	ownerUserID  string
}

// AgentTokenTarget names an agent token by its name, its stable audit ref and,
// for a tenant-owned token, its owner.
func AgentTokenTarget(name, ref, ownerUserID string) AdminTarget {
	return AdminTarget{kind: "agent_token", name: name, ref: ref, ownerUserID: ownerUserID}
}

// IsZero reports whether no target was set.
func (t AdminTarget) IsZero() bool { return t.kind == "" }

func (t AdminTarget) build() (map[string]interface{}, error) {
	switch t.kind {
	case "agent_token":
	default:
		return nil, fmt.Errorf("audit.NewAdminAction: unsupported target kind %q", t.kind)
	}
	if !validRef(t.ref) {
		return nil, fmt.Errorf("audit.NewAdminAction: target %s requires a valid ref", t.kind)
	}
	m := map[string]interface{}{"kind": t.kind, "ref": t.ref}
	if name, ok := renderLabel(t.name); ok && !t.withheldName {
		m["name"] = name
	} else {
		m["name_withheld"] = true
	}
	if t.ownerUserID != "" {
		owner, ok := renderLabel(t.ownerUserID)
		if !ok {
			return nil, fmt.Errorf("audit.NewAdminAction: owner_user_id is not a label")
		}
		m["owner_user_id"] = owner
	}
	return m, nil
}

// validRef matches the opaque audit ref shape the proxy mints
// (auth.NewAuditRef): a three-letter kind, '_', then 8–32 base32 characters.
func validRef(r string) bool {
	if len(r) < 12 || len(r) > 36 || r[3] != '_' {
		return false
	}
	for i := 0; i < 3; i++ {
		if r[i] < 'a' || r[i] > 'z' {
			return false
		}
	}
	for _, c := range r[4:] {
		lower := c >= 'a' && c <= 'z'
		digit := c >= '0' && c <= '9'
		if !lower && !digit {
			return false
		}
	}
	// A ref the whole-line sanitizer would rewrite is not stable.
	_, hit := maskAllSpans(r)
	return !hit
}

// AdminActionInput builds one admin_action line. Every value comes from the
// committed effect (Target, Changes) or from the trusted request context
// (Caller, Origin, Source, RequestID, client keys) — never from an argument.
type AdminActionInput struct {
	// Ts is the commit time of the effect.
	Ts time.Time
	// RequestID is the id the matching activity row carries; "" when no
	// request caused the effect.
	RequestID string
	Origin    string // local|socket
	Source    string // api|mcp|internal
	Caller    Caller

	ClientName    string
	ClientVersion string
	ClientIP      string
	ClientSurface string // web|macos|cli|mcp|api

	Profile string

	Action  string
	Target  AdminTarget
	Changes []Change

	Trigger string
	Policy  string
}

// NewAdminAction builds and structurally validates one admin_action line.
func NewAdminAction(in AdminActionInput) (Line, error) {
	if y := in.Ts.UTC().Year(); y < 0 || y > 9999 {
		return Line{}, fmt.Errorf("audit.NewAdminAction: ts year %d is outside the four-digit wire layout", y)
	}
	if !adminActionSet[in.Action] {
		return Line{}, fmt.Errorf("audit.NewAdminAction: unknown action %q", in.Action)
	}
	if in.Target.IsZero() {
		return Line{}, fmt.Errorf("audit.NewAdminAction: target is required")
	}
	switch in.Origin {
	case "local", "socket":
	default:
		return Line{}, fmt.Errorf("audit.NewAdminAction: invalid origin %q", in.Origin)
	}
	switch in.Source {
	case "api", "mcp", "internal":
	default:
		return Line{}, fmt.Errorf("audit.NewAdminAction: invalid source %q", in.Source)
	}
	kind := in.Caller.Kind
	if !adminCallerKinds[kind] {
		return Line{}, fmt.Errorf("audit.NewAdminAction: invalid caller.kind %q", kind)
	}
	needsTrigger := kind == "none" || kind == "internal"
	if needsTrigger != (in.Trigger != "") {
		return Line{}, fmt.Errorf("audit.NewAdminAction: trigger is required iff caller.kind is none or internal (kind %q, trigger %q)", kind, in.Trigger)
	}
	if in.Trigger != "" && !adminTriggers[in.Trigger] {
		return Line{}, fmt.Errorf("audit.NewAdminAction: invalid trigger %q", in.Trigger)
	}
	if (in.Trigger == TriggerPolicy) != (in.Policy != "") {
		return Line{}, fmt.Errorf("audit.NewAdminAction: policy is required iff trigger is policy")
	}
	if in.Policy != "" && !adminPolicies[in.Policy] {
		return Line{}, fmt.Errorf("audit.NewAdminAction: invalid policy %q", in.Policy)
	}
	if (kind == "none" || kind == "cli_offline") && in.RequestID != "" {
		return Line{}, fmt.Errorf("audit.NewAdminAction: caller.kind %s never carries request_id", kind)
	}
	// A request entered through an ingress, so its lines are never
	// source internal (that value means "no initiating request").
	if in.RequestID != "" && in.Source == "internal" {
		return Line{}, fmt.Errorf("audit.NewAdminAction: a line with a request_id has the source of its ingress, not internal")
	}
	// No request: no ingress, so source internal and origin local.
	if in.RequestID == "" && (in.Source != "internal" || in.Origin != "local") {
		return Line{}, fmt.Errorf("audit.NewAdminAction: a line with no request has source internal and origin local")
	}
	// A request caller always has a request: its line joins the activity row
	// by request_id (FR-009). Only the background kinds may omit it.
	if !backgroundKinds[kind] && in.RequestID == "" {
		return Line{}, fmt.Errorf("audit.NewAdminAction: caller.kind %s requires request_id", kind)
	}
	clientRecordDelete := in.Action == ActionTokenDelete && in.Target.kind == "client"
	if strings.HasPrefix(in.Action, "token.") && in.Target.kind != "agent_token" && !clientRecordDelete {
		return Line{}, fmt.Errorf("audit.NewAdminAction: %s requires an agent_token target", in.Action)
	}
	if err := checkRequiredPrefixes(in.Action, in.Changes); err != nil {
		return Line{}, err
	}
	if in.ClientSurface != "" && !adminClientSurfaces[in.ClientSurface] {
		return Line{}, fmt.Errorf("audit.NewAdminAction: invalid client.surface %q", in.ClientSurface)
	}

	caller, err := buildAdminCaller(in.Caller)
	if err != nil {
		return Line{}, err
	}
	target, err := in.Target.build()
	if err != nil {
		return Line{}, err
	}

	f := map[string]interface{}{
		"schema_version": 1,
		"ts":             in.Ts.UTC().Format(tsLayout),
		"event":          "admin_action",
		"origin":         in.Origin,
		"source":         in.Source,
		"caller":         caller,
		"action":         in.Action,
		"target":         target,
		"outcome":        "success",
	}
	if in.RequestID != "" {
		if containsPrivateKey(in.RequestID) {
			// No position can carry a private-key block: the record is
			// rejected (and counted), never serialized.
			return Line{}, fmt.Errorf("audit.NewAdminAction: request_id carries a private-key block")
		}
		masked, hit := maskAllSpans(in.RequestID)
		if hit {
			noteFieldMask()
			f["request_id_masked"] = true
		}
		f["request_id"] = truncateField(masked)
	}
	setOptString(f, "trigger", in.Trigger)
	setOptString(f, "policy", in.Policy)
	if in.Profile != "" {
		if p, ok := renderLabel(in.Profile); ok {
			f["profile"] = p
		}
	}
	client := map[string]interface{}{}
	if v, ok := renderMetaLabel(in.ClientName); ok {
		client["name"] = v
	}
	if v, ok := renderMetaLabel(in.ClientVersion); ok {
		client["version"] = v
	}
	// client.ip is an IP address or nothing: a Unix-socket peer label or any
	// other text never reaches the line.
	if ip := net.ParseIP(in.ClientIP); ip != nil {
		client["ip"] = ip.String()
	}
	if in.ClientSurface != "" {
		client["surface"] = in.ClientSurface
	}
	if len(client) > 0 {
		f["client"] = client
	}

	if len(in.Changes) > 0 {
		sorted := sortChanges(in.Changes)
		entries := make([]interface{}, 0, len(sorted))
		for _, c := range sorted {
			e, err := c.entry()
			if err != nil {
				return Line{}, err
			}
			entries = append(entries, e)
		}
		if len(entries) > maxChanges {
			f["changes_truncated"] = true
			f["changes_count"] = len(entries)
			entries = entries[:maxChanges]
		}
		f["changes"] = entries
	}

	if err := fitAdminBudget(f, len(in.Changes)); err != nil {
		return Line{}, err
	}
	// The caps can drop entries: the required display prefix must survive
	// them, or the line is rejected rather than written incomplete.
	if err := checkRenderedPrefixes(in.Action, f); err != nil {
		return Line{}, err
	}
	return Line{fields: f}, nil
}

// fitAdminBudget drops trailing change entries, then target.names, until the
// serialized line fits MaxAdminLineBytes (contract §Size).
func fitAdminBudget(f map[string]interface{}, fullChanges int) error {
	for {
		raw, err := json.Marshal(f)
		if err != nil {
			return fmt.Errorf("audit.NewAdminAction: %w", err)
		}
		if len(raw) <= MaxAdminLineBytes {
			return nil
		}
		if entries, ok := f["changes"].([]interface{}); ok && len(entries) > 0 {
			f["changes_truncated"] = true
			f["changes_count"] = fullChanges
			// One entry at a time from the end, so no more is dropped than
			// the budget requires (at most 128 re-serializations).
			f["changes"] = entries[:len(entries)-1]
			continue
		}
		if tgt, ok := f["target"].(map[string]interface{}); ok {
			if _, has := tgt["names"]; has {
				delete(tgt, "names")
				tgt["truncated"] = true
				continue
			}
		}
		return fmt.Errorf("audit.NewAdminAction: line exceeds %d bytes with every list dropped", MaxAdminLineBytes)
	}
}

// containsPrivateKey reports a private-key block header anywhere in s
// (contract §Target: no byte of such a label is serialized).
func containsPrivateKey(s string) bool {
	if !strings.Contains(s, "-----BEGIN") {
		return false
	}
	return strings.Contains(s, "PRIVATE KEY") || strings.Contains(s, "PGP PRIVATE KEY BLOCK")
}

// renderLabel applies the label order: private-key check on the original,
// mask, then the 64-rune cap. ok=false means the label must not be
// serialized at all (its position decides the withheld shape).
func renderLabel(s string) (string, bool) {
	if s == "" || containsPrivateKey(s) {
		return "", false
	}
	// Control characters are replaced first: JSON escapes them (\u000e), and
	// the whole-line sanitizer, which reads the escaped form, could otherwise
	// match a credential shape across the escape and rewrite the label.
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s)
	masked, hit := maskAllSpans(s)
	if hit {
		noteFieldMask()
	}
	r := []rune(masked)
	if len(r) > maxLabelRunes {
		r = r[:maxLabelRunes]
	}
	return string(r), true
}

// renderMetaLabel is renderLabel for optional metadata (client.name,
// client.version): a private-key label is omitted.
func renderMetaLabel(s string) (string, bool) { return renderLabel(s) }

func buildAdminCaller(c Caller) (map[string]interface{}, error) {
	m := map[string]interface{}{"kind": c.Kind}
	if backgroundKinds[c.Kind] || c.Kind == "api_key" || c.Kind == "socket" || c.Kind == "stdio" || c.Kind == "anonymous" {
		if c.UserID != "" || c.UserEmail != "" || c.EmailHash != "" || c.Role != "" || c.Provider != "" ||
			c.TokenName != "" || c.TokenPrefix != "" || c.ProfilePin != "" || c.Ref != "" || c.TokenNameWithheld {
			return nil, fmt.Errorf("audit.NewAdminAction: caller.kind %s carries no identity", c.Kind)
		}
		return m, nil
	}
	if c.EmailHash != "" {
		return nil, fmt.Errorf("audit.NewAdminAction: email_hash is auth_event only")
	}
	if c.UserID != "" {
		id, ok := renderLabel(c.UserID)
		if !ok {
			return nil, fmt.Errorf("audit.NewAdminAction: caller.user_id is not a label")
		}
		m["user_id"] = id
		if c.Role == "" || c.Provider == "" {
			return nil, fmt.Errorf("audit.NewAdminAction: caller.user_id requires role and provider")
		}
		if c.Role != "admin" && c.Role != "user" {
			return nil, fmt.Errorf("audit.NewAdminAction: invalid caller.role %q", c.Role)
		}
		switch c.Provider {
		case "google", "github", "microsoft", "oidc":
		default:
			return nil, fmt.Errorf("audit.NewAdminAction: invalid caller.provider %q", c.Provider)
		}
		m["role"] = c.Role
		m["provider"] = c.Provider
		if email, ok := validEmail(c.UserEmail); ok {
			m["user_email"] = email
		}
	}
	switch c.Kind {
	case "agent_token":
		if c.TokenPrefix == "" {
			return nil, fmt.Errorf("audit.NewAdminAction: agent_token caller requires token_prefix")
		}
		if _, ok := TokenPrefix(c.TokenPrefix).render(); !ok {
			return nil, fmt.Errorf("audit.NewAdminAction: invalid caller.token_prefix")
		}
		m["token_prefix"] = c.TokenPrefix
		if name, ok := renderLabel(c.TokenName); ok && !c.TokenNameWithheld {
			m["token_name"] = name
		} else {
			if !validRef(c.Ref) {
				return nil, fmt.Errorf("audit.NewAdminAction: a withheld token_name requires caller.ref")
			}
			m["token_name_withheld"] = true
			m["ref"] = c.Ref
		}
		if pin, ok := renderLabel(c.ProfilePin); ok {
			m["profile_pin"] = pin
		}
	case "session_admin", "session_user":
		if c.UserID == "" {
			return nil, fmt.Errorf("audit.NewAdminAction: %s caller requires user_id", c.Kind)
		}
		want := "admin"
		if c.Kind == "session_user" {
			want = "user"
		}
		if c.Role != want {
			return nil, fmt.Errorf("audit.NewAdminAction: %s caller requires role %s", c.Kind, want)
		}
		if c.TokenName != "" || c.TokenPrefix != "" {
			return nil, fmt.Errorf("audit.NewAdminAction: session caller carries no token fields")
		}
	}
	return m, nil
}

// validEmail returns the masked, capped email only when it is still a valid
// bare address (contract §Caller).
func validEmail(s string) (string, bool) {
	if s == "" {
		return "", false
	}
	out, ok := renderLabel(s)
	if !ok {
		return "", false
	}
	a, err := mail.ParseAddress(out)
	if err != nil || a.Address != out || a.Name != "" {
		return "", false
	}
	return out, true
}

// AdminActionSorted returns the vocabulary sorted (tests).
func AdminActionSorted() []string {
	out := append([]string(nil), AdminActions...)
	sort.Strings(out)
	return out
}

// requiredPrefixSides names the display-prefix sides each lifecycle action
// must carry on /token_prefix (contract §Keys, changes row).
var requiredPrefixSides = map[string]struct{ before, after bool }{
	ActionTokenCreate:          {after: true},
	ActionClientCreate:         {after: true},
	ActionTokenRotate:          {before: true, after: true},
	ActionClientRotateFinalize: {before: true, after: true},
	ActionTokenRevoke:          {before: true},
	ActionClientKeyRevoke:      {before: true},
}

func checkRequiredPrefixes(action string, changes []Change) error {
	n := 0
	for _, c := range changes {
		if c.path.sortKey() == FieldTokenPrefix.sortKey() {
			n++
		}
	}
	if n > 1 {
		return fmt.Errorf("audit.NewAdminAction: at most one /token_prefix entry")
	}
	want, ok := requiredPrefixSides[action]
	if !ok {
		return nil
	}
	for _, c := range changes {
		if c.path.sortKey() != FieldTokenPrefix.sortKey() || c.withheld != "" {
			continue
		}
		if want.before == (c.before != nil) && want.after == (c.after != nil) {
			return nil
		}
	}
	return fmt.Errorf("audit.NewAdminAction: %s requires its /token_prefix entry with exactly the sides the contract names", action)
}

// checkRenderedPrefixes re-checks the required /token_prefix entry on the
// rendered (capped) changes.
func checkRenderedPrefixes(action string, f map[string]interface{}) error {
	want, ok := requiredPrefixSides[action]
	if !ok {
		want = struct{ before, after bool }{}
	}
	entries, _ := f["changes"].([]interface{})
	n := 0
	for _, e := range entries {
		if m, _ := e.(map[string]interface{}); m["field"] == "/token_prefix" {
			n++
		}
	}
	if n > 1 {
		// Paths are compared as rendered: a deeper path that collapses onto
		// /token_prefix counts too.
		return fmt.Errorf("audit.NewAdminAction: at most one rendered /token_prefix entry")
	}
	for _, e := range entries {
		m, _ := e.(map[string]interface{})
		if m["field"] != "/token_prefix" {
			continue
		}
		_, hasBefore := m["before"]
		_, hasAfter := m["after"]
		if want.before == hasBefore && want.after == hasAfter {
			return nil
		}
	}
	if !ok {
		return nil
	}
	return fmt.Errorf("audit.NewAdminAction: %s lost its /token_prefix entry to the size caps", action)
}
