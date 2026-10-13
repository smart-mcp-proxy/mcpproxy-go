package runtime

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

// ProfileChange is one attributed profile mutation derived from a config
// write (Spec 108 FR-030): what `profile_change` activity records are built
// from. Diff carries the changed field names only, never a secret-bearing
// value: a profile holds none, and anonymous_profile is a name.
type ProfileChange struct {
	Kind            profile.ChangeKind
	Profile         string
	PreviousProfile string
	Diff            map[string]interface{}
}

// ConfigDiff is the profile-level difference one MutateConfig applied. It is
// empty when the write touched nothing profile-related.
type ConfigDiff struct {
	Changes []ProfileChange
}

// ChangeHint lets a service that performs ONE named operation (rename, delete
// with reassign, classify, set anonymous) say so, so the write yields exactly
// that operation's single record instead of the create+delete pair (and the
// cascaded switchable_to edits) a plain snapshot diff would show (F8). A zero
// Kind means "derive the records from the diff".
type ChangeHint struct {
	Kind            profile.ChangeKind
	Profile         string
	PreviousProfile string
	Diff            map[string]interface{}
}

// diffProfiles derives the profile changes between two desired configs
// (F8). A hint overrides the derivation.
func diffProfiles(before, after *config.Config, hint ChangeHint) []ProfileChange {
	if hint.Kind != "" {
		return []ProfileChange{{Kind: hint.Kind, Profile: hint.Profile, PreviousProfile: hint.PreviousProfile, Diff: hint.Diff}}
	}
	var out []ProfileChange
	byName := func(c *config.Config) map[string]config.ProfileConfig {
		m := map[string]config.ProfileConfig{}
		if c != nil {
			for _, p := range c.Profiles {
				if _, dup := m[p.Name]; !dup {
					m[p.Name] = p
				}
			}
		}
		return m
	}
	oldByName, newByName := byName(before), byName(after)

	// Capacity from one side only: summing both lengths trips CodeQL's
	// allocation-overflow check, and append grows the slice as needed.
	names := make([]string, 0, len(oldByName))
	seen := map[string]bool{}
	for n := range oldByName {
		names, seen[n] = append(names, n), true
	}
	for n := range newByName {
		if !seen[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		o, hadOld := oldByName[n]
		nw, hasNew := newByName[n]
		switch {
		case hadOld && !hasNew:
			out = append(out, ProfileChange{Kind: profile.ChangeDelete, Profile: n, PreviousProfile: n})
		case !hadOld && hasNew:
			out = append(out, ProfileChange{Kind: profile.ChangeCreate, Profile: n})
		default:
			if profileJSON(o) != profileJSON(nw) {
				out = append(out, ProfileChange{Kind: profile.ChangeUpdate, Profile: n, PreviousProfile: n, Diff: diffProfileFields(o, nw)})
			}
		}
	}

	oldAnon, newAnon := "", ""
	if before != nil {
		oldAnon = before.AnonymousProfile
	}
	if after != nil {
		newAnon = after.AnonymousProfile
	}
	if oldAnon != newAnon {
		out = append(out, ProfileChange{
			Kind: profile.ChangeAnonymous, Profile: newAnon, PreviousProfile: oldAnon,
			Diff: map[string]interface{}{"anonymous_profile": map[string]interface{}{"from": oldAnon, "to": newAnon}},
		})
	}
	return out
}

func profileJSON(p config.ProfileConfig) string {
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// diffProfileFields names the fields that differ between two versions of one
// profile: {from,to} for scalars, {added,removed} for lists. It never carries
// anything but profile policy (no secret-bearing field exists on a profile).
func diffProfileFields(o, n config.ProfileConfig) map[string]interface{} {
	d := map[string]interface{}{}
	scalar := func(field string, from, to interface{}) {
		if !reflect.DeepEqual(from, to) {
			d[field] = map[string]interface{}{"from": from, "to": to}
		}
	}
	list := func(field string, from, to []string) {
		added, removed := setDiff(from, to)
		if len(added) > 0 || len(removed) > 0 {
			d[field] = map[string]interface{}{"added": added, "removed": removed}
		}
	}
	scalar("title", o.Title, n.Title)
	scalar("description", o.Description, n.Description)
	scalar("max_tier", o.MaxTier, n.MaxTier)
	scalar("unannotated", o.Unannotated, n.Unannotated)
	scalar("code_execution", boolPtr(o.CodeExecution), boolPtr(n.CodeExecution))
	scalar("management_tools", boolPtr(o.ManagementTools), boolPtr(n.ManagementTools))
	list("servers", o.Servers, n.Servers)
	list("switchable_to", derefList(o.SwitchableTo), derefList(n.SwitchableTo))
	if (o.SwitchableTo == nil) != (n.SwitchableTo == nil) {
		d["switchable_to_set"] = map[string]interface{}{"from": o.SwitchableTo != nil, "to": n.SwitchableTo != nil}
	}
	var oa, od, na, nd []string
	var oc, nc map[string]string
	if o.Tools != nil {
		oa, od, oc = o.Tools.Allow, o.Tools.Deny, o.Tools.Classify
	}
	if n.Tools != nil {
		na, nd, nc = n.Tools.Allow, n.Tools.Deny, n.Tools.Classify
	}
	list("tools.allow", oa, na)
	list("tools.deny", od, nd)
	if !reflect.DeepEqual(nonNilMap(oc), nonNilMap(nc)) {
		changed := map[string]interface{}{}
		for k, v := range nc {
			if ov, ok := oc[k]; !ok || ov != v {
				changed[k] = map[string]interface{}{"from": oc[k], "to": v}
			}
		}
		for k, v := range oc {
			if _, ok := nc[k]; !ok {
				changed[k] = map[string]interface{}{"from": v, "to": ""}
			}
		}
		d["tools.classify"] = changed
	}
	return d
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func boolPtr(p *bool) interface{} {
	if p == nil {
		return nil
	}
	return *p
}

func derefList(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}

// setDiff returns the members added to and removed from a list (sorted).
func setDiff(from, to []string) (added, removed []string) {
	in := func(list []string) map[string]bool {
		m := make(map[string]bool, len(list))
		for _, v := range list {
			m[v] = true
		}
		return m
	}
	f, t := in(from), in(to)
	for v := range t {
		if !f[v] {
			added = append(added, v)
		}
	}
	for v := range f {
		if !t[v] {
			removed = append(removed, v)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	if added == nil {
		added = []string{}
	}
	if removed == nil {
		removed = []string{}
	}
	return added, removed
}

// writeProfileChanges writes one `profile_change` record per change (FR-030).
func (r *Runtime) writeProfileChanges(ctx context.Context, actor Actor, changes []ProfileChange) {
	sm := r.StorageManager()
	if sm == nil {
		return
	}
	for _, c := range changes {
		writeChangeRecord(ctx, sm.SaveActivity, r.logger, time.Now(), actor, changeRecord{
			change: c.Kind, profile: c.Profile, previousProfile: c.PreviousProfile, diff: c.Diff,
		})
	}
}
