package auth

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// hmacKeyMu serialises in-process creators. Disk exclusivity (below) covers
// other processes and every caller that bypasses this package-level lock.
var hmacKeyMu sync.Mutex

// linkFile publishes the temp file without replacing a target; a variable so a
// test can simulate a filesystem without hard links.
var linkFile = os.Link

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
// link (or, where links are unsupported, an atomic rename) of a fully written
// 0600 temporary file in the same directory, so a crash never leaves a partial
// key and two first-time callers can never each generate a key and overwrite
// the other's. A caller that loses the race returns the winner's key; after any
// write the on-disk bytes are re-read and returned. A file of the wrong size
// (empty included) is replaced, as it always was.
//
// Single-process assumption: replacing an INVALID file is not guarded across
// processes. That is safe because one data dir has one core process (the bbolt
// lock on config.db, exit code 3) and every caller runs only after its process
// holds that lock.
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

	// Publish the complete temp file. A hard link never replaces an existing
	// target; when it fails (the target exists, or the filesystem has no hard
	// links) a valid existing key wins, and otherwise the complete temp file is
	// renamed into place, which is atomic and works on every filesystem and also
	// recovers an empty or wrong-size target.
	if err := linkFile(tmpPath, keyPath); err != nil {
		if existing, ok := readValidHMACKey(keyPath); ok {
			return existing, nil
		}
		if err := os.Rename(tmpPath, keyPath); err != nil {
			return nil, fmt.Errorf("failed to write HMAC key file: %w", err)
		}
	}
	// Whatever happened above, return what is on disk now.
	if onDisk, ok := readValidHMACKey(keyPath); ok {
		return onDisk, nil
	}
	return nil, errors.New("failed to write HMAC key file: the key file is unreadable after writing")
}
