package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// SecretFileDirs is the configured ${file:} allow-list. Absent (nil) selects
// platform defaults; an explicit empty list disables file references; exactly
// ["*"] is wildcard compatibility mode. JSON null is rejected.
type SecretFileDirs []string

// UnmarshalJSON rejects null and decodes a JSON array.
func (s *SecretFileDirs) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("secret_file_dirs cannot be null")
	}
	var values []string
	if err := json.Unmarshal(data, &values); err != nil {
		return err
	}
	*s = SecretFileDirs(values)
	return nil
}

const (
	secretFileDirsWildcard = "*"
	envCredentialsDir      = "CREDENTIALS_DIRECTORY"
)

// ErrAdminAPIKeyFilePath is returned when MCPPROXY_API_KEY_FILE names a forbidden path.
var ErrAdminAPIKeyFilePath = errors.New(EnvAPIKeyFile + ": path is not allowed")

// ErrSecretFileDirsNotWritable is returned when a management surface tries to change secret_file_dirs.
var ErrSecretFileDirsNotWritable = errors.New("secret_file_dirs can only be set in the configuration file")

// SecretFileDirsChanged reports whether a management merge attempted to change
// secret_file_dirs away from what is on disk.
func SecretFileDirsChanged(base, edited *Config) bool {
	if base == nil || edited == nil {
		return false
	}
	return !SecretFileDirsEqual(base.SecretFileDirs, edited.SecretFileDirs)
}

// RestoreSecretFileDirsFromFile copies secret_file_dirs from the on-disk config into dst.
func RestoreSecretFileDirsFromFile(dst, onDisk *Config) {
	if dst == nil || onDisk == nil {
		return
	}
	dst.SecretFileDirs = onDisk.SecretFileDirs
}

// SecretFileDirsManagementEditRefused reports whether incoming changes
// secret_file_dirs relative to the file at configPath.
func SecretFileDirsManagementEditRefused(configPath string, incoming *Config) bool {
	if incoming == nil || configPath == "" {
		return false
	}
	onDisk, err := DecodeConfigFile(configPath)
	if err != nil {
		return false
	}
	return SecretFileDirsChanged(onDisk, incoming)
}

// DefaultSecretFileDirRoots returns the platform default allow-list roots before
// the data directory is known. The derived <data-dir>/secrets entry is added
// separately once data_dir is resolved.
func DefaultSecretFileDirRoots() []string {
	return DefaultSecretFileDirRootsForGOOS(runtime.GOOS)
}

// DefaultSecretFileDirRootsForGOOS returns platform default roots before data_dir is known.
func DefaultSecretFileDirRootsForGOOS(goos string) []string {
	switch goos {
	case "windows", "darwin":
		return nil
	default:
		return []string{"/run/secrets", "/run/credentials"}
	}
}

// SecretFileDirsWildcard reports whether dirs is exactly ["*"].
func SecretFileDirsWildcard(dirs []string) bool {
	return len(dirs) == 1 && dirs[0] == secretFileDirsWildcard
}

// SecretFileDirsEqual reports whether two secret_file_dirs settings are the same.
func SecretFileDirsEqual(a, b *SecretFileDirs) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return stringSlicesEqual([]string(*a), []string(*b))
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// EffectiveSecretFileDirRoots resolves the configured allow-list for policy
// enforcement. When cfg.SecretFileDirs is nil the defaults apply; an empty
// non-nil slice disables ${file:}; ["*"] is wildcard mode.
func EffectiveSecretFileDirRoots(cfg *Config, includeDataDirDerived bool) (wildcard bool, roots []string, disabled bool) {
	return EffectiveSecretFileDirRootsForGOOS(cfg, includeDataDirDerived, runtime.GOOS)
}

