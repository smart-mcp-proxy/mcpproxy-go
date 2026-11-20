// Package redirectionguard detects the Windows RedirectionGuard process
// mitigation on the current process (issue #1844).
//
// Inno Setup 6.7.0-6.7.3 leaked the mitigation from Setup into the
// "postinstall" [Run] child, so a tray launched from the installer ran with
// EnforceRedirectionTrust=1 and every uv-managed server failed with
// "untrusted mount point. (os error 448)". The mitigation is inherited by all
// upstream child processes, so the core can only tell the operator; it cannot
// clear the flag on itself.
package redirectionguard

// warning is shown once at startup and by `mcpproxy doctor`.
const warning = "The Windows RedirectionGuard process mitigation is enforced on this MCPProxy process " +
	"and is inherited by every upstream server it starts. uvx/uv servers will fail with " +
	"\"The path cannot be traversed because it contains an untrusted mount point. (os error 448)\" " +
	"when uv's Python links are junctions made by a standard user. " +
	"Likely cause: MCPProxy was started by an installer (Inno Setup 6.7.0-6.7.3 \"Launch MCPProxy\") " +
	"or by another process that has RedirectionGuard enabled. " +
	"Fix: quit MCPProxy and start it again from the Start menu."

// WarningFor is the pure decision: given whether EnforceRedirectionTrust is
// set, return the operator-facing warning. It is OS-independent so the text is
// unit-tested everywhere.
func WarningFor(enforced bool) (string, bool) {
	if !enforced {
		return "", false
	}
	return warning, true
}

// enforceRedirectionTrustFlag is bit 0 of the policy's Flags
// (PROCESS_MITIGATION_REDIRECTION_TRUST_POLICY.EnforceRedirectionTrust);
// bit 1 is AuditRedirectionTrust, which only logs.
const enforceRedirectionTrustFlag = 1

// policyEnforced interprets a GetProcessMitigationPolicy result: ret is the
// BOOL return value and flags the policy struct's Flags. It is OS-independent
// so the decision is unit-tested everywhere.
func policyEnforced(ret uintptr, flags uint32) bool {
	return ret != 0 && flags&enforceRedirectionTrustFlag != 0
}

// Check inspects the current process. It always reports no warning off
// Windows, and when the policy cannot be read.
func Check() (string, bool) {
	return WarningFor(enforced())
}
