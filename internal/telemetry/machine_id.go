package telemetry

import (
	"sync"

	"github.com/denisbrodbeck/machineid"
)

// machineIDAppKey scopes the hashed machine id to mcpproxy. machineid.ProtectedID
// feeds it as the HMAC-SHA256 message keyed by the OS machine id, so the emitted
// value is a non-reversible per-app hash: it cannot be reversed to the raw id and
// cannot be correlated with any other application that hashes the same OS machine
// id with a different key.
const machineIDAppKey = "mcpproxy-telemetry"

// machineIDProvider is the seam used to obtain the app-scoped, non-reversible
// machine-id hash. Production points it at protectedMachineID; tests override it
// to inject deterministic values or simulate an unreadable machine id. It mirrors
// the function-pointer injection style used elsewhere in this package
// (see populateBlockedValuesFrom, DetectEnvKind).
var machineIDProvider = protectedMachineID

// protectedMachineID returns HMAC-SHA256(osMachineID, machineIDAppKey) as a
// lowercase hex string (64 chars). On any failure to read the OS machine id
// (permission errors, missing /etc/machine-id in a minimal container, an exotic
// platform) it returns "" — the caller then omits the field, and the backend
// treats empty as "unknown". The raw OS machine id is NEVER returned or
// transmitted, only the salted hash.
func protectedMachineID() string {
	id, err := machineid.ProtectedID(machineIDAppKey)
	if err != nil {
		return ""
	}
	return id
}

// machineIDOnce guards the cached machine-id hash so repeated heartbeats reuse
// one value without re-probing the OS each time.
var (
	machineIDOnce   sync.Once
	machineIDCached string
)

// resolveMachineID computes the app-scoped machine-id hash once per process and
// caches it. Subsequent heartbeats reuse the cached value, guaranteeing the
// field is stable across payload builds without re-probing the OS. Empty string
// when the OS machine id is unavailable.
func resolveMachineID() string {
	machineIDOnce.Do(func() {
		machineIDCached = machineIDProvider()
	})
	return machineIDCached
}

// resetMachineIDForTest clears the cached machine-id hash so tests can re-run
// resolveMachineID with a freshly injected machineIDProvider. MUST NOT be called
// from production code — guard calls behind _test.go files only.
func resetMachineIDForTest() {
	machineIDOnce = sync.Once{}
	machineIDCached = ""
}
