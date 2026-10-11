package configsvc

import (
	"fmt"
	"sort"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// ServerGen is the committed {generation, incarnation} of one server. It lives
// in the leaf package commitview so configsvc and storage never import each
// other (Spec 116 review R7.2).
type ServerGen = commitview.ServerGen

// ChangeKind classifies one server's change in a commit.
type ChangeKind int

const (
	// ChangeCreated: the name is absent from the base snapshot.
	ChangeCreated ChangeKind = iota + 1
	// ChangeUpdated: the entry exists in both and is not ServerEntryEqual.
	ChangeUpdated
	// ChangeRemoved: the name is absent from the new config.
	ChangeRemoved
	// ChangeReplaced: removed and re-created in one commit (review R8.2). Every
	// consumer treats it as Removed(Old) followed by Created(New).
	ChangeReplaced
)

func (k ChangeKind) String() string {
	switch k {
	case ChangeCreated:
		return "created"
	case ChangeUpdated:
		return "updated"
	case ChangeRemoved:
		return "removed"
	case ChangeReplaced:
		return "replaced"
	default:
		return "unknown"
	}
}

// ServerChange is one server's change in a commit (Spec 116 data-model §1).
type ServerChange struct {
	Kind ChangeKind
	Old  ServerGen // zero when Created
	New  ServerGen // zero when Removed
	// Reconnect is true when the connection settings, Enabled or Quarantined
	// changed (config.ConnectionEquivalent is false); always true for
	// Created, Removed and Replaced.
	Reconnect bool
	// QuarantineRaised is true when the server is quarantined in the new
	// config and was not in the base (or did not exist).
	QuarantineRaised bool
}

// SeedStamp is the stored stamp of a server whose base snapshot carries no
// generation (the snapshot NewService was built with). Matches reports
// whether the stored row describes the same configuration as the new entry.
type SeedStamp struct {
	Stamp   ServerGen
	Matches bool
}

// PrepareInput is everything Prepare needs. Replaced names a server the
// commit removed and re-created (review R8.2): the final config alone cannot
// tell that from an unchanged or updated entry. Every replaced name must be
// present in both the base and the new config.
type PrepareInput struct {
	Config   *config.Config
	Source   string
	Type     UpdateType
	Replaced map[string]bool
	// Seed carries stored stamps for servers whose base snapshot has no
	// generation, so generations survive a restart (data-model §2, D13).
	Seed map[string]SeedStamp
	// Path, when non-empty, is the config file path the snapshot publishes
	// with. It moves only when Publish succeeds (Spec 116 PR1 review round 3:
	// a failed commit must not leave a new path published).
	Path string
}

// PreparedSnapshot is a snapshot computed against a base version and not yet
// visible. Changes describes the commit from the same inputs, so the
// snapshot, the commit result and the stored rows cannot disagree.
type PreparedSnapshot struct {
	base       int64
	generation int64
	updateType UpdateType
	source     string
	pathSet    bool
	snap       *Snapshot
	Changes    map[string]ServerChange
}

// Generation is the commit generation G this snapshot will publish as.
func (p *PreparedSnapshot) Generation() int64 { return p.generation }

// Snapshot is the snapshot that Publish will make current. Callers must treat
// it as read-only.
func (p *PreparedSnapshot) Snapshot() *Snapshot { return p.snap }

// ChangedNames returns the names in Changes, sorted.
func (p *PreparedSnapshot) ChangedNames() []string {
	names := make([]string, 0, len(p.Changes))
	for n := range p.Changes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// computeGenerations applies the generation rule of data-model §2:
//
//	replaced (checked first)        → {G, G}
//	absent in base (created)        → {G, G}
//	present, ServerEntryEqual       → base value (kept)
//	present, not equal              → {G, base.Incarnation}
//	absent in next                  → dropped
//
// A base entry without a generation (the initial snapshot) takes its stored
// stamp when the stored row matches, keeps the stored incarnation otherwise,
// and is stamped {G, G} when nothing was ever stored for it.
func computeGenerations(base *Snapshot, in PrepareInput, g int64) (map[string]ServerGen, map[string]ServerChange, error) {
	baseByName := map[string]*config.ServerConfig{}
	if base != nil && base.Config != nil {
		for _, sc := range base.Config.Servers {
			if sc != nil {
				baseByName[sc.Name] = sc
			}
		}
	}
	nextByName := map[string]*config.ServerConfig{}
	for _, sc := range in.Config.Servers {
		if sc == nil {
			continue
		}
		if _, dup := nextByName[sc.Name]; dup {
			return nil, nil, fmt.Errorf("configsvc: server %q is configured twice", sc.Name)
		}
		nextByName[sc.Name] = sc
	}
	for name := range in.Replaced {
		if baseByName[name] == nil || nextByName[name] == nil {
			return nil, nil, fmt.Errorf("configsvc: replaced server %q must be present in both the base and the new config", name)
		}
	}

	baseGen := func(name string) (ServerGen, bool) {
		if base == nil {
			return ServerGen{}, false
		}
		return base.ServerGeneration(name)
	}

	gens := make(map[string]ServerGen, len(nextByName))
	changes := map[string]ServerChange{}
	for name, next := range nextByName {
		prev := baseByName[name]
		switch {
		case in.Replaced[name]:
			old, _ := baseGen(name)
			gens[name] = ServerGen{Gen: g, Incarnation: g}
			changes[name] = ServerChange{
				Kind: ChangeReplaced, Old: old, New: gens[name], Reconnect: true,
				QuarantineRaised: next.Quarantined && !prev.Quarantined,
			}
		case prev == nil:
			gens[name] = ServerGen{Gen: g, Incarnation: g}
			changes[name] = ServerChange{
				Kind: ChangeCreated, New: gens[name], Reconnect: true,
				QuarantineRaised: next.Quarantined,
			}
		default:
			old, stamped := baseGen(name)
			equal := config.ServerEntryEqual(prev, next)
			if !stamped {
				if seed, ok := in.Seed[name]; ok && !seed.Stamp.IsZero() {
					if seed.Matches && equal {
						old = seed.Stamp
					} else {
						old = ServerGen{Incarnation: seed.Stamp.Incarnation}
					}
				}
			}
			switch {
			case equal && old.Gen != 0:
				gens[name] = old
			case equal:
				// Unchanged in the configuration but never stamped (or its
				// stored row differs): stamp it with this commit.
				inc := old.Incarnation
				if inc == 0 {
					inc = g
				}
				gens[name] = ServerGen{Gen: g, Incarnation: inc}
			default:
				inc := old.Incarnation
				if inc == 0 {
					inc = g
				}
				gens[name] = ServerGen{Gen: g, Incarnation: inc}
				changes[name] = ServerChange{
					Kind: ChangeUpdated, Old: old, New: gens[name],
					Reconnect:        !config.ConnectionEquivalent(prev, next),
					QuarantineRaised: next.Quarantined && !prev.Quarantined,
				}
			}
		}
	}
	for name := range baseByName {
		if nextByName[name] == nil {
			old, _ := baseGen(name)
			changes[name] = ServerChange{Kind: ChangeRemoved, Old: old, Reconnect: true}
		}
	}
	return gens, changes, nil
}
