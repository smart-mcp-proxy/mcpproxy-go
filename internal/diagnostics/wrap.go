package diagnostics

// WrapError attaches a stable Code to an existing error so the classifier
// can recognise it without falling back to free-text matching. Producers in
// the OAUTH / DOCKER / CONFIG / QUARANTINE domains should wrap their terminal
// (non-automatically-recoverable) errors with WrapError at the point the
// failure is first observed. Spec 044.
//
// The returned error preserves the original via errors.Unwrap(), so callers
// that still inspect typed error chains (errors.Is / errors.As) continue to
// work unchanged.
func WrapError(code Code, err error) error {
	if err == nil {
		return nil
	}
	return &codedError{code: code, inner: err}
}

// codedError is the concrete implementation of the errors.Is-friendly
// "has-code" interface the classifier's fast path looks for.
type codedError struct {
	code  Code
	inner error
}

func (e *codedError) Error() string {
	return e.inner.Error()
}

func (e *codedError) Unwrap() error {
	return e.inner
}

// Code returns the attached diagnostic code. Matches the interface used by
// Classify's fast path (`interface{ Code() Code }`).
func (e *codedError) Code() Code {
	return e.code
}

// Convenience shortcuts — thin wrappers around WrapError so call-sites in
// other packages don't have to import the stable code constants just to
// attach them. Keep the set small; only the most common attribution sites
// live here. Spec 044.

// WrapOAuthRefreshExpired marks an error as the refresh-token-expired code.
func WrapOAuthRefreshExpired(err error) error { return WrapError(OAuthRefreshExpired, err) }

// WrapOAuthRefresh403 marks an error as an OAuth refresh 403 / invalid_grant.
func WrapOAuthRefresh403(err error) error { return WrapError(OAuthRefresh403, err) }

// WrapOAuthDiscoveryFailed marks an OAuth authorization-server discovery failure.
func WrapOAuthDiscoveryFailed(err error) error { return WrapError(OAuthDiscoveryFailed, err) }

// WrapDockerDaemonDown marks a Docker daemon unreachable error.
func WrapDockerDaemonDown(err error) error { return WrapError(DockerDaemonDown, err) }

// WrapDockerImagePullFailed marks a Docker image-pull failure.
func WrapDockerImagePullFailed(err error) error { return WrapError(DockerImagePullFailed, err) }

// WrapDockerNoPermission marks a Docker permission-denied error.
func WrapDockerNoPermission(err error) error { return WrapError(DockerNoPermission, err) }

// WrapConfigParseError marks a configuration-parse failure.
func WrapConfigParseError(err error) error { return WrapError(ConfigParseError, err) }

// WrapConfigMissingSecret marks a missing / unresolved secret reference.
func WrapConfigMissingSecret(err error) error { return WrapError(ConfigMissingSecret, err) }

// WrapConfigDeprecatedField marks a use of a deprecated config field.
func WrapConfigDeprecatedField(err error) error { return WrapError(ConfigDeprecatedField, err) }

// WrapQuarantinePendingApproval marks a tool-quarantine rejection.
func WrapQuarantinePendingApproval(err error) error { return WrapError(QuarantinePendingApproval, err) }

// WrapQuarantineToolChanged marks a tool-description-changed rejection.
func WrapQuarantineToolChanged(err error) error { return WrapError(QuarantineToolChanged, err) }
