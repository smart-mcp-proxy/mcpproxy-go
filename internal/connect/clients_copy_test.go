package connect

import "testing"

// Callers get copies of registry entries; mutating a returned slice must not
// reach the package-global registry.
func TestGetAllClients_DoesNotAliasClientInfoNames(t *testing.T) {
	first := GetAllClients()
	idx := -1
	for i := range first {
		if len(first[i].ClientInfoNames) > 0 {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Skip("no client with ClientInfoNames")
	}
	id := first[idx].ID
	orig := first[idx].ClientInfoNames[0]
	first[idx].ClientInfoNames[0] = "mutated-by-caller"

	for _, c := range GetAllClients() {
		if c.ID == id && c.ClientInfoNames[0] != orig {
			t.Fatalf("registry ClientInfoNames leaked a caller mutation: got %q want %q", c.ClientInfoNames[0], orig)
		}
	}
}

func TestFindClient_DoesNotAliasClientInfoNames(t *testing.T) {
	var id, orig string
	for _, c := range GetAllClients() {
		if len(c.ClientInfoNames) > 0 {
			id, orig = c.ID, c.ClientInfoNames[0]
			break
		}
	}
	if id == "" {
		t.Skip("no client with ClientInfoNames")
	}
	FindClient(id).ClientInfoNames[0] = "mutated-by-caller"

	if got := FindClient(id).ClientInfoNames[0]; got != orig {
		t.Fatalf("registry ClientInfoNames leaked a caller mutation: got %q want %q", got, orig)
	}
}
