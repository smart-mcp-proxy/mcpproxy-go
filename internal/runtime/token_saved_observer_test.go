package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// GH #1929 release QA: every token save reaches the observers (the direct
// surface republishes from it), on goroutines of their own so a save path that
// holds a client's locks never runs a rebuild inline.
func TestTokenSavedObserversRunOffTheSavePath(t *testing.T) {
	r := &Runtime{}
	got := make(chan string, 2)
	block := make(chan struct{})
	r.AddTokenSavedObserver(func(name string) { <-block; got <- name })
	r.AddTokenSavedObserver(func(name string) { got <- name })
	r.AddTokenSavedObserver(nil)

	done := make(chan struct{})
	go func() { r.notifyTokenSaved("qa-oauth"); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("notifyTokenSaved waited for an observer")
	}
	assert.Equal(t, "qa-oauth", <-got)
	close(block)
	assert.Equal(t, "qa-oauth", <-got)
}
