package auth

import (
	"crypto/rand"
	"encoding/base32"
	"regexp"
	"strings"
)

// Audit ref kinds (Spec 133 §Target): the three-letter prefix of an opaque
// audit identifier.
const (
	AuditRefKindAgentToken = "tok"
	AuditRefKindClient     = "cli"
)

// Lower case on purpose: the audit credential mask matches upper-case
// shapes (AKIA…), so an upper-case ref could be rewritten by the sanitizer.
var auditRefPattern = regexp.MustCompile(`^[a-z]{3}_[a-z0-9]{8,32}$`)

// NewAuditRef returns a fresh opaque audit identifier "<kind>_<16 base32>".
// It is random, never derived from a name or a value, so it can stand in for
// a label the audit line withholds and still join two lines about one target.
func NewAuditRef(kind string) string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on supported platforms; a constant here
		// would make two targets indistinguishable, so fail loudly.
		panic("auth: crypto/rand unavailable: " + err.Error())
	}
	return kind + "_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]))
}

// ValidAuditRef reports whether r has the audit ref shape.
func ValidAuditRef(r string) bool { return auditRefPattern.MatchString(r) }

// EnsureAuditRef assigns t an audit ref when it has none (a record created
// before Spec 133) and reports whether it did.
func EnsureAuditRef(t *AgentToken) bool {
	if t == nil || ValidAuditRef(t.AuditRef) {
		return false
	}
	kind := AuditRefKindAgentToken
	if t.Kind == KindClient {
		kind = AuditRefKindClient
	}
	t.AuditRef = NewAuditRef(kind)
	return true
}
