package diagnostics

import "sort"

// Get looks up a catalog entry by code. Returns (zero, false) for unknown codes.
func Get(c Code) (CatalogEntry, bool) {
	e, ok := registry[c]
	return e, ok
}

// All returns a stable-sorted copy of every registered catalog entry.
// The ordering is by code lexicographically so output is deterministic.
func All() []CatalogEntry {
	out := make([]CatalogEntry, 0, len(registry))
	for _, e := range registry {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Code < out[j].Code
	})
	return out
}

// Has reports whether a code is registered in the catalog.
func Has(c Code) bool {
	_, ok := registry[c]
	return ok
}
