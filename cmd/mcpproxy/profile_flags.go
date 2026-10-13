package main

// Flag -> config.ProfileConfig builders for `profile create|update|try`
// (Spec 108-g G5, G6, G9). Field names on the wire are the REST JSON names;
// nothing here defines a second spelling.

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// profileFlags holds the flags shared by `profile create` and `profile update`.
type profileFlags struct {
	servers         []string
	title           string
	description     string
	maxTier         string
	unannotated     string
	allow           []string
	deny            []string
	codeExecution   string
	managementTools string
	switchableTo    []string

	// update only
	addServers    []string
	removeServers []string
	addAllow      []string
	removeAllow   []string
	addDeny       []string
	removeDeny    []string
	clears        map[string]*bool
}

// profileClearFields are the --clear-<field> flags of `profile update`.
var profileClearFields = []string{
	"title", "description", "max-tier", "unannotated", "allow", "deny", "classify",
	"code-execution", "management-tools", "switchable-to",
}

func (f *profileFlags) register(cmd *cobra.Command, update bool) {
	fl := cmd.Flags()
	fl.StringSliceVar(&f.servers, "servers", nil, "Comma-separated upstream servers the profile exposes")
	fl.StringVar(&f.title, "title", "", "Display title (at most 80 characters)")
	fl.StringVar(&f.description, "description", "", "Description (at most 500 characters)")
	fl.StringVar(&f.maxTier, "max-tier", "", "Highest tool tier the profile admits: read, write or destructive")
	fl.StringVar(&f.unannotated, "unannotated", "", "Handling of tools with no tier hint: deny, as_write or as_read (as-write and as-read are accepted)")
	fl.StringSliceVar(&f.allow, "allow", nil, "Comma-separated server:tool allow globs")
	fl.StringSliceVar(&f.deny, "deny", nil, "Comma-separated server:tool deny globs (deny beats allow)")
	fl.StringVar(&f.codeExecution, "code-execution", "", "Code execution: on, off or inherit (inherit clears the field)")
	fl.StringVar(&f.managementTools, "management-tools", "", "Management tools (upstream_servers, quarantine_security): on, off or inherit")
	fl.StringSliceVar(&f.switchableTo, "switchable-to", nil, "Comma-separated profiles a session may switch into; an empty value means none")
	if !update {
		return
	}
	fl.StringSliceVar(&f.addServers, "add-server", nil, "Add a server to the profile (repeatable)")
	fl.StringSliceVar(&f.removeServers, "remove-server", nil, "Remove a server from the profile (repeatable)")
	fl.StringSliceVar(&f.addAllow, "add-allow", nil, "Add an allow glob (repeatable)")
	fl.StringSliceVar(&f.removeAllow, "remove-allow", nil, "Remove an allow glob (repeatable)")
	fl.StringSliceVar(&f.addDeny, "add-deny", nil, "Add a deny glob (repeatable)")
	fl.StringSliceVar(&f.removeDeny, "remove-deny", nil, "Remove a deny glob (repeatable)")
	f.clears = map[string]*bool{}
	for _, name := range profileClearFields {
		v := new(bool)
		f.clears[name] = v
		fl.BoolVar(v, "clear-"+name, false, "Clear "+strings.ReplaceAll(name, "-", " ")+" (back to unset)")
	}
}

// parseTriState reads on|off|inherit. inherit yields nil (the field is omitted).
func parseTriState(flag, value string) (*bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true":
		v := true
		return &v, nil
	case "off", "false":
		v := false
		return &v, nil
	case "inherit", "":
		return nil, nil
	}
	return nil, newFlagValidationError("--%s must be on, off or inherit (got %q)", flag, value)
}

// canonicalUnannotated maps the kebab input aliases to the wire spelling. Any
// other value is passed through for the one REST validator to judge.
func canonicalUnannotated(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "as-write":
		return config.ProfileUnannotatedAsWrite
	case "as-read":
		return config.ProfileUnannotatedAsRead
	}
	return strings.TrimSpace(v)
}

// checkConflicts rejects contradictory flag combinations before any request.
func (f *profileFlags) checkConflicts(cmd *cobra.Command) error {
	changed := cmd.Flags().Changed
	type pair struct{ set, other string }
	for _, p := range []pair{
		{"servers", "add-server"}, {"servers", "remove-server"},
		{"allow", "add-allow"}, {"allow", "remove-allow"},
		{"deny", "add-deny"}, {"deny", "remove-deny"},
	} {
		if cmd.Flags().Lookup(p.other) != nil && changed(p.set) && changed(p.other) {
			return newFlagValidationError("--%s cannot be combined with --%s", p.set, p.other)
		}
	}
	for _, name := range profileClearFields {
		if name == "classify" || cmd.Flags().Lookup("clear-"+name) == nil {
			continue
		}
		if *f.clears[name] && changed(name) {
			return newFlagValidationError("--%s cannot be combined with --clear-%s", name, name)
		}
	}
	return nil
}

