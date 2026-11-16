//go:build windows

package redirectionguard

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// processRedirectionTrustPolicy is PROCESS_MITIGATION_POLICY
// ProcessRedirectionTrustPolicy; golang.org/x/sys/windows does not export it.
const processRedirectionTrustPolicy = 16

var procGetProcessMitigationPolicy = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessMitigationPolicy")

// redirectionTrustPolicy mirrors PROCESS_MITIGATION_REDIRECTION_TRUST_POLICY.
type redirectionTrustPolicy struct {
	Flags uint32
}

func enforced() bool {
	if procGetProcessMitigationPolicy.Find() != nil {
		return false // older Windows without the API
	}
	var p redirectionTrustPolicy
	r, _, _ := procGetProcessMitigationPolicy.Call(
		uintptr(windows.CurrentProcess()),
		processRedirectionTrustPolicy,
		uintptr(unsafe.Pointer(&p)),
		unsafe.Sizeof(p),
	)
	return policyEnforced(r, p.Flags)
}
