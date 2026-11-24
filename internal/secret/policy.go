package secret

import "errors"

// IsPolicyDenial reports whether err is a fixed upstream secret-policy refusal.
func IsPolicyDenial(err error) bool {
	return errors.Is(err, ErrSecretFileDenied) || errors.Is(err, ErrReservedEnvVar)
}
