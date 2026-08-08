package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseChecksums(t *testing.T) {
	const digestA = "1111111111111111111111111111111111111111111111111111111111111111"
	const digestB = "2222222222222222222222222222222222222222222222222222222222222222"

	manifest := strings.Join([]string{
		"# a comment line",
		digestA + "  mcpproxy-0.55.0-darwin-arm64.tar.gz",
		digestB + " *mcpproxy-0.55.0-windows-amd64.zip", // binary-mode marker
		"garbage line without a digest",
		"deadbeef  too-short-digest.txt",
		"",
	}, "\n")

	got, err := parseChecksums(strings.NewReader(manifest))
	if err != nil {
		t.Fatalf("parseChecksums: %v", err)
	}
	if got["mcpproxy-0.55.0-darwin-arm64.tar.gz"] != digestA {
		t.Errorf("darwin entry = %q, want %q", got["mcpproxy-0.55.0-darwin-arm64.tar.gz"], digestA)
	}
	if got["mcpproxy-0.55.0-windows-amd64.zip"] != digestB {
		t.Errorf("windows entry = %q (the '*' binary-mode marker must be stripped)", got["mcpproxy-0.55.0-windows-amd64.zip"])
	}
	if _, ok := got["too-short-digest.txt"]; ok {
		t.Errorf("a malformed digest must be skipped, not accepted")
	}
	if len(got) != 2 {
		t.Errorf("parsed %d entries, want 2: %v", len(got), got)
	}
}

func TestParseChecksums_EmptyManifestIsAnError(t *testing.T) {
	// An empty or unparseable manifest must never read as "nothing to verify".
	if _, err := parseChecksums(strings.NewReader("# only comments\n")); err == nil {
		t.Fatal("expected an error for a manifest with no usable entries")
	}
}

func TestVerifyFileSHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// sha256("hello")
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

	if err := verifyFileSHA256(path, want); err != nil {
		t.Errorf("matching digest should verify: %v", err)
	}
	if err := verifyFileSHA256(path, strings.ToUpper(want)); err != nil {
		t.Errorf("digest comparison should be case-insensitive: %v", err)
	}
	err := verifyFileSHA256(path, "0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %v, want a checksum mismatch", err)
	}
}

func TestExtractBinary_Zip(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "release.zip")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("mcpproxy.exe")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write([]byte("binary-bytes")); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	if err := os.WriteFile(archivePath, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	dest := filepath.Join(dir, "staged")
	if err := extractBinary(archivePath, "mcpproxy.exe", dest); err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read staged: %v", err)
	}
	if string(got) != "binary-bytes" {
		t.Errorf("staged content = %q", string(got))
	}
}

func TestExtractBinary_MissingMember(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "release.tar.gz")
	if err := os.WriteFile(archivePath, makeTarGz(t, "something-else", "x"), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	err := extractBinary(archivePath, "mcpproxy", filepath.Join(dir, "staged"))
	if err == nil || !strings.Contains(err.Error(), "does not contain") {
		t.Fatalf("error = %v, want a missing-member error", err)
	}
}

func TestExtractBinary_UnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "release.7z")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := extractBinary(path, "mcpproxy", filepath.Join(dir, "staged")); err == nil {
		t.Fatal("expected an error for an unsupported archive format")
	}
}

// applyNewBinary must preserve the target's mode and remove the backup only
// after verification succeeds (FR-021).
func TestApplyNewBinary_PreservesModeAndClearsBackup(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mcpproxy")
	staged := filepath.Join(dir, ".mcpproxy.new")

	if err := os.WriteFile(target, []byte("old"), 0o750); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Chmod(target, 0o750); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := os.WriteFile(staged, []byte("new"), 0o600); err != nil {
		t.Fatalf("write staged: %v", err)
	}

	verified := ""
	err := applyNewBinary(target, staged, func(path string) error {
		verified = path
		// The new binary must already be in place when verification runs.
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if string(content) != "new" {
			t.Errorf("verification saw %q, want the new binary", string(content))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("applyNewBinary: %v", err)
	}
	if verified != target {
		t.Errorf("verify was called with %q, want %q", verified, target)
	}

	fi, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o750 {
		t.Errorf("mode = %o, want 0750", fi.Mode().Perm())
	}
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Errorf("backup must be removed after successful verification")
	}
}

func TestApplyNewBinary_RestoresOnVerifyFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mcpproxy")
	staged := filepath.Join(dir, ".mcpproxy.new")

	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.WriteFile(staged, []byte("new"), 0o600); err != nil {
		t.Fatalf("write staged: %v", err)
	}

	err := applyNewBinary(target, staged, func(string) error { return os.ErrInvalid })
	if err == nil || !strings.Contains(err.Error(), "previous version restored") {
		t.Fatalf("error = %v, want a restore error", err)
	}

	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("target must exist after restore: %v", readErr)
	}
	if string(got) != "old" {
		t.Errorf("target content = %q, want the restored old binary", string(got))
	}
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Errorf("backup must not linger after a restore")
	}
}

