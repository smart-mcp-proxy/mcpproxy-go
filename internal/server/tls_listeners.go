package server

import (
	"crypto/tls"
)

// wrapTCPListenerWithTLS replaces the TCP listener's underlying net.Listener
// with tls.NewListener so a multiplex Serve applies TLS only to TCP connections.
//
// The tray / Unix-socket / named-pipe listener must stay plain HTTP: the macOS
// tray and CLI speak cleartext over that local IPC channel (file permissions
// protect it). Wrapping the whole multiplex listener with TLS broke
// tray /ready and CLI status when tls.enabled=true (issue #1623).
func wrapTCPListenerWithTLS(tcp *Listener, cfg *tls.Config) {
	if tcp == nil || cfg == nil {
		return
	}
	tcp.Listener = tls.NewListener(tcp.Listener, cfg)
}
