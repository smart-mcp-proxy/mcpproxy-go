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

// errIndexGenerationChanged is what a post-open verifyUnchanged closure
// returns when the scripts directory's generation moved between the index
// lookup that produced a hit and this open (round 8 MUST-FIX, the
// lookup→open race): resolve treats it as an ordinary not-found, never as an
// unreadable-directory error, so it discloses nothing beyond the caller's
// own requested name.
var errIndexGenerationChanged = errors.New("codescripts: scripts directory changed between the index lookup and the open")

// errSpellingUnproven is what the darwin/Windows verifyUnchanged closure
// returns when the OPENED descriptor's stored spelling (round 9 MUST-FIX)
// could not be proven to match the requested name — a mismatch (a
// case-rename or replacement landed between the pre-open probe and the
// open) or a failure of the proof call itself; resolve treats either the
// same as errIndexGenerationChanged, as an ordinary not-found.
var errSpellingUnproven = errors.New("codescripts: the opened file's stored spelling could not be proven to match the requested name")

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
//
// The enumeration is administrator-only (Spec 105 FR-012): a scoped caller
// receives the same error with Undisclosed set, whose text names neither
// the other scripts, their count nor the directory — see NonDisclosing().
type NotFoundError struct {
	Name      string
	Dir       string
	Available []string // first MaxErrorNames ok names, alphabetical
	Total     int      // total ok scripts in the directory

	// Undisclosed marks the agent-token form of the error: the listing is
	// withheld and the message is independent of the directory's contents,
	// so a failed call cannot serve as an oracle for what is stored.
	Undisclosed bool
}

// nonDisclosingNotFoundFormat is the agent-token refusal text. It carries only
// the caller's own requested name and must never depend on the directory's
// contents (Spec 105 FR-012: byte-equal for an empty and a populated
// directory).
const nonDisclosingNotFoundFormat = "stored script %q not found (the stored-script listing is available to administrators only; an agent-token caller must already know the script name)"

// NonDisclosing returns a copy of the error stripped of everything that
// discloses the directory's contents — names, count and path — for delivery
// to a scoped (agent-token) caller. The typed identity is preserved, so the
// REST surface still classifies it as SCRIPT_NOT_FOUND.
func (e *NotFoundError) NonDisclosing() *NotFoundError {
	return &NotFoundError{Name: e.Name, Undisclosed: true}
}

