package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGetCurrentConfig_NilRuntimeReturnsNil pins GetCurrentConfig's behavior
// to match its sibling GetManagementService: a hand-built *Server with no
// runtime (as tests construct via &Server{}) must return nil rather than
// panic on the nil pointer dereference inside runtime.GetCurrentConfig().
// Unreachable in production (runtime.New rejects a nil config), but latent
// for any test that hand-builds a *Server.
func TestGetCurrentConfig_NilRuntimeReturnsNil(t *testing.T) {
	s := &Server{}
	assert.Nil(t, s.GetCurrentConfig())
}
