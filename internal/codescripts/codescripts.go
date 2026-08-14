// Package codescripts resolves and lists server-side stored scripts for the
// code_execution tool (Spec 097).
//
// A stored script is a file named `<name>.js` or `<name>.ts` in the `scripts/`
// directory next to the ACTIVE configuration file. Callers address it by base
// NAME — never by path — and this package is the single owner of what a name
// may be, how it maps to a file, and what a usable script file looks like.
//
// Confinement is the name validator, not the filesystem walk: a token-valid
// name contains no separators and no dots, so joining it to the scripts
// directory cannot escape that directory by construction (FR-003 / SC-003).
// ValidateName therefore runs before any filesystem call. On top of that
// boundary sits a symlink/non-regular POLICY, made atomic where the platform
// allows it (Unix O_NOFOLLOW) and best-effort where it does not (Windows).
//
// Nothing here caches: each Resolve performs one open and one bounded read, so
// an atomic replacement of a script file is visible to the very next
// invocation with nothing to invalidate (FR-009).
package codescripts

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// MaxNameLen is the longest permitted script name.
	MaxNameLen = 64

	// MaxSizeBytes bounds the daemon-side read of a script file. Inline code
	// has no such bound; stored scripts do, purely to bound the read.
	MaxSizeBytes = 256 * 1024

	// MaxErrorNames is how many available names a not-found error lists
	// (FR-004); the total count is reported alongside.
	MaxErrorNames = 20

	// DirName is the scripts directory's name, relative to the config file.
	DirName = "scripts"

	// LanguageJavaScript / LanguageTypeScript are the languages a script
	// extension can derive, matching the code_execution `language` parameter.
	LanguageJavaScript = "javascript"
	LanguageTypeScript = "typescript"

	extJS = ".js"
	extTS = ".ts"
)

// Status classifies a listed script.
type Status string

const (
	StatusOK        Status = "ok"        // invocable
	StatusAmbiguous Status = "ambiguous" // both .js and .ts exist for this name
	StatusInvalid   Status = "invalid"   // present but not usable; see Reason
)

// Reasons a script file is present but unusable.
const (
	ReasonEmpty      = "empty"
	ReasonOversized  = "oversized"
	ReasonUnreadable = "unreadable"
	ReasonNonRegular = "non-regular"
)

// errNonRegular is the platform-independent signal that the path is not a
// regular file — a symlink, directory or device. Platform openers return it
// (Unix maps the kernel's no-follow rejection onto it).
var errNonRegular = errors.New("not a regular file")

// Entry is one listed script (FR-007). Paths holds the single source file, or
// both candidates when the name is ambiguous.
type Entry struct {
	Name   string   `json:"name"`
	Paths  []string `json:"paths"`
	Status Status   `json:"status"`
	Reason string   `json:"reason,omitempty"`
}

// InvalidNameError rejects a script name before any filesystem access.
type InvalidNameError struct {
	Name   string
	Reason string
}

func (e *InvalidNameError) Error() string {
	return fmt.Sprintf("invalid script name %q: %s (names are 1-%d characters of A-Z, a-z, 0-9, '-' or '_' — a name, never a path)",
		truncateForMessage(e.Name), e.Reason, MaxNameLen)
}

// NotFoundError reports a name with no script file behind it, carrying the
// available names so the caller can recover in one round trip (FR-004).
type NotFoundError struct {
	Name      string
	Dir       string
	Available []string // first MaxErrorNames ok names, alphabetical
	Total     int      // total ok scripts in the directory
}

func (e *NotFoundError) Error() string {
	if e.Total == 0 {
		return fmt.Sprintf("stored script %q not found: no stored scripts in %s (create %s%s or %s%s there)",
			e.Name, e.Dir, e.Name, extJS, e.Name, extTS)
	}
	msg := fmt.Sprintf("stored script %q not found in %s. Available scripts (%d): %s",
		e.Name, e.Dir, e.Total, strings.Join(e.Available, ", "))
	if e.Total > len(e.Available) {
		msg += fmt.Sprintf(" … and %d more (run 'mcpproxy code scripts list' for the full list)", e.Total-len(e.Available))
	}
	return msg
}

// AmbiguousError reports a name backed by both a .js and a .ts file.
type AmbiguousError struct {
	Name  string
	Paths []string
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("stored script %q is ambiguous: %s both exist — remove one",
		e.Name, strings.Join(e.Paths, " and "))
}

