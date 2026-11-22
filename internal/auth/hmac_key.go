package auth

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// hmacKeyMu serialises in-process creators. Disk exclusivity (below) covers
// other processes and every caller that bypasses this package-level lock.
var hmacKeyMu sync.Mutex

// readValidHMACKey returns the key at path when it is a 32-byte file.
func readValidHMACKey(path string) ([]byte, bool) {
	data, err := os.ReadFile(path)
	if err == nil && len(data) == 32 {
		return data, true
	}
	return nil, false
}

// GetOrCreateHMACKey reads the HMAC key from <dataDir>/.token_key.
// If the file does not exist, it generates a 32-byte random key,
// writes it with 0600 permissions, and returns it.
//
// Creation is atomic for every caller: in-process callers are serialised by a
// package-level mutex, and the key reaches the final name only through a hard
// link (or O_EXCL) of a fully written 0600 temporary file in the same
// directory, so two first-time callers can never each generate a key and
// overwrite the other's. A caller that loses the race reads and returns the
// winner's key. A file of the wrong size is replaced, as it always was.
func GetOrCreateHMACKey(dataDir string) ([]byte, error) {
	keyPath := filepath.Join(dataDir, hmacKeyFile)

	hmacKeyMu.Lock()
	defer hmacKeyMu.Unlock()

	// Try to read existing key
	if key, ok := readValidHMACKey(keyPath); ok {
		return key, nil
	}

	// Generate new key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate HMAC key: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	tmp, err := os.CreateTemp(dataDir, ".token_key.tmp-*") // created 0600
	if err != nil {
		return nil, fmt.Errorf("failed to write HMAC key file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("failed to write HMAC key file: %w", err)
	}
	if _, err := tmp.Write(key); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("failed to write HMAC key file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("failed to write HMAC key file: %w", err)
	}

	linkErr := os.Link(tmpPath, keyPath)
	switch {
	case linkErr == nil:
		return key, nil
	case errors.Is(linkErr, fs.ErrExist):
		// Someone else created it first: theirs wins when it is valid.
		if existing, ok := readValidHMACKey(keyPath); ok {
			return existing, nil
		}
		// A corrupt file of the wrong size: replace it atomically.
		if err := os.Rename(tmpPath, keyPath); err != nil {
			return nil, fmt.Errorf("failed to write HMAC key file: %w", err)
		}
		return key, nil
	default:
		// No hard links on this filesystem: exclusive create instead.
		f, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			if errors.Is(err, fs.ErrExist) {
				if existing, ok := readValidHMACKey(keyPath); ok {
					return existing, nil
				}
			}
			return nil, fmt.Errorf("failed to write HMAC key file: %w", err)
		}
		_, werr := f.Write(key)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			_ = os.Remove(keyPath)
			return nil, fmt.Errorf("failed to write HMAC key file: %w", errors.Join(werr, cerr))
		}
		return key, nil
	}
}
