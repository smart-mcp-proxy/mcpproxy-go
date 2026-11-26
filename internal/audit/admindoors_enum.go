package audit

import (
	"sort"
	"strings"
)

// RegistryDoors returns the distinct registry doors with the given prefix
// ("REST ", "MCP ", "CLI "), sorted. Used by the enumeration tests.
func RegistryDoors(prefix string) []string {
	set := map[string]bool{}
	for _, d := range AdminDoors {
		if strings.HasPrefix(d.Door, prefix) {
			set[d.Door] = true
		}
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// DiffDoors compares enumerated doors with the registry's: missing are
// registered but unclassified, stale are classified but no longer registered.
func DiffDoors(prefix string, enumerated []string) (missing, stale []string) {
	reg := map[string]bool{}
	for _, d := range RegistryDoors(prefix) {
		reg[d] = true
	}
	got := map[string]bool{}
	for _, d := range enumerated {
		got[d] = true
		if !reg[d] {
			missing = append(missing, d)
		}
	}
	for d := range reg {
		if !got[d] {
			stale = append(stale, d)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	return missing, stale
}