// InvalidError reports a script file that exists but cannot be executed.
type InvalidError struct {
	Name   string
	Path   string
	Reason string
	Detail string
}

func (e *InvalidError) Error() string {
	msg := fmt.Sprintf("stored script %q (%s) is %s", e.Name, e.Path, e.Reason)
	switch e.Reason {
	case ReasonOversized:
		msg += fmt.Sprintf(": scripts are limited to %d bytes", MaxSizeBytes)
	case ReasonNonRegular:
		msg += ": only regular files are executed (symlinks, directories and devices are rejected)"
	}
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

// LanguageMismatchError reports an explicit `language` that contradicts the
// script's extension (the extension is authoritative).
type LanguageMismatchError struct {
	Name      string
	Extension string
	Requested string
	Derived   string
}

func (e *LanguageMismatchError) Error() string {
	return fmt.Sprintf("stored script %q is a %s file (%s) but language %q was requested — omit 'language' or set it to %q",
		e.Name, e.Extension, e.Derived, e.Requested, e.Derived)
}

// DirFor returns the scripts directory belonging to a config file path.
// An empty config path yields an empty directory (no authority, no scripts).
func DirFor(configFilePath string) string {
	if configFilePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configFilePath), DirName)
}

// ValidateName enforces the script-name token: 1-MaxNameLen characters of
// [A-Za-z0-9_-]. This is the confinement boundary and performs NO filesystem
// access — a valid name has no separators and no dots, so it cannot traverse.
func ValidateName(name string) error {
	if name == "" {
		return &InvalidNameError{Name: name, Reason: "name is empty"}
	}
	if len(name) > MaxNameLen {
		return &InvalidNameError{Name: name, Reason: fmt.Sprintf("name is %d characters long", len(name))}
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return &InvalidNameError{Name: name, Reason: fmt.Sprintf("character %q is not allowed", string(name[i]))}
		}
	}
	return nil
}

// DeriveLanguage maps a script extension to a code_execution language and
// rejects an explicit language that contradicts it. An empty explicit language
// always agrees.
func DeriveLanguage(name, ext, explicitLanguage string) (string, error) {
	var derived string
	switch ext {
	case extJS:
		derived = LanguageJavaScript
	case extTS:
		derived = LanguageTypeScript
	default:
		return "", &InvalidError{Name: name, Reason: ReasonNonRegular, Detail: fmt.Sprintf("unsupported extension %q", ext)}
	}
	if explicitLanguage != "" && explicitLanguage != derived {
		return "", &LanguageMismatchError{Name: name, Extension: ext, Requested: explicitLanguage, Derived: derived}
	}
	return derived, nil
}

// Resolve reads the stored script `name` from scriptsDir and returns its
// source together with the language derived from its extension.
//
// Order matters: the name is validated BEFORE any filesystem call (SC-003),
// then both candidate extensions are probed, then the surviving candidate is
// opened with the platform's no-follow idiom and read through a bounded
// reader. Exactly one open and one read per call — no cache, no re-read.
func Resolve(scriptsDir, name, explicitLanguage string) (source []byte, language string, err error) {
	if err := ValidateName(name); err != nil {
		return nil, "", err
	}

	// Probe both extensions. Lstat (not Stat) so a symlink is seen as a
	// symlink here; the open below is the authoritative, race-free check.
	var found []string
	for _, ext := range []string{extJS, extTS} {
		path := filepath.Join(scriptsDir, name+ext)
		if _, statErr := os.Lstat(path); statErr == nil {
			found = append(found, path)
		}
	}

	switch len(found) {
	case 0:
		return nil, "", newNotFoundError(scriptsDir, name)
	case 1:
	default:
		return nil, "", &AmbiguousError{Name: name, Paths: found}
	}

	path := found[0]
	lang, err := DeriveLanguage(name, filepath.Ext(path), explicitLanguage)
	if err != nil {
		return nil, "", err
	}

	f, err := openScriptFile(path)
	if err != nil {
		switch {
		case errors.Is(err, errNonRegular):
			return nil, "", &InvalidError{Name: name, Path: path, Reason: ReasonNonRegular}
		case errors.Is(err, fs.ErrNotExist):
			// Removed between the probe and the open.
			return nil, "", newNotFoundError(scriptsDir, name)
		default:
			return nil, "", &InvalidError{Name: name, Path: path, Reason: ReasonUnreadable, Detail: err.Error()}
		}
	}
	defer f.Close()

	// Re-verify on the open descriptor: this is the file that will actually be
	// read, whatever the path pointed at a moment ago.
	info, err := f.Stat()
	if err != nil {
		return nil, "", &InvalidError{Name: name, Path: path, Reason: ReasonUnreadable, Detail: err.Error()}
	}
	if !info.Mode().IsRegular() {
		return nil, "", &InvalidError{Name: name, Path: path, Reason: ReasonNonRegular}
	}

	// Bound the read itself rather than trusting the stat size: a file that
	// grows between stat and read would otherwise execute truncated content.
	// One extra byte is requested purely to detect the overflow.
	data, err := io.ReadAll(io.LimitReader(f, MaxSizeBytes+1))
	if err != nil {
		return nil, "", &InvalidError{Name: name, Path: path, Reason: ReasonUnreadable, Detail: err.Error()}
	}
	if len(data) > MaxSizeBytes {
		return nil, "", &InvalidError{Name: name, Path: path, Reason: ReasonOversized}
	}
	if len(data) == 0 {
		return nil, "", &InvalidError{Name: name, Path: path, Reason: ReasonEmpty}
	}

	return data, lang, nil
}

