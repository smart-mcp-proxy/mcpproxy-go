//go:build windows

package codescripts

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Round 13 MUST-FIX (round-10 findings 2, 3 and 4 — unify Windows onto the
// same design every unix platform uses, darwin included as of this round):
// earlier rounds answered a scoped request from a per-path FindFirstFile
// probe (storedExactly: Lstat then, only on a hit, FindFirstFile) and opened
// the winning candidate by a FRESH, independent path lookup
// (openScriptFile) — two problems the maintainer's decision closes at once
// by giving Windows the same two things Linux/BSD/darwin have:
//
//  1. An exact-spelling INDEX (round-10 finding 3, the timing oracle): the
//     directory is listed off the request path (Warm, and a single-flight
//     rebuild goroutine when a request finds the index behind the
//     directory's own generation), and a request answers from that index's
//     map — an O(1) lookup that costs the SAME whether the name is absent or
//     a differently cased variant exists, unlike Lstat-then-FindFirstFile's
//     0-vs-2-call asymmetry.
//
//  2. A directory HANDLE retained for the whole request (round-10 findings 2
//     and 4, the reparse-point/ancestor escape): winOpenScopedDir opens
//     scriptsDir exactly ONCE; the candidate probe (winProbeEntry) and the
//     actual open (winOpenEntry) are both performed RELATIVE TO THAT HANDLE
//     via windows.NtCreateFile with OBJECT_ATTRIBUTES.RootDirectory set to
//     it and ObjectName the bare basename — never a fresh path lookup that a
//     retargeted reparse point on the directory itself or an ancestor could
//     redirect. FILE_OPEN_REPARSE_POINT is the Windows analogue of O_NOFOLLOW
//     (opens the reparse point itself rather than following it), and
//     FILE_NON_DIRECTORY_FILE refuses a directory outright, atomically — no
//     check-then-open window exists for either to land in. The share mode
//     (FILE_SHARE_READ|WRITE|DELETE, finding 4) matches what open_windows.go
//     now also requests for the administrator's own read: a script being
//     read here can still be atomically replaced by a concurrent deploy.
//
// The post-open proof (verifyUnchanged) mirrors storednames_other.go's
// gen-before/gen-after recheck on the SAME retained handle — proving the
// directory itself was not swapped mid-request — plus, belt-and-suspenders,
// winOpenedBaseName (GetFinalPathNameByHandle on the OPENED file's own
// handle, basename only: the parent is already structurally bound by the
// relative open itself, so — unlike round 11's design, which had to compare
// the full path because opens were not yet handle-relative — only the
// basename is worth re-checking here).
//
// The index's own generation (winDirGeneration) folds the directory's
// IDENTITY — VolumeSerialNumber plus FileIndexHigh/Low, GetFileInformationByHandle,
// Windows's rough counterpart to a Unix dev+ino pair — together with its
// LastWriteTime, exactly as dirGeneration folds dev+ino with mtime/ctime on
// unix: a request whose retained handle resolves to a DIFFERENT directory
// than the one the index was built from (identity mismatch) is answered
// exactly like a stale generation — a plain miss, rebuild scheduled. The
// settle window (generationSettleTime, indexclock.go — shared with unix) is
// unchanged: NTFS timestamps are fine-grained, but a scripts directory is
// not guaranteed to live on an NTFS volume, and a FAT-formatted one carries
// the identical two-second write-time coarseness vfat has on Linux.

// winDirGeneration is Windows's counterpart to dirGeneration
// (storednames_other.go).
type winDirGeneration struct {
	volumeSerial                uint32
	fileIndexHigh, fileIndexLow uint32
	lastWrite                   time.Time
}

func (g winDirGeneration) equal(o winDirGeneration) bool {
	return g.volumeSerial == o.volumeSerial &&
		g.fileIndexHigh == o.fileIndexHigh && g.fileIndexLow == o.fileIndexLow &&
		g.lastWrite.Equal(o.lastWrite)
}

func (g winDirGeneration) latest() time.Time { return g.lastWrite }

// winStoredNames is storedNames's (storednames_other.go) Windows
// counterpart — see there for the full rationale behind every field; this
// struct is built from a retained directory HANDLE rather than a file
// descriptor.
type winStoredNames struct {
	mu      sync.Mutex
	names   map[string]struct{}
	err     error
	gen     winDirGeneration
	settled bool

	building bool
	landed   chan struct{}

	refreshAfter time.Time
	nextAttempt  time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// winStoredIndexes holds one *winStoredNames per cleaned scripts directory —
// the Windows counterpart of storedIndexes (storednames_other.go); see there
// for the LRU/pruning rationale, identical here.
var (
	winStoredIndexesMu  sync.Mutex
	winStoredIndexes    = map[string]*winStoredNames{}
	winStoredIndexesLRU []string
)

func winStoredNamesIndex(key string) *winStoredNames {
	winStoredIndexesMu.Lock()
	defer winStoredIndexesMu.Unlock()
	idx, ok := winStoredIndexes[key]
	if !ok {
		ctx, cancel := context.WithCancel(context.Background())
		idx = &winStoredNames{ctx: ctx, cancel: cancel}
		winStoredIndexes[key] = idx
	}
	winTouchIndexLocked(key)
	winEvictExcessLocked()
	return idx
}

func winTouchIndexLocked(key string) {
	for i, k := range winStoredIndexesLRU {
		if k == key {
			winStoredIndexesLRU = append(winStoredIndexesLRU[:i], winStoredIndexesLRU[i+1:]...)
			break
		}
	}
	winStoredIndexesLRU = append(winStoredIndexesLRU, key)
}

func winEvictExcessLocked() {
	for len(winStoredIndexesLRU) > maxStoredNameIndexes {
		oldest := winStoredIndexesLRU[0]
		winStoredIndexesLRU = winStoredIndexesLRU[1:]
		if idx, ok := winStoredIndexes[oldest]; ok {
			idx.cancel()
		}
		delete(winStoredIndexes, oldest)
	}
}

func winPruneOtherIndexesLocked(keep string) {
	for k, idx := range winStoredIndexes {
		if k != keep {
			idx.cancel()
			delete(winStoredIndexes, k)
		}
	}
	kept := winStoredIndexesLRU[:0]
	for _, k := range winStoredIndexesLRU {
		if k == keep {
			kept = append(kept, k)
		}
	}
	winStoredIndexesLRU = kept
}

// winForgetIndex removes one directory's index entirely; test-only (mirrors
// forgetIndex, storednames_other.go).
func winForgetIndex(key string) {
	winStoredIndexesMu.Lock()
	defer winStoredIndexesMu.Unlock()
	if idx, ok := winStoredIndexes[key]; ok {
		idx.cancel()
	}
	delete(winStoredIndexes, key)
	for i, k := range winStoredIndexesLRU {
		if k == key {
			winStoredIndexesLRU = append(winStoredIndexesLRU[:i], winStoredIndexesLRU[i+1:]...)
			break
		}
	}
}

// winForEachIndex calls fn for every currently held index; test-only
// (mirrors forEachIndex, storednames_other.go).
func winForEachIndex(fn func(*winStoredNames)) {
	winStoredIndexesMu.Lock()
	idxs := make([]*winStoredNames, 0, len(winStoredIndexes))
	for _, idx := range winStoredIndexes {
		idxs = append(idxs, idx)
	}
	winStoredIndexesMu.Unlock()
	for _, idx := range idxs {
		fn(idx)
	}
}

// Warm builds the stored-name index of scriptsDir on the caller's goroutine
// — the Windows counterpart of Warm (storednames_other.go); see there for
// the full rationale, identical here down to Warm's own rebuild never being
// cancelled by pruneOtherIndexesLocked's cancellation of every OTHER
// directory's index.
func Warm(scriptsDir string) error {
	key := filepath.Clean(scriptsDir)
	idx := winStoredNamesIndex(key)
	winStoredIndexesMu.Lock()
	winPruneOtherIndexesLocked(key)
	winStoredIndexesMu.Unlock()
	for {
		idx.mu.Lock()
		if !idx.building {
			idx.beginRebuildLocked()
			idx.mu.Unlock()
			break
		}
		landed := idx.landed
		idx.mu.Unlock()
		<-landed
	}
	idx.wg.Add(1)
	idx.rebuild(key, false, false)
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return idx.err
}

// storedSpellingsOf answers, for one scoped request, whether scriptsDir
// holds an entry spelled exactly `want`, bound to the SINGLE directory
// handle this call opens — see this file's package doc comment above and
// storednames_other.go's identical unix contract.
func storedSpellingsOf(scriptsDir string) (storedExactly func(want string) (bool, error), open func(path string) (*os.File, error), verifyUnchanged func(f *os.File, want string) error, closeSession func(), err error) {
	key := filepath.Clean(scriptsDir)

	dirHandle, err := winOpenScopedDir(key)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	closeSession = func() { _ = windows.CloseHandle(dirHandle) }

	gen, err := winFstatDirGeneration(dirHandle)
	if err != nil {
		closeSession()
		return nil, nil, nil, nil, err
	}

	names, lookupErr := winStoredNamesFor(key, gen)
	if lookupErr != nil {
		closeSession()
		return nil, nil, nil, nil, lookupErr
	}

	storedExactly = func(want string) (bool, error) {
		if _, ok := names[want]; !ok {
			return false, nil
		}
		if err := winProbeEntry(dirHandle, want); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	open = func(path string) (*os.File, error) {
		return winOpenEntry(dirHandle, filepath.Base(path))
	}
	verifyUnchanged = func(f *os.File, want string) error {
		cur, err := winFstatDirGeneration(dirHandle)
		if err != nil {
			return err
		}
		if !cur.equal(gen) {
			return errIndexGenerationChanged
		}
		// The parent is already bound by the relative open itself
		// (winOpenEntry, via RootDirectory) — only the basename is worth
		// re-checking here, unlike round 11's full-path comparison, which
		// existed only because that round's open was still a fresh, unbound
		// path lookup.
		got, err := winOpenedBaseName(f)
		if err != nil || got != want {
			return errSpellingUnproven
		}
		return nil
	}
	return storedExactly, open, verifyUnchanged, closeSession, nil
}

// winStoredNamesFor is storedNamesFor's (storednames_other.go) Windows
// counterpart: identical contract, one directory handle's freshly read
// generation in, the index's names (or nil, fail-closed) out.
func winStoredNamesFor(key string, gen winDirGeneration) (names map[string]struct{}, err error) {
	idx := winStoredNamesIndex(key)
	now := indexClock()

	idx.mu.Lock()
	defer idx.mu.Unlock()

	current := (idx.names != nil || idx.err != nil) && idx.gen.equal(gen)

	switch {
	case !current:
		idx.scheduleRebuildLocked(key, now)
	case !idx.settled && !now.Before(idx.refreshAfter):
		idx.scheduleRebuildLocked(key, now)
	}

	if !current || !idx.settled {
		return nil, nil
	}
	return idx.names, idx.err
}

// beginRebuildLocked claims the single-flight slot.
func (idx *winStoredNames) beginRebuildLocked() {
	idx.building = true
	idx.landed = make(chan struct{})
}

// scheduleRebuildLocked mirrors storedNames.scheduleRebuildLocked
// (storednames_other.go) exactly, rebuildSlots (round 13 SHOULD, finding 5)
// included: the semaphore is process-wide and shared with the unix index
// implementation (rebuildsemaphore.go), since the two never build together.
func (idx *winStoredNames) scheduleRebuildLocked(key string, now time.Time) {
	idx.refreshAfter = now.Add(generationSettleTime)
	if idx.building {
		return
	}
	if !idx.nextAttempt.IsZero() && now.Before(idx.nextAttempt) {
		return
	}
	select {
	case rebuildSlots <- struct{}{}:
	default:
		return
	}
	idx.beginRebuildLocked()
	idx.wg.Add(1)
	spawnIndexRebuild(func() {
		defer func() { <-rebuildSlots }()
		idx.rebuild(key, true, true)
	})
}

// rebuild mirrors storedNames.rebuild (storednames_other.go) exactly.
func (idx *winStoredNames) rebuild(key string, backoffAfter, cancellable bool) {
	defer idx.wg.Done()
	for attempt := 1; ; attempt++ {
		if cancellable && idx.ctx.Err() != nil {
			idx.finishRebuild(backoffAfter)
			return
		}
		before, after, names, listErr := winListScopedDirOnce(key)
		now := indexClock()
		if cancellable && idx.ctx.Err() != nil {
			idx.finishRebuild(backoffAfter)
			return
		}
		if listErr == nil && attempt < maxRebuildAttempts && !before.equal(after) {
			continue
		}
		gen := after
		if listErr != nil {
			gen = before
		}
		idx.mu.Lock()
		idx.names, idx.err, idx.gen = names, listErr, gen
		idx.settled = listErr == nil && now.Sub(gen.latest()) >= generationSettleTime
		idx.building = false
		if backoffAfter {
			idx.nextAttempt = indexClock().Add(rebuildBackoff)
		}
		close(idx.landed)
		idx.mu.Unlock()
		return
	}
}

// finishRebuild mirrors storedNames.finishRebuild (storednames_other.go).
func (idx *winStoredNames) finishRebuild(backoffAfter bool) {
	idx.mu.Lock()
	idx.building = false
	if backoffAfter {
		idx.nextAttempt = indexClock().Add(rebuildBackoff)
	}
	close(idx.landed)
	idx.mu.Unlock()
}

// winListScopedDirOnce mirrors defaultListScopedDirOnce (storednames_other.go):
// one directory handle serves the generation read AND the listing, so a
// change during the listing is caught by the before/after reads disagreeing
// without ever resolving the path a second time. A variable so the tests
// can inject the directory-open seam's behaviour directly.
var winListScopedDirOnce = defaultWinListScopedDirOnce

func defaultWinListScopedDirOnce(key string) (before, after winDirGeneration, names map[string]struct{}, err error) {
	h, err := winOpenScopedDir(key)
	if err != nil {
		return winDirGeneration{}, winDirGeneration{}, nil, err
	}
	defer func() { _ = windows.CloseHandle(h) }()

	before, err = winFstatDirGeneration(h)
	if err != nil {
		return winDirGeneration{}, winDirGeneration{}, nil, err
	}
	entryNames, err := winListScopedDir(h, key)
	if err != nil {
		return before, winDirGeneration{}, nil, err
	}
	after, err = winFstatDirGeneration(h)
	if err != nil {
		return before, winDirGeneration{}, nil, err
	}
	names = make(map[string]struct{}, len(entryNames))
	for _, n := range entryNames {
		names[n] = struct{}{}
	}
	return before, after, names, nil
}

// The primitives below are variables, exactly as dirfd_other.go's are, so
// the package's tests can hook them individually.
var (
	winOpenScopedDir      = defaultWinOpenScopedDir
	winFstatDirGeneration = defaultWinFstatDirGeneration
	winProbeEntry         = defaultWinProbeEntry
	winOpenEntry          = defaultWinOpenEntry
	winListScopedDir      = defaultWinListScopedDir
)

// defaultWinOpenScopedDir opens scriptsDir once. FILE_FLAG_BACKUP_SEMANTICS
// is required to obtain any handle on a directory at all; the share mode
// (round 13 MUST-FIX, finding 4) matches open_windows.go's own
// openScriptFile — READ|WRITE|DELETE — so holding this handle for the
// request's duration cannot itself block a concurrent write or atomic
// replace anywhere under the directory.
//
// Round 13 sibling sweep: unlike unix's O_DIRECTORY (dirfd_other.go),
// CreateFile with FILE_FLAG_BACKUP_SEMANTICS does not itself refuse a path
// that now names a plain FILE — the scripts directory replaced by a file is
// exactly the sibling class this round's sweep calls out — so the type is
// checked explicitly here, on the SAME handle everything else in the
// request is bound to, and refused (unreadable) rather than silently
// treating an ordinary file as an empty directory.
func defaultWinOpenScopedDir(path string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	h, err := windows.CreateFile(p,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0)
	if err != nil {
		return 0, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var fi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &fi); err != nil {
		_ = windows.CloseHandle(h)
		return 0, &os.PathError{Op: "open", Path: path, Err: err}
	}
	if fi.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		_ = windows.CloseHandle(h)
		return 0, &os.PathError{Op: "open", Path: path, Err: windows.ERROR_DIRECTORY}
	}
	return h, nil
}

// defaultWinFstatDirGeneration reads a directory's generation from an
// already-open handle — GetFileInformationByHandle, never a path lookup —
// so it can be called again, after the candidate open, without re-resolving
// scriptsDir. VolumeSerialNumber + FileIndexHigh/Low is the directory's
// IDENTITY (round 13, the maintainer's exact instruction — Windows's
// counterpart to a Unix dev+ino pair); LastWriteTime is what moves whenever
// the directory's entry set changes.
func defaultWinFstatDirGeneration(h windows.Handle) (winDirGeneration, error) {
	var fi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &fi); err != nil {
		return winDirGeneration{}, err
	}
	return winDirGeneration{
		volumeSerial:  fi.VolumeSerialNumber,
		fileIndexHigh: fi.FileIndexHigh,
		fileIndexLow:  fi.FileIndexLow,
		lastWrite:     time.Unix(0, fi.LastWriteTime.Nanoseconds()),
	}, nil
}

// defaultWinListScopedDir lists h's entries through a DUPLICATE of the
// handle: os.File.Close on the duplicate releases only the copy, leaving
// the caller's own h untouched. DuplicateHandle, not a fresh CreateFile,
// so the listing is bound to the identical open the generation came from —
// standard library os.File.Readdirnames on Windows lists via the handle
// itself (GetFileInformationByHandleEx), never by re-resolving the path
// string os.NewFile is given for bookkeeping.
func defaultWinListScopedDir(h windows.Handle, path string) ([]string, error) {
	cur := windows.CurrentProcess()
	var dup windows.Handle
	if err := windows.DuplicateHandle(cur, h, cur, &dup, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(dup), path)
	defer func() { _ = f.Close() }()
	return f.Readdirnames(-1)
}

// defaultWinProbeEntry probes name relative to dirHandle — the candidate's
// own existence check, bound to the SAME handle the generation was just
// read from rather than a fresh path lookup (round 13 MUST-FIX, finding 2:
// exactly the second, independent path resolution that let a retargeted
// reparse point substitute a different file). Minimal access
// (FILE_READ_ATTRIBUTES) and FILE_OPEN_REPARSE_POINT: this is existence
// only, mirroring fstatatEntry (dirfd_other.go) — it does not itself decide
// regular-vs-not; the actual open plus resolve's own f.Stat() does that.
func defaultWinProbeEntry(dirHandle windows.Handle, name string) error {
	h, err := ntCreateRelative(dirHandle, name,
		windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if err != nil {
		return &os.PathError{Op: "open", Path: name, Err: err}
	}
	_ = windows.CloseHandle(h)
	return nil
}

// defaultWinOpenEntry opens name relative to dirHandle — the entry
// winProbeEntry already probed, opened relative to the SAME handle, never a
// second, independent lookup of the name (round 13 MUST-FIX, finding 2).
// FILE_OPEN_REPARSE_POINT is the Windows analogue of O_NOFOLLOW (opens the
// reparse point itself, atomically, rather than transparently resolving it
// — the same no-check-then-open-window guarantee open_windows.go's own
// openScriptFile relies on); FILE_NON_DIRECTORY_FILE refuses a directory at
// the NtCreateFile layer itself. GetFileInformationByHandle on the opened
// handle then refuses a reparse point or (belt-and-suspenders) a directory,
// exactly as open_windows.go's openScriptFile does for the administrator.
func defaultWinOpenEntry(dirHandle windows.Handle, name string) (*os.File, error) {
	h, err := ntCreateRelative(dirHandle, name,
		windows.FILE_GENERIC_READ,
		windows.FILE_OPEN_REPARSE_POINT|windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	var fi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &fi); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	if fi.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		_ = windows.CloseHandle(h)
		return nil, errNonRegular
	}
	return os.NewFile(uintptr(h), name), nil
}

// winOpenedBaseName is this file's counterpart to darwin's F_GETPATH
// belt-and-suspenders proof (entryname_darwin.go): the on-disk basename
// GetFinalPathNameByHandle reports for the OPENED descriptor, not a
// separate pre-open probe of the same name.
func winOpenedBaseName(f *os.File) (string, error) {
	return openedBaseName(f)
}

// ntCreateRelative opens name relative to dirHandle via windows.NtCreateFile
// — OBJECT_ATTRIBUTES.RootDirectory bound to dirHandle, ObjectName the bare
// basename — so the lookup can never traverse outside dirHandle's own
// directory: a rename of the directory itself, or a reparse point planted
// on an ancestor, cannot redirect a RELATIVE open the way it could a fresh
// path lookup (round 13 MUST-FIX, finding 2). Share mode
// READ|WRITE|DELETE matches open_windows.go's own openScriptFile (round 13
// MUST-FIX, finding 4).
func ntCreateRelative(dirHandle windows.Handle, name string, access, options uint32) (windows.Handle, error) {
	objName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	oa := &windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: dirHandle,
		ObjectName:    objName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	var h windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	ntErr := windows.NtCreateFile(&h, access, oa, &iosb, nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		options,
		0, 0)
	if ntErr != nil {
		if st, ok := ntErr.(windows.NTStatus); ok {
			return 0, st.Errno()
		}
		return 0, ntErr
	}
	return h, nil
}
