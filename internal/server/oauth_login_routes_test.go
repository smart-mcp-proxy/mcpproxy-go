package server

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPeerIsLoopback(t *testing.T) {
	cases := []struct {
		remote string
		want   bool
	}{
		{"127.0.0.1:5000", true},
		{"127.5.6.7:5000", true},
		{"[::1]:5000", true},
		{"[::ffff:127.0.0.1]:5000", true},
		{"192.0.2.1:5000", false},
		{"10.0.0.5:5000", false},
		{"[2001:db8::1]:5000", false},
		{"", false},
		{"@", false},
		{"/tmp/mcpproxy.sock", false},
		{"127.0.0.1", false}, // no port
		{"not-an-address", false},
		{"localhost:5000", false}, // names are not peer addresses
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		assert.Equal(t, c.want, peerIsLoopback(r), "remote=%q", c.remote)
	}
}