// EffectiveSecretFileDirRootsForGOOS is EffectiveSecretFileDirRoots with an
// explicit GOOS so platform default roots can be tested on Linux CI.
func EffectiveSecretFileDirRootsForGOOS(cfg *Config, includeDataDirDerived bool, goos string) (wildcard bool, roots []string, disabled bool) {
	if cfg != nil && cfg.SecretFileDirs != nil {
		if len(*cfg.SecretFileDirs) == 0 {
			return false, nil, true
		}
		if SecretFileDirsWildcard([]string(*cfg.SecretFileDirs)) {
			return true, nil, false
		}
		roots = append(roots, ([]string)(*cfg.SecretFileDirs)...)
	} else {
		roots = append(roots, DefaultSecretFileDirRootsForGOOS(goos)...)
	}

	if credDir := os.Getenv(envCredentialsDir); credDir != "" && filepath.IsAbs(credDir) {
		roots = appendUniqueAbs(roots, credDir)
	}

	if includeDataDirDerived && cfg != nil && cfg.DataDir != "" && !strings.Contains(cfg.DataDir, "${") {
		roots = appendUniqueAbs(roots, filepath.Join(cfg.DataDir, "secrets"))
	}

	return false, roots, false
}

func appendUniqueAbs(roots []string, dir string) []string {
	dir = filepath.Clean(dir)
	for _, existing := range roots {
		if existing == dir {
			return roots
		}
	}
	return append(roots, dir)
}

// validateSecretFileDirs returns fatal validation errors for secret_file_dirs.
func validateSecretFileDirs(cfg *Config) []ValidationError {
	if cfg == nil || cfg.SecretFileDirs == nil {
		return nil
	}
	dirs := []string(*cfg.SecretFileDirs)
	if len(dirs) == 0 {
		return nil
	}
	if SecretFileDirsWildcard(dirs) {
		return nil
	}
	var errs []ValidationError
	for i, dir := range dirs {
		if dir == secretFileDirsWildcard {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("secret_file_dirs[%d]", i),
				Message: `wildcard "*" must be the only entry`,
			})
			continue
		}
		if !filepath.IsAbs(dir) {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("secret_file_dirs[%d]", i),
				Message: "each entry must be an absolute directory path",
			})
		}
	}
	return errs
}

// WarnMissingSecretFileDirRoots logs a warning for each configured root that
// does not exist yet. Missing defaults are expected on non-container hosts.
func WarnMissingSecretFileDirRoots(cfg *Config) []string {
	if cfg == nil || cfg.SecretFileDirs == nil {
		return nil
	}
	if SecretFileDirsWildcard([]string(*cfg.SecretFileDirs)) {
		return nil
	}
	var missing []string
	for _, root := range []string(*cfg.SecretFileDirs) {
		if _, err := os.Stat(root); err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, root)
			}
		}
	}
	return missing
}

// ResolveConfigPath returns the effective on-disk config path.
func ResolveConfigPath(cfg *Config, configPath string) string {
	if configPath != "" {
		return configPath
	}
	if cfg == nil || cfg.DataDir == "" {
		return ""
	}
	return filepath.Join(cfg.DataDir, ConfigFileName)
}

// validateAdminAPIKeyFilePath refuses MCPPROXY_API_KEY_FILE paths that point at
// the active config file or at anything in the data directory except
// <data-dir>/secrets.
func validateAdminAPIKeyFilePath(cfg *Config, configPath, filePath string) error {
	if filePath == "" {
		return nil
	}
	resolved, err := filepath.EvalSymlinks(filePath)
	if err != nil {
		// Unusable files are handled by ResolveEnvAPIKey's read error.
		resolved = filepath.Clean(filePath)
	}
	cfgPath := ResolveConfigPath(cfg, configPath)
	if cfgPath != "" {
		cfgResolved, err := filepath.EvalSymlinks(cfgPath)
		if err != nil {
			cfgResolved = filepath.Clean(cfgPath)
		}
		if pathsEqual(resolved, cfgResolved) {
			return ErrAdminAPIKeyFilePath
		}
	}
	if cfg != nil && cfg.DataDir != "" && !strings.Contains(cfg.DataDir, "${") {
		secretsDir := filepath.Join(cfg.DataDir, "secrets")
		if contained, _ := pathContainedInDir(secretsDir, resolved); contained {
			return nil
		}
		if contained, _ := pathContainedInDir(cfg.DataDir, resolved); contained {
			return ErrAdminAPIKeyFilePath
		}
	}
	return nil
}

func pathsEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func pathContainedInDir(dir, file string) (bool, error) {
	dir = filepath.Clean(dir)
	file = filepath.Clean(file)
	rel, err := filepath.Rel(dir, file)
	if err != nil {
		return false, err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}
	return true, nil
}
