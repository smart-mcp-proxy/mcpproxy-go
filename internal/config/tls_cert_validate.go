package config

import (
	"fmt"
	"path/filepath"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tlslocal"
)

// validateTLSCertSettings checks tls.extra_sans, tls.cert_file and
// tls.key_file. It does not read the files: that happens when the server
// starts, and an unreadable file stops the start with an error naming the
// setting.
func validateTLSCertSettings(t *TLSConfig) []ValidationError {
	var errs []ValidationError
	if _, _, err := tlslocal.ParseSANs(t.ExtraSANs); err != nil {
		errs = append(errs, ValidationError{Field: "tls.extra_sans", Message: err.Error()})
	}
	if (t.CertFile == "") != (t.KeyFile == "") {
		field, other := "tls.cert_file", "tls.key_file"
		if t.CertFile == "" {
			field, other = other, field
		}
		errs = append(errs, ValidationError{Field: field, Message: "requires " + other + "; set both or neither"})
	}
	for _, f := range []struct{ field, path string }{{"tls.cert_file", t.CertFile}, {"tls.key_file", t.KeyFile}} {
		if f.path != "" && !filepath.IsAbs(f.path) {
			errs = append(errs, ValidationError{Field: f.field, Message: fmt.Sprintf("path must be absolute: %s", f.path)})
		}
	}
	if t.CertFile != "" && len(t.ExtraSANs) > 0 {
		errs = append(errs, ValidationError{Field: "tls.extra_sans", Message: "cannot be combined with tls.cert_file: the provided certificate decides the names"})
	}
	return errs
}
