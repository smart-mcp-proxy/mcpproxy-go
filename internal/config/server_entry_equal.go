package config

import (
	"bytes"
	"encoding/json"
	"time"
)

// ServerEntryEqual reports whether two entries of the same server are the same
// configuration (Spec 116). Every serialized field counts, including Shared,
// AuthBroker and the explicit-quarantine bit (#937), which the JSON form only
// partly carries. Created and Updated are ignored: they are bookkeeping that
// unrelated writes rewrite, and counting them would give every commit a new
// generation for every server.
//
// The generation rule (Spec 116 data-model §2) keeps a server's generation
// when this is true and assigns the commit's generation when it is false.
func ServerEntryEqual(a, b *ServerConfig) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.QuarantineExplicitlySet() != b.QuarantineExplicitlySet() {
		return false
	}
	ja, errA := entryJSON(a)
	jb, errB := entryJSON(b)
	if errA != nil || errB != nil {
		// An unmarshalable entry cannot be proven equal; treating it as
		// changed costs a new generation, never a lost change.
		return false
	}
	return bytes.Equal(ja, jb)
}

func entryJSON(sc *ServerConfig) ([]byte, error) {
	cp := *sc
	cp.Created = time.Time{}
	cp.Updated = time.Time{}
	return json.Marshal(&cp)
}