func (e *NotFoundError) Error() string {
	if e.Undisclosed {
		return fmt.Sprintf(nonDisclosingNotFoundFormat, e.Name)
	}
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
//
// The paths are host filesystem locations (they reveal the config directory),
// so the scoped form withholds them — see NonDisclosing().
type AmbiguousError struct {
	Name  string
	Paths []string

	// Undisclosed marks the agent-token form: the message names the caller's
	// own script and the reason only, never a host path (Spec 105 FR-012).
	Undisclosed bool
}

// NonDisclosing returns a copy stripped of the host paths, for delivery to a
// scoped (agent-token) caller. The typed identity is preserved, so the REST
// surface still classifies it as SCRIPT_UNUSABLE.
func (e *AmbiguousError) NonDisclosing() *AmbiguousError {
	return &AmbiguousError{Name: e.Name, Undisclosed: true}
}

func (e *AmbiguousError) Error() string {
	if e.Undisclosed {
		return fmt.Sprintf("stored script %q is ambiguous: both a %s and a %s file exist — ask an administrator to remove one",
			e.Name, extJS, extTS)
	}
	return fmt.Sprintf("stored script %q is ambiguous: %s both exist — remove one",
		e.Name, strings.Join(e.Paths, " and "))
}

// InvalidError reports a script file that exists but cannot be executed.
//
// Path is a host filesystem location and Detail is frequently a raw OS error
// carrying another one, so the scoped form withholds both — see
// NonDisclosing().
type InvalidError struct {
	Name   string
	Path   string
	Reason string
	Detail string

	// Undisclosed marks the agent-token form: the message names the caller's
	// own script and the reason only — no path, no OS error (Spec 105 FR-012).
	Undisclosed bool
}

// NonDisclosing returns a copy stripped of the host path and the raw detail,
// for delivery to a scoped (agent-token) caller. The typed identity and the
// reason are preserved, so the REST surface still classifies it as
// SCRIPT_UNUSABLE and the caller still learns what is wrong with its own
// script.
func (e *InvalidError) NonDisclosing() *InvalidError {
	return &InvalidError{Name: e.Name, Reason: e.Reason, Undisclosed: true}
}

func (e *InvalidError) Error() string {
	var msg string
	if e.Undisclosed {
		msg = fmt.Sprintf("stored script %q is %s", e.Name, e.Reason)
	} else {
		msg = fmt.Sprintf("stored script %q (%s) is %s", e.Name, e.Path, e.Reason)
	}
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
// source together with the language derived from its extension. This is the
// ADMINISTRATOR form: a not-found error carries the directory's listing
// (FR-004) and every other refusal names the host path it is about.
//
// Order matters: the name is validated BEFORE any filesystem call (SC-003),
// then the directory decides which candidates exist (pre-105 behaviour, kept
// verbatim: a directory the administrator cannot list is a refusal, SC-005),
// then the surviving candidate is opened with the platform's no-follow idiom
// and read through a bounded reader. Exactly one open and one read per call —
// no cache, no re-read.
func Resolve(scriptsDir, name, explicitLanguage string) (source []byte, language string, err error) {
	return resolve(scriptsDir, name, explicitLanguage, true)
}

// ResolveScoped is Resolve for a scoped (agent-token) caller — Spec 105
// FR-012. It opens and reads the script exactly as Resolve does, but decides
// its candidates by a constant-cost path probe instead of the directory
// listing, and every refusal it returns is already the non-disclosing form: a
// not-found error is built WITHOUT listing the directory — neither the
// discovery listing nor a directory read on the way to the miss; the two
// candidate names are probed and nothing else, so the refusal's cost does not
// grow with what is stored and does not depend on the name asked for
// (probeCandidates) — and the ambiguous / invalid forms carry the caller's
// own name and the reason but no host path and no raw OS error. Typed
// identities are the same, so the REST classifier does not tell the two
// callers apart. The probe is the scoped resolver's alone: the administrator
// path keeps its directory-based decision (SC-005), so a directory that is
// searchable but not listable still refuses administrators as it always did.
func ResolveScoped(scriptsDir, name, explicitLanguage string) (source []byte, language string, err error) {
	return resolve(scriptsDir, name, explicitLanguage, false)
}

// resolve is the shared body of Resolve and ResolveScoped; disclose selects
// the administrator (true) or the scoped (false) refusal forms.
func resolve(scriptsDir, name, explicitLanguage string, disclose bool) (source []byte, language string, err error) {
	if err := ValidateName(name); err != nil {
		return nil, "", err
	}

	notFound := func() error { return notFoundErrorFor(scriptsDir, name, disclose) }
	invalid := func(path, reason, detail string) error {
		e := &InvalidError{Name: name, Path: path, Reason: reason, Detail: detail}
		if !disclose {
			return e.NonDisclosing()
		}
		return e
	}

	// An empty scripts dir would make filepath.Join produce a bare relative
	// path resolved against the process CWD — never that. No authority means
	// no scripts.
	if scriptsDir == "" {
		return nil, "", notFound()
	}

	candidates := candidatesFor
	if !disclose {
		candidates = probeCandidates
	}
	found, verifyUnchanged, err := candidates(scriptsDir, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "", notFound()
		}
		return nil, "", invalid(scriptsDir, ReasonUnreadable, err.Error())
	}

	switch len(found) {
	case 0:
		return nil, "", notFound()
	case 1:
	default:
		ambiguous := &AmbiguousError{Name: name, Paths: found}
		if !disclose {
			return nil, "", ambiguous.NonDisclosing()
		}
		return nil, "", ambiguous
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
			return nil, "", invalid(path, ReasonNonRegular, "")
		case errors.Is(err, fs.ErrNotExist):
			// Removed between the probe and the open.
			return nil, "", notFound()
		default:
			return nil, "", invalid(path, ReasonUnreadable, err.Error())
		}
	}
	defer f.Close()

	// Round 8 MUST-FIX (the lookup→open race): an index hit is re-probed by
	// the candidate's own Lstat, but neither that nor the open itself proves
	// the file just opened is the one the index vouched for — a rename
	// landing between the probe and this open can leave a DIFFERENT file
	// occupying the exact name (a folded spelling of it, on a case-folding
	// mount) for the descriptor's entire lifetime; a no-follow open cannot
	// tell the difference, because it does not compare names, only symlink
	// status. verifyUnchanged proves this AUTHORITATIVELY on f, the
	// descriptor that will actually be read (round 9 MUST-FIX, the
	// PROVEN-AT-OPEN rule): on Linux/BSD by re-reading the directory's
	// generation once more (gen-before == index.gen == gen-after proves the
	// opened entry is the one the index vouched for); on darwin/Windows by
	// reading the opened descriptor's own stored spelling (F_GETPATH /
	// GetFinalPathNameByHandle) and comparing it byte-for-byte to the name
	// that was requested. Either failure closes the descriptor (via the
	// defer above) and refuses rather than trusting it. Nil for the
	// administrator, whose candidatesFor has nothing to recheck against.
	if verifyUnchanged != nil {
		if verifyErr := verifyUnchanged(f, filepath.Base(path)); verifyErr != nil {
			if errors.Is(verifyErr, errIndexGenerationChanged) || errors.Is(verifyErr, errSpellingUnproven) {
				return nil, "", notFound()
			}
			return nil, "", invalid(path, ReasonUnreadable, verifyErr.Error())
		}
	}

	// Re-verify on the open descriptor: this is the file that will actually be
	// read, whatever the path pointed at a moment ago.
	info, err := f.Stat()
	if err != nil {
		return nil, "", invalid(path, ReasonUnreadable, err.Error())
	}
	if !info.Mode().IsRegular() {
		return nil, "", invalid(path, ReasonNonRegular, "")
	}

	// Bound the read itself rather than trusting the stat size: a file that
	// grows between stat and read would otherwise execute truncated content.
	// One extra byte is requested purely to detect the overflow.
	data, err := io.ReadAll(io.LimitReader(f, MaxSizeBytes+1))
	if err != nil {
		return nil, "", invalid(path, ReasonUnreadable, err.Error())
	}
	if len(data) > MaxSizeBytes {
		return nil, "", invalid(path, ReasonOversized, "")
	}
	if len(data) == 0 {
		return nil, "", invalid(path, ReasonEmpty, "")
	}

	return data, lang, nil
}

