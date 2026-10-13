package headerfwd

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

var digestKey = func() []byte {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		panic("headerfwd: cannot seed digest key: " + err.Error())
	}
	return k
}()

// Digest is HMAC-SHA256 (per-process random key) over the sorted canonical
// name=value pairs (FR-017). The empty snapshot yields "". Never log it.
func Digest(s Snapshot) string {
	if s.IsEmpty() {
		return ""
	}
	m := hmac.New(sha256.New, digestKey)
	for _, n := range s.Names() {
		m.Write([]byte(n))
		m.Write([]byte{0})
		m.Write([]byte(s.h[n]))
		m.Write([]byte{0})
	}
	return hex.EncodeToString(m.Sum(nil))
}
