package storage

// ToolCallScope restricts a tool-call read to a set of upstream server names
// (#1166 follow-up).
//
// nil means UNRESTRICTED — an admin caller, or an internal caller with no
// identity to check. A non-nil value restricts, and an EMPTY non-nil value
// restricts to nothing: that is the shape an agent token allowed no servers
// produces, and reading it as "unrestricted" would open the door it exists to
// close. "*" is a wildcard, matching auth.AuthContext.CanAccessServer.
//
// It is threaded into the read itself rather than applied to a returned page
// because the reads it guards also report a `total`: post-filtering shrinks the
// page while the total keeps counting the records it removed, which is both a
// broken pager and a precise count oracle for what was hidden.
type ToolCallScope []string

// Allows reports whether records attributed to serverName are in scope.
func (s ToolCallScope) Allows(serverName string) bool {
	if s == nil {
		return true
	}
	if serverName == "" {
		return false
	}
	for _, allowed := range s {
		if allowed == "*" || allowed == serverName {
			return true
		}
	}
	return false
}