// candidatesFor returns the paths of the script files backing `name`, in
// extension order (.js then .ts), by reading the directory and comparing entry
// names BYTE FOR BYTE — the same rule List applies. This is the ADMINISTRATOR
// resolver's decision, unchanged from before Spec 105 (SC-005): a directory
// the process cannot list is a refusal, whatever the constructed paths would
// have answered.
//
// The obvious implementation, stat-ing the two constructed paths, delegates the
// name→file decision to the filesystem, and on the default macOS and Windows
// volumes that decision is case-insensitive. `backdoor.JS` then satisfied a
// probe for `backdoor.js` and executed, while every discovery surface — the
// listing, GET /api/v1/code/scripts, the not-found error — skipped it as an
// unknown extension; conversely `foo.js` plus `FOO.ts` were two ok listing
// entries that both refused to run as ambiguous. Reading the directory removes
// the filesystem's matching from the loop entirely, so the two agree on every
// platform. Resolve's no-follow open remains the authoritative check.
//
// The third return is the post-open authoritative recheck probeCandidates
// supplies (round 8 / round 9 MUST-FIX); the administrator's directory-based
// decision has nothing to recheck against, so it is always nil here.
func candidatesFor(scriptsDir, name string) ([]string, func(f *os.File, want string) error, error) {
	dirEntries, err := readDir(scriptsDir)
	if err != nil {
		return nil, nil, err
	}

	present := make(map[string]bool, 2)
	for _, d := range dirEntries {
		switch d.Name() {
		case name + extJS:
			present[extJS] = true
		case name + extTS:
			present[extTS] = true
		}
	}

	found := make([]string, 0, 2)
	for _, ext := range []string{extJS, extTS} {
		if present[ext] {
			found = append(found, filepath.Join(scriptsDir, name+ext))
		}
	}
	return found, nil, nil
}