// newNotFoundError builds the discovery-carrying not-found error (FR-004).
// A listing failure is not fatal here: the caller still gets "not found".
func newNotFoundError(scriptsDir, name string) *NotFoundError {
	err := &NotFoundError{Name: name, Dir: scriptsDir}
	if scriptsDir == "" {
		return err
	}
	entries, listErr := List(scriptsDir)
	if listErr != nil {
		return err
	}
	for _, e := range entries {
		if e.Status != StatusOK {
			continue
		}
		err.Total++
		if len(err.Available) < MaxErrorNames {
			err.Available = append(err.Available, e.Name)
		}
	}
	return err
}

// List enumerates the token-valid stored scripts in scriptsDir, alphabetically
// by name. An absent (or unset) directory is an empty list, not an error
// (FR-007). Statuses are advisory — Resolve re-checks at invocation time.
func List(scriptsDir string) ([]Entry, error) {
	if scriptsDir == "" {
		return []Entry{}, nil
	}
	dirEntries, err := os.ReadDir(scriptsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Entry{}, nil
		}
		return nil, fmt.Errorf("failed to read scripts directory %s: %w", scriptsDir, err)
	}

	// name -> extension -> dir entry, so both candidates of an ambiguous name
	// are collected before any status is decided.
	candidates := make(map[string]map[string]fs.DirEntry, len(dirEntries))
	for _, d := range dirEntries {
		ext := filepath.Ext(d.Name())
		if ext != extJS && ext != extTS {
			continue
		}
		base := strings.TrimSuffix(d.Name(), ext)
		if ValidateName(base) != nil {
			continue
		}
		if candidates[base] == nil {
			candidates[base] = make(map[string]fs.DirEntry, 2)
		}
		candidates[base][ext] = d
	}

	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	sort.Strings(names)

	entries := make([]Entry, 0, len(names))
	for _, name := range names {
		byExt := candidates[name]
		if len(byExt) == 2 {
			entries = append(entries, Entry{
				Name:   name,
				Paths:  []string{filepath.Join(scriptsDir, name+extJS), filepath.Join(scriptsDir, name+extTS)},
				Status: StatusAmbiguous,
			})
			continue
		}
		ext := extJS
		if _, ok := byExt[extTS]; ok {
			ext = extTS
		}
		entries = append(entries, describeEntry(scriptsDir, name, ext, byExt[ext]))
	}
	return entries, nil
}

// describeEntry classifies a single candidate file for the listing.
func describeEntry(scriptsDir, name, ext string, d fs.DirEntry) Entry {
	entry := Entry{Name: name, Paths: []string{filepath.Join(scriptsDir, name+ext)}, Status: StatusOK}
	info, err := d.Info()
	switch {
	case err != nil:
		entry.Status, entry.Reason = StatusInvalid, ReasonUnreadable
	case !info.Mode().IsRegular():
		entry.Status, entry.Reason = StatusInvalid, ReasonNonRegular
	case info.Size() == 0:
		entry.Status, entry.Reason = StatusInvalid, ReasonEmpty
	case info.Size() > MaxSizeBytes:
		entry.Status, entry.Reason = StatusInvalid, ReasonOversized
	}
	return entry
}

// truncateForMessage bounds caller-supplied text echoed back in an error.
func truncateForMessage(s string) string {
	const limit = MaxNameLen + 16
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}
