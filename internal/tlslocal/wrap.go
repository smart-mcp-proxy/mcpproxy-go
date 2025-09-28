package tlslocal

import (
    "crypto/tls"
    "net"
    "net/http"
)

func ServeWithTLS(srv *http.Server, ln net.Listener, cfg *tls.Config) error {
    tlsLn := tls.NewListener(ln, cfg)
    return srv.Serve(tlsLn)
}