// probeCandidates is candidatesFor for the SCOPED resolver: the same two
// candidate names, each decided by the platform's constant-cost answer to
// "does the directory hold an entry spelled exactly so" (storedSpellingsOf)
// instead of by a listing. A scoped caller's refusal must cost the same
// whatever the directory holds and whatever it asks for (Spec 105 FR-012 —
// timing class is part of a non-disclosing refusal), so nothing here lists
// the directory on a request's behalf. Exactness matters because the
// filesystem's own name→file decision is case-insensitive on the default
// macOS and Windows volumes and on a Linux case-folding mount (see
// candidatesFor): a `backdoor.JS` that a probe for `backdoor.js` would open
// is not a stored script, exactly as List decides. On darwin and Windows a
// single-entry platform call reports the stored spelling of a probed path; on
// Linux and the BSDs, which have no such call, the answer comes from a
// per-directory index of exact names that is listed once per directory
// change, never per request (storednames_other.go, codex r5 #1). The
// no-follow open remains the authoritative check.
//
// The second return is a post-open AUTHORITATIVE recheck (round 8 / round 9
// MUST-FIX, the lookup→open race): storedSpellingsOf's own verify closure,
// run by resolve on the descriptor that was actually opened — on Linux/BSD a
// directory-generation recheck (storednames_other.go), on darwin/Windows a
// proof of the opened descriptor's own stored spelling
// (storedspellings_probe.go). Never nil on either platform: this is what
// makes the pre-open probe above merely a cheap gate rather than the
// authoritative decision.
func probeCandidates(scriptsDir, name string) ([]string, func(f *os.File, want string) error, error) {
	storedExactly, verifyUnchanged, err := storedSpellingsOf(scriptsDir)
	if err != nil {
		return nil, nil, err
	}
	found := make([]string, 0, 2)
	for _, ext := range []string{extJS, extTS} {
		want := name + ext
		stored, err := storedExactly(want)
		if err != nil {
			return nil, nil, err
		}
		if stored {
			found = append(found, filepath.Join(scriptsDir, want))
		}
	}
	return found, verifyUnchanged, nil
}

// listForNotFound is the directory listing newNotFoundError attaches to the
// administrator's error. A variable so the package's tests can witness that
// the scoped form never invokes it.
var listForNotFound = List

// readDir and lstat are the package's two directory-touching primitives,
// variables so the tests can count them: a scoped resolution must never
// enumerate the directory on a request's behalf (readDir) and must probe a
// fixed number of paths (lstat) whatever the directory holds and whatever it
// asks for (Spec 105 FR-012 timing class).
var (
	readDir = os.ReadDir
	lstat   = os.Lstat
)

// notFoundErrorFor builds the not-found error for one caller kind: the
// discovery-carrying administrator form (FR-004), or the scoped form that is
// constructed without touching the directory at all (Spec 105 FR-012 — the
// listing would only be thrown away, and its per-entry stat would make the
// refusal's latency grow with the number of stored scripts).
func notFoundErrorFor(scriptsDir, name string, disclose bool) *NotFoundError {
	if !disclose {
		return &NotFoundError{Name: name, Undisclosed: true}
	}
	return newNotFoundError(scriptsDir, name)
}

// newNotFoundError builds the discovery-carrying not-found error (FR-004).
// A listing failure is not fatal here: the caller still gets "not found".
func newNotFoundError(scriptsDir, name string) *NotFoundError {
	err := &NotFoundError{Name: name, Dir: scriptsDir}
	if scriptsDir == "" {
		return err
	}
	entries, listErr := listForNotFound(scriptsDir)
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
	dirEntries, err := readDir(scriptsDir)
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
