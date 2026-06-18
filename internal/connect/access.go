package connect

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// AccessOutcome classifies an attempt to read or write a client config file
// (Spec 075 FR-003). It is an alias of string so it interoperates with the
// existing untyped access* constants and the ClientStatus.AccessState wire
// field, while giving classifier signatures a documented, enum-like type.
//
// Valid values are the access* constants in connect.go: accessAccessible,
// accessAbsent, accessDenied, accessMalformed. (accessUnknown is the overall,
// not-content-checked status default, not a classification outcome.)
type AccessOutcome = string

// macOS bundle identifiers used in the tccutil reset remediation. Kept in sync
// with native/macos/MCPProxy/MCPProxy/Info.plist (prod) and the .dev variant.
const (
	bundleIDProd = "com.smartmcpproxy.mcpproxy"
	bundleIDDev  = "com.smartmcpproxy.mcpproxy.dev"
)

// classifyAccess maps a file-access error to an AccessOutcome strictly from the
// error class (Spec 075 FR-011) — never from string-matching error text:
//
//   - nil                          -> accessAccessible
//   - errors.Is(err, ErrNotExist)  -> accessAbsent   (client not installed)
//   - errors.Is(err, ErrPermission)-> accessDenied   (macOS TCC App-Data block)
//   - any other error              -> accessMalformed (read/parse failure)
//
// syscall.EPERM and syscall.EACCES both satisfy errors.Is(err, fs.ErrPermission)
// via their Errno.Is implementation, so a real TCC denial classifies as denied.
func classifyAccess(err error) AccessOutcome {
	switch {
	case err == nil:
		return accessAccessible
	case errors.Is(err, fs.ErrNotExist):
		return accessAbsent
	case errors.Is(err, fs.ErrPermission):
		return accessDenied
	default:
		return accessMalformed
	}
}

// AccessError is returned by connect/disconnect (and surfaced by GetStatus via
// the Remediation field) when a client-config access is permission-denied. It
// is errors.As-discoverable so the REST layer can map it to the right field,
// and unwraps to the underlying OS error so errors.Is(err, fs.ErrPermission)
// still holds (data-model.md AccessError).
type AccessError struct {
	Client      string        // client id/name
	Path        string        // config path attempted
	Outcome     AccessOutcome // accessDenied (could also wrap malformed)
	Remediation string        // actionable fix text
	Err         error         // underlying OS cause
}

func (e *AccessError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("%s (config: %s)", e.Remediation, e.Path)
	}
	return e.Remediation
}

// Unwrap exposes the underlying OS error for errors.Is/errors.As.
func (e *AccessError) Unwrap() error { return e.Err }

// remediationText builds the canonical privacy-denied message (data-model.md):
// it names the cause, the App Data settings path, and the exact tccutil reset
// command with both the prod and dev bundle identifiers (FR-005).
func remediationText(client string) string {
	return fmt.Sprintf(
		"macOS blocked mcpproxy from reading %s's configuration (Privacy & Security ▸ App Data).\n"+
			"Fix: System Settings ▸ Privacy & Security ▸ App Data ▸ enable mcpproxy,\n"+
			"or run: tccutil reset SystemPolicyAppData %s\n"+
			"(dev builds: %s)",
		client, bundleIDProd, bundleIDDev,
	)
}

// newAccessError constructs an *AccessError for a denied access on the given
// client/path, wrapping the OS cause.
func (s *Service) newAccessError(client *ClientDef, path string, cause error) *AccessError {
	name := client.Name
	return &AccessError{
		Client:      name,
		Path:        path,
		Outcome:     accessDenied,
		Remediation: remediationText(name),
		Err:         cause,
	}
}

// asAccessError wraps a connect/disconnect error as a typed *AccessError when it
// classifies as a permission denial; otherwise it returns the error unchanged so
// existing error semantics (unknown client, already-exists, parse failures) are
// preserved (FR-004). A nil error stays nil.
func (s *Service) asAccessError(client *ClientDef, path string, err error) error {
	if err == nil {
		return nil
	}
	if classifyAccess(err) == accessDenied {
		return s.newAccessError(client, path, err)
	}
	return err
}

// DetectAppDataDenial probes installed client configs for a persisted macOS
// App-Data (TCC) permission denial, for the doctor diagnostic (Spec 075 US3,
// FR-007/008). It walks the supported clients and, for the first whose config
// file exists (os.Stat metadata only), performs a single content read through the
// seam; if that read classifies as accessDenied it reports the denial with the
// canonical, one-command remediation. It returns (false, "") when no installed
// client config is permission-denied — including when none are installed — so the
// check never raises a false positive on a machine that simply has no clients or
// has granted access (FR-008, T022).
//
// Unlike GetAllStatus this DOES read content: the doctor command is an explicit
// user action, the one place a macOS App-Data prompt may legitimately appear.
func (s *Service) DetectAppDataDenial() (denied bool, remediation string) {
	for _, c := range GetAllClients() {
		if !c.Supported {
			continue
		}
		cfgPath := ConfigPath(c.ID, s.homeDir)
		if cfgPath == "" {
			continue
		}
		// Metadata-only existence gate: never read a config that is not installed,
		// so a brand-new machine produces no read and no false positive.
		if _, err := os.Stat(cfgPath); err != nil {
			continue
		}
		if _, _, outcome := s.entryAccess(c, cfgPath); outcome == accessDenied {
			return true, remediationText(c.Name)
		}
	}
	return false, ""
}
