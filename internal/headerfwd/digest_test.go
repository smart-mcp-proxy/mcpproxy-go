package headerfwd

import (
	"strings"
	"testing"
)

func TestDigest(t *testing.T) {
	a := newSnapshot(map[string]string{"X-A": "1", "X-B": "2"})
	b := newSnapshot(map[string]string{"X-B": "2", "X-A": "1"})
	c := newSnapshot(map[string]string{"X-A": "1", "X-B": "3"})
	if Digest(a) == "" || Digest(a) != Digest(b) {
		t.Error("not stable for same set")
	}
	if Digest(a) == Digest(c) {
		t.Error("must differ by value")
	}
	if Digest(Snapshot{}) != "" {
		t.Error("empty digest must be empty")
	}
	d := Digest(newSnapshot(map[string]string{"X-A": "secretvalue"}))
	if strings.Contains(d, "secretvalue") || len(d) != 64 {
		t.Errorf("digest = %q", d)
	}
	// name/value boundary must not collide.
	if Digest(newSnapshot(map[string]string{"X-A": "b=c"})) == Digest(newSnapshot(map[string]string{"X-A=b": "c"})) {
		t.Error("boundary collision")
	}
}