// A stale .old left by an interrupted run must not block the next attempt.
func TestApplyNewBinary_OverwritesStaleBackup(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mcpproxy")
	staged := filepath.Join(dir, ".mcpproxy.new")

	for path, content := range map[string]string{
		target:            "old",
		staged:            "new",
		target + ".old":   "stale",
		target + ".other": "unrelated",
	} {
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	if err := applyNewBinary(target, staged, nil); err != nil {
		t.Fatalf("applyNewBinary: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "new" {
		t.Errorf("target content = %q, want new", string(got))
	}
}

// The pair of renames in applyNewBinary is not atomic: a crash between them
// leaves the target path empty with target.old holding the only copy of the
// binary. A retry must put it back, not delete it (FR-021).
func TestApplyNewBinary_RecoversFromAnInterruptedSwap(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mcpproxy")
	staged := filepath.Join(dir, ".mcpproxy.new")

	// The exact on-disk state after a crash between the two renames.
	if err := os.WriteFile(target+".old", []byte("old"), 0o750); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	if err := os.WriteFile(staged, []byte("new"), 0o600); err != nil {
		t.Fatalf("write staged: %v", err)
	}

	if err := applyNewBinary(target, staged, func(path string) error {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if string(content) != "new" {
			t.Errorf("verification saw %q, want the new binary", string(content))
		}
		return nil
	}); err != nil {
		t.Fatalf("applyNewBinary: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("target must exist: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("target content = %q, want new", string(got))
	}
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o750 {
		t.Errorf("mode = %o, want the recovered binary's 0750", fi.Mode().Perm())
	}
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Errorf("backup must be gone once the new binary verified")
	}
}

// The same interrupted state, but this attempt cannot proceed either. The
// previous binary must survive: it is the only one there is.
func TestApplyNewBinary_InterruptedSwapKeepsTheOnlyBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mcpproxy")
	staged := filepath.Join(dir, ".mcpproxy.new") // deliberately never created

	if err := os.WriteFile(target+".old", []byte("old"), 0o755); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	if err := applyNewBinary(target, staged, nil); err == nil {
		t.Fatal("expected an error: there is no staged binary to install")
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("the previous binary must have been restored: %v", err)
	}
	if string(got) != "old" {
		t.Errorf("target content = %q, want the restored old binary", string(got))
	}
}

// The other crash window: staged->target succeeded but verification never
// ran, so the target holds an UNVERIFIED binary and .old holds the last
// known-good one. On disk that is indistinguishable from a finished update
// unless the sentinel says otherwise — and mistaking it for one deletes the
// only known-good binary there is.
//
// Here the interrupted binary proves itself, so the retry may clean up.
func TestApplyNewBinary_RecoveryKeepsAVerifiedInterruptedSwap(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mcpproxy")
	staged := filepath.Join(dir, ".mcpproxy.new")

	writeAll(t, map[string]string{
		target:                      "interrupted-new",
		target + ".old":             "old",
		target + swapSentinelSuffix: "swap in progress",
		staged:                      "new",
	})

	var verified []string
	if err := applyNewBinary(target, staged, func(path string) error {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		verified = append(verified, string(content))
		return nil
	}); err != nil {
		t.Fatalf("applyNewBinary: %v", err)
	}

	if len(verified) == 0 || verified[0] != "interrupted-new" {
		t.Errorf("recovery must verify what the interrupted swap left behind, saw %v", verified)
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Errorf("target content = %q, want the freshly staged binary", string(got))
	}
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Error("the backup must be gone once the update completed")
	}
	assertNoSentinel(t, target)
}

