//go:build !darwin

package management

// appDataDenialWarning is a no-op off macOS: the App-Data (TCC) privacy gate is
// macOS-specific, so non-darwin builds never probe client configs or emit this
// warning (Spec 075 US3, T023).
func appDataDenialWarning() (string, bool) { return "", false }
