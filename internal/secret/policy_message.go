package secret

import "errors"

// PublicPolicyMessage returns the fixed operator-facing text for a policy denial.
func PublicPolicyMessage(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrSecretFileDenied) {
		return ErrSecretFileDenied.Error()
	}
	if errors.Is(err, ErrReservedEnvVar) {
		return ErrReservedEnvVar.Error()
	}
	return ErrSecretFileDenied.Error()
}