// Same interrupted state, but what the crash left at the target does not run.
// The known-good binary must come back and the unverified one must go.
func TestApplyNewBinary_RecoveryRestoresOverAnUnverifiableInterruptedSwap(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mcpproxy")
	staged := filepath.Join(dir, ".mcpproxy.new")

	writeAll(t, map[string]string{
		target:                      "unverified",
		target + ".old":             "old",
		target + swapSentinelSuffix: "swap in progress",
		staged:                      "new",
	})

	// Rejects only what the interrupted run left behind.
	verify := func(path string) error {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if string(content) == "unverified" {
			return errors.New("does not run")
		}
		return nil
	}
	if err := applyNewBinary(target, staged, verify); err != nil {
		t.Fatalf("applyNewBinary: %v", err)
	}

	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Errorf("target content = %q, want the freshly staged binary", string(got))
	}
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Error("the backup must be gone once the update completed")
	}
	assertNoSentinel(t, target)
}

// The case the regression was really about: the retry cannot install anything,
// so the run must end on the last known-good binary rather than on the
// unverified one the crash left behind.
func TestApplyNewBinary_RecoveryNeverLeavesTheUnverifiedBinaryInstalled(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mcpproxy")
	staged := filepath.Join(dir, ".mcpproxy.new") // deliberately never created

	writeAll(t, map[string]string{
		target:                      "unverified",
		target + ".old":             "old",
		target + swapSentinelSuffix: "swap in progress",
	})

	err := applyNewBinary(target, staged, func(path string) error {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if string(content) == "unverified" {
			return errors.New("does not run")
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected an error: there is no staged binary to install")
	}

	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("the known-good binary must have been restored: %v", readErr)
	}
	if string(got) != "old" {
		t.Errorf("target content = %q, want the restored known-good binary", string(got))
	}
	assertNoSentinel(t, target)
}

// Without a verifier there is no way to prove the interrupted binary, so the
// known-good one wins by default.
func TestApplyNewBinary_RecoveryPrefersTheKnownGoodWhenItCannotVerify(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mcpproxy")
	staged := filepath.Join(dir, ".mcpproxy.new")

	writeAll(t, map[string]string{
		target:                      "unverified",
		target + ".old":             "old",
		target + swapSentinelSuffix: "swap in progress",
		staged:                      "new",
	})

	if err := applyNewBinary(target, staged, nil); err != nil {
		t.Fatalf("applyNewBinary: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Errorf("target content = %q, want the freshly staged binary", string(got))
	}
	assertNoSentinel(t, target)
}

// A crash before the first rename: the target is the binary that was always
// there, and there is nothing to undo.
func TestApplyNewBinary_RecoveryAcceptsATargetWithNoBackup(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mcpproxy")
	staged := filepath.Join(dir, ".mcpproxy.new")

	writeAll(t, map[string]string{
		target:                      "old",
		target + swapSentinelSuffix: "swap in progress",
		staged:                      "new",
	})

	if err := applyNewBinary(target, staged, nil); err != nil {
		t.Fatalf("applyNewBinary: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Errorf("target content = %q, want new", string(got))
	}
	assertNoSentinel(t, target)
}

// The sentinel has to be on disk for the whole window it describes, or the
// recovery above can never fire.
func TestApplyNewBinary_SentinelExistsForTheDurationOfTheSwap(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mcpproxy")
	staged := filepath.Join(dir, ".mcpproxy.new")

	writeAll(t, map[string]string{target: "old", staged: "new"})

	sawSentinel := false
	if err := applyNewBinary(target, staged, func(string) error {
		// Verification runs after both renames — the exact moment a crash
		// would leave an unverified binary installed.
		_, statErr := os.Stat(target + swapSentinelSuffix)
		sawSentinel = statErr == nil
		return nil
	}); err != nil {
		t.Fatalf("applyNewBinary: %v", err)
	}
	if !sawSentinel {
		t.Error("the swap must be marked in progress while the new binary is unverified")
	}
	assertNoSentinel(t, target)
}

func writeAll(t *testing.T, files map[string]string) {
	t.Helper()
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil { // #nosec G306 -- test fixture standing in for an executable
			t.Fatalf("write %s: %v", path, err)
		}
	}
}

func assertNoSentinel(t *testing.T, target string) {
	t.Helper()
	if _, err := os.Stat(target + swapSentinelSuffix); !os.IsNotExist(err) {
		t.Errorf("%s must not survive a completed run", target+swapSentinelSuffix)
	}
}

func TestApplyNewBinary_MissingTargetAndBackupIsAnError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mcpproxy")
	staged := filepath.Join(dir, ".mcpproxy.new")
	if err := os.WriteFile(staged, []byte("new"), 0o600); err != nil {
		t.Fatalf("write staged: %v", err)
	}

	err := applyNewBinary(target, staged, nil)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error = %v, want a refusal to install over nothing", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Error("nothing may be installed where there was no binary to update")
	}
}