// apply writes the flags onto p in the order set -> add/remove -> clear.
func (f *profileFlags) apply(cmd *cobra.Command, p *config.ProfileConfig, update bool) error {
	changed := cmd.Flags().Changed
	if changed("servers") {
		p.Servers = append([]string{}, f.servers...)
	}
	if changed("title") {
		p.Title = f.title
	}
	if changed("description") {
		p.Description = f.description
	}
	if changed("max-tier") {
		p.MaxTier = strings.TrimSpace(f.maxTier)
	}
	if changed("unannotated") {
		p.Unannotated = canonicalUnannotated(f.unannotated)
	}
	if changed("allow") {
		ensureTools(p).Allow = append([]string{}, f.allow...)
	}
	if changed("deny") {
		ensureTools(p).Deny = append([]string{}, f.deny...)
	}
	if changed("code-execution") {
		v, err := parseTriState("code-execution", f.codeExecution)
		if err != nil {
			return err
		}
		p.CodeExecution = v
	}
	if changed("management-tools") {
		v, err := parseTriState("management-tools", f.managementTools)
		if err != nil {
			return err
		}
		p.ManagementTools = v
	}
	if changed("switchable-to") {
		list := append([]string{}, f.switchableTo...)
		p.SwitchableTo = &list
	}
	if !update {
		normalizeTools(p)
		return nil
	}

	p.Servers = removeItems(appendUnique(p.Servers, f.addServers), f.removeServers)
	if len(f.addAllow)+len(f.removeAllow) > 0 {
		t := ensureTools(p)
		t.Allow = removeItems(appendUnique(t.Allow, f.addAllow), f.removeAllow)
	}
	if len(f.addDeny)+len(f.removeDeny) > 0 {
		t := ensureTools(p)
		t.Deny = removeItems(appendUnique(t.Deny, f.addDeny), f.removeDeny)
	}

	clear := func(name string) bool { return *f.clears[name] }
	if clear("title") {
		p.Title = ""
	}
	if clear("description") {
		p.Description = ""
	}
	if clear("max-tier") {
		p.MaxTier = ""
	}
	if clear("unannotated") {
		p.Unannotated = ""
	}
	if clear("allow") && p.Tools != nil {
		p.Tools.Allow = nil
	}
	if clear("deny") && p.Tools != nil {
		p.Tools.Deny = nil
	}
	if clear("classify") && p.Tools != nil {
		p.Tools.Classify = nil
	}
	if clear("code-execution") {
		p.CodeExecution = nil
	}
	if clear("management-tools") {
		p.ManagementTools = nil
	}
	if clear("switchable-to") {
		p.SwitchableTo = nil
	}
	normalizeTools(p)
	return nil
}

func ensureTools(p *config.ProfileConfig) *config.ProfileToolRules {
	if p.Tools == nil {
		p.Tools = &config.ProfileToolRules{}
	}
	return p.Tools
}

// normalizeTools drops an empty rule set so it round-trips as "unset".
func normalizeTools(p *config.ProfileConfig) {
	if p.Tools != nil && len(p.Tools.Allow) == 0 && len(p.Tools.Deny) == 0 && len(p.Tools.Classify) == 0 {
		p.Tools = nil
	}
}

func appendUnique(list, add []string) []string {
	seen := map[string]bool{}
	for _, v := range list {
		seen[v] = true
	}
	for _, v := range add {
		if !seen[v] {
			list = append(list, v)
			seen[v] = true
		}
	}
	return list
}

func removeItems(list, remove []string) []string {
	if len(remove) == 0 {
		return list
	}
	drop := map[string]bool{}
	for _, v := range remove {
		drop[v] = true
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		if !drop[v] {
			out = append(out, v)
		}
	}
	return out
}

// draftFields is the list of keys `profile try --set` accepts.
var draftFields = []string{
	"servers", "title", "description", "max_tier", "unannotated",
	"tools.allow", "tools.deny", "tools.classify.<server:tool>",
	"code_execution", "management_tools", "switchable_to",
}

// applyDraftSet applies one `--set key=value` override to a draft profile. Keys
// are the REST JSON field names.
func applyDraftSet(p *config.ProfileConfig, kv string) error {
	key, value, ok := strings.Cut(kv, "=")
	if !ok || strings.TrimSpace(key) == "" {
		return newFlagValidationError("--set expects key=value (got %q)", kv)
	}
	key = strings.TrimSpace(key)
	list := func() []string {
		out := []string{}
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
		return out
	}
	switch {
	case key == "servers":
		p.Servers = list()
	case key == "title":
		p.Title = value
	case key == "description":
		p.Description = value
	case key == "max_tier":
		p.MaxTier = strings.TrimSpace(value)
	case key == "unannotated":
		p.Unannotated = canonicalUnannotated(value)
	case key == "tools.allow":
		ensureTools(p).Allow = list()
	case key == "tools.deny":
		ensureTools(p).Deny = list()
	case strings.HasPrefix(key, "tools.classify."):
		tool := strings.TrimPrefix(key, "tools.classify.")
		if tool == "" {
			return newFlagValidationError("--set tools.classify.<server:tool>=<tier> needs a tool")
		}
		t := ensureTools(p)
		if strings.TrimSpace(value) == "" {
			delete(t.Classify, tool)
		} else {
			if t.Classify == nil {
				t.Classify = map[string]string{}
			}
			t.Classify[tool] = strings.TrimSpace(value)
		}
	case key == "code_execution":
		v, err := parseTriState("set code_execution", value)
		if err != nil {
			return err
		}
		p.CodeExecution = v
	case key == "management_tools":
		v, err := parseTriState("set management_tools", value)
		if err != nil {
			return err
		}
		p.ManagementTools = v
	case key == "switchable_to":
		l := list()
		p.SwitchableTo = &l
	default:
		return newFlagValidationError("unknown draft field %q; valid: %s", key, strings.Join(draftFields, ", "))
	}
	normalizeTools(p)
	return nil
}

// sortedKeys returns a map's keys in order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
