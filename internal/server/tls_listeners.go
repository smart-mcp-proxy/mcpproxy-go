package server

import (
	"crypto/tls"
)

// wrapTCPListenerWithTLS makes the multiplex listener serve TLS on the TCP
// listener's connections only.
//
// The tray / Unix-socket / named-pipe listener must stay plain HTTP: the macOS
// tray and CLI speak cleartext over that local IPC channel (file permissions
// protect it). Wrapping the whole multiplex listener with TLS broke
// tray /ready and CLI status when tls.enabled=true (issue #1623).
//
// The TLS layer is applied in the multiplex accept loop, outside the source
// tag, so net/http still receives a *tls.Conn: it needs that type to set
// r.TLS and to hand ALPN-negotiated h2 connections to its HTTP/2 server.
func wrapTCPListenerWithTLS(tcp *Listener, cfg *tls.Config) {
	if tcp == nil || cfg == nil {
		return
	}
	tcp.tlsConfig = cfg
}