func TestEnsureTargetWritable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mcpproxy")
	if err := os.WriteFile(target, []byte("x"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := ensureTargetWritable(target); err != nil {
		t.Errorf("a writable directory should pass: %v", err)
	}

	// The probe must not litter.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("probe left files behind: %v", entries)
	}
}

func TestVerifyInstalledVersion(t *testing.T) {
	requirePOSIXShell(t)

	dir := t.TempDir()
	bin := filepath.Join(dir, "fake")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"MCPProxy v1.2.3 (personal)\"\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := verifyInstalledVersion(bin, "v1.2.3"); err != nil {
		t.Errorf("matching version should verify: %v", err)
	}
	if err := verifyInstalledVersion(bin, "v9.9.9"); err == nil {
		t.Error("a mismatched version must fail verification")
	}

	broken := filepath.Join(dir, "broken")
	if err := os.WriteFile(broken, []byte("not an executable"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := verifyInstalledVersion(broken, "v1.2.3"); err == nil {
		t.Error("a binary that cannot run must fail verification")
	}
}

// A substring test would accept 0.54.10 as proof that 0.54.1 was installed.
func TestVerifyInstalledVersion_RejectsAVersionPrefix(t *testing.T) {
	requirePOSIXShell(t)

	dir := t.TempDir()
	bin := filepath.Join(dir, "fake")
	if err := os.WriteFile(bin,
		[]byte("#!/bin/sh\necho \"MCPProxy 0.54.10 (personal) darwin/arm64\"\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := verifyInstalledVersion(bin, "v0.54.1"); err == nil {
		t.Error("0.54.10 must not satisfy a request for 0.54.1")
	}
	if err := verifyInstalledVersion(bin, "v0.54.10"); err != nil {
		t.Errorf("the exact version should verify: %v", err)
	}
}

func TestReportsVersion(t *testing.T) {
	const line = "MCPProxy 0.54.10 (personal) darwin/arm64"

	for _, tc := range []struct {
		output, want string
		match        bool
	}{
		{line, "v0.54.10", true},
		{line, "0.54.10", true},
		{line, "v0.54.1", false},
		{line, "0.54.100", false},
		{line, "", false},
		{"MCPProxy v1.0.0-rc.2 (personal)", "v1.0.0-rc.2", true},
		{"MCPProxy v1.0.0-rc.2 (personal)", "v1.0.0-rc.20", false},
		// Parentheses around a bare version must not defeat the token match.
		{"mcpproxy (1.2.3)", "v1.2.3", true},
	} {
		if got := reportsVersion(tc.output, tc.want); got != tc.match {
			t.Errorf("reportsVersion(%q, %q) = %v, want %v", tc.output, tc.want, got, tc.match)
		}
	}
}

func TestAcquireUpdateLock_SecondHolderIsRefused(t *testing.T) {
	target := filepath.Join(t.TempDir(), "mcpproxy")

	release, err := acquireUpdateLock(target)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	// flock/LockFileEx conflict even between two opens in the same process,
	// so this stands in for a concurrent `mcpproxy update` invocation.
	if _, err := acquireUpdateLock(target); !errors.Is(err, errUpdateInProgress) {
		t.Fatalf("second acquire: want errUpdateInProgress, got %v", err)
	}

	release()
	release2, err := acquireUpdateLock(target)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	release2()
}

func TestApplyNewBinary_RefusedWhileAnotherSwapHoldsTheLock(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mcpproxy")
	staged := filepath.Join(dir, "staged")
	if err := os.WriteFile(target, []byte("current"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.WriteFile(staged, []byte("new"), 0o600); err != nil {
		t.Fatalf("write staged: %v", err)
	}

	release, err := acquireUpdateLock(target)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	if err := applyNewBinary(target, staged, nil); !errors.Is(err, errUpdateInProgress) {
		t.Fatalf("applyNewBinary under a held lock: want errUpdateInProgress, got %v", err)
	}
	// The refused attempt must not have touched anything.
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read target: %v", readErr)
	}
	if string(got) != "current" {
		t.Fatalf("target changed by refused swap: %q", got)
	}
}
