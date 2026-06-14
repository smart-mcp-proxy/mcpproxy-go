package scanner

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// Package source fetch (MCP-2206)
//
// Package-runner servers (npx, uvx) are the PRIMARY quarantine/scan target, yet
// when a server is quarantined-on-add it has never run locally, so the local
// package cache misses and the scanner falls back to a tool-definitions-only
// scan (no real source-level analysis). This file adds a resolution strategy
// that fetches the PUBLISHED package source WITHOUT EXECUTING IT, so the AI and
// supply-chain scanners can run against real code.
//
// Security crux: a scanner must never execute the untrusted code it is about to
// scan. We therefore only ever DOWNLOAD (npm pack / uv pip download / pip
// download) and EXTRACT archives. We never install, build, or run setup.py. npm
// pack uses --ignore-scripts; the Python path passes --no-deps AND
// --only-binary=:all: so pip/uv only ever fetch a prebuilt wheel — downloading
// an sdist would invoke its PEP 517 build backend (setup.py egg_info) to resolve
// metadata, executing the package's code. A package with no wheel therefore
// fails the fetch and falls back to tool-definitions-only. Archive extraction is
// hardened against path traversal (zip-slip), symlink escape, and decompression
// bombs (bounded file count + total size).

const (
	// fetchMaxFiles caps the number of files extracted from a fetched package.
	fetchMaxFiles = 20000
	// fetchMaxTotalBytes caps the total uncompressed size extracted from a
	// fetched package (decompression-bomb guard).
	fetchMaxTotalBytes int64 = 256 << 20 // 256 MiB
	// fetchMaxFileBytes caps any single extracted file.
	fetchMaxFileBytes int64 = 64 << 20 // 64 MiB
)

type archiveKind int

const (
	archiveTarGz archiveKind = iota
	archiveZip
)

// parsePackageSpec splits a package spec into its name and exact version.
// Only exact pins are honored ("pkg@1.2.3", "pkg==1.2.3"); range/min specifiers
// (">=", "~", "^") yield an empty version so the package manager resolves the
// version it would actually run. Scoped npm names ("@scope/name") keep their
// leading '@'.
func parsePackageSpec(spec string) (name, version string) {
	if spec == "" {
		return "", ""
	}
	// PEP 508 exact pin: pkg==1.2.3
	if idx := strings.Index(spec, "=="); idx > 0 {
		return spec[:idx], spec[idx+2:]
	}
	// Range/compatible specifiers — strip to bare name, no version.
	for _, op := range []string{">=", "<=", "~=", "!=", ">", "<", "~", "^"} {
		if idx := strings.Index(spec, op); idx > 0 {
			return spec[:idx], ""
		}
	}
	// npm version pin: name@1.2.3 or @scope/name@1.2.3. The version '@' is the
	// LAST '@', and only counts when it is not the scope's leading '@'.
	if idx := strings.LastIndex(spec, "@"); idx > 0 {
		return spec[:idx], spec[idx+1:]
	}
	return spec, ""
}

// firstPackageArg returns the package spec from a runner command's args. It
// honors `--from <pkg>` (uvx) and otherwise returns the first non-flag arg.
func firstPackageArg(args []string) string {
	for i, arg := range args {
		if arg == "--from" && i+1 < len(args) {
			return args[i+1]
		}
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

// npmPackArgs builds the `npm pack` argument list. `npm pack <remote-spec>`
// downloads the published tarball and re-packs it WITHOUT running the package's
// lifecycle scripts; --ignore-scripts makes that guarantee explicit.
func npmPackArgs(spec, destDir string) []string {
	return []string{"pack", spec, "--ignore-scripts", "--pack-destination", destDir}
}

// uvDownloadArgs builds the `uv pip download` argument list. --no-deps avoids
// pulling the dependency tree (we only scan the target's own source).
// --only-binary=:all: forces a wheel and NEVER builds an sdist: a plain
// `pip/uv download` of an sdist invokes the package's PEP 517 build backend
// (setup.py egg_info) to resolve metadata, which would EXECUTE the untrusted
// code we are about to scan. With this flag, a package that ships no wheel makes
// the download fail, and the caller falls back to tool-definitions-only.
func uvDownloadArgs(spec, destDir string) []string {
	return []string{"pip", "download", spec, "--no-deps", "--only-binary=:all:", "-d", destDir}
}

// pipDownloadArgs builds the `pip download` argument list (fallback when uv is
// unavailable). --no-deps and --only-binary=:all: for the same reasons as uv —
// the latter guarantees we never build (and thus never execute) an sdist.
func pipDownloadArgs(spec, destDir string) []string {
	return []string{"download", spec, "--no-deps", "--only-binary=:all:", "-d", destDir}
}

// findDownloadedArchive locates the archive produced by a download command in
// dir. A wheel (.whl) is returned immediately. npm pack writes a single .tgz.
// A bare .tar.gz (a Python sdist) is reported as a last resort so the caller can
// detect and REFUSE it — the Python path only accepts wheels (--only-binary=:all:),
// because building/extracting an sdist would execute the package's code.
func findDownloadedArchive(dir string) (path string, kind archiveKind, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, err
	}
	var tgz, sdist string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		full := filepath.Join(dir, e.Name())
		switch {
		case strings.HasSuffix(name, ".whl"):
			// Wheel: prefer immediately.
			return full, archiveZip, nil
		case strings.HasSuffix(name, ".tgz"):
			tgz = full
		case strings.HasSuffix(name, ".tar.gz"):
			sdist = full
		}
	}
	if tgz != "" {
		return tgz, archiveTarGz, nil
	}
	if sdist != "" {
		return sdist, archiveTarGz, nil
	}
	return "", 0, fmt.Errorf("no package archive found in %s", dir)
}

// safeJoin joins destDir with a (possibly hostile) archive entry name and
// returns an error if the result would escape destDir (path traversal).
func safeJoin(destDir, name string) (string, error) {
	cleaned := filepath.Clean("/" + name) // make absolute then strip leading /
	target := filepath.Join(destDir, cleaned)
	rel, err := filepath.Rel(destDir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("entry %q escapes destination", name)
	}
	return target, nil
}

// extractTarballGz extracts a gzip-compressed tar stream into destDir. It is
// hardened for UNTRUSTED input: regular files and directories only (symlinks,
// hardlinks, devices skipped), path-traversal entries skipped, and bounded by
// maxFiles and maxTotalBytes.
func extractTarballGz(r io.Reader, destDir string, maxFiles int, maxTotalBytes int64) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var fileCount int
	var totalBytes int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		// Only regular files and directories. Symlinks/hardlinks are escape
		// vectors and are never needed for source scanning.
		switch hdr.Typeflag {
		case tar.TypeDir:
			target, jerr := safeJoin(destDir, hdr.Name)
			if jerr != nil {
				continue
			}
			_ = os.MkdirAll(target, 0o755)
			continue
		case tar.TypeReg, tar.TypeRegA: //nolint:staticcheck // TypeRegA for legacy tars
			// handled below
		default:
			continue // symlink, hardlink, char/block device, fifo — skip
		}

		target, jerr := safeJoin(destDir, hdr.Name)
		if jerr != nil {
			continue
		}
		if fileCount >= maxFiles {
			return fmt.Errorf("package exceeds max file count (%d)", maxFiles)
		}
		if hdr.Size > fetchMaxFileBytes {
			continue // skip oversized single file rather than abort
		}
		if totalBytes+hdr.Size > maxTotalBytes {
			return fmt.Errorf("package exceeds max total size (%d bytes)", maxTotalBytes)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			continue
		}
		written, werr := writeArchiveFile(target, tr, hdr.Size)
		if werr != nil {
			continue
		}
		totalBytes += written
		fileCount++
	}
	return nil
}

// extractZip extracts a zip archive (e.g. a Python wheel) into destDir with the
// same untrusted-input hardening as extractTarballGz.
func extractZip(zipPath, destDir string, maxFiles int, maxTotalBytes int64) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("zip: %w", err)
	}
	defer zr.Close()

	var fileCount int
	var totalBytes int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			if target, jerr := safeJoin(destDir, f.Name); jerr == nil {
				_ = os.MkdirAll(target, 0o755)
			}
			continue
		}
		// Skip symlinks (mode bit) — escape vector.
		if f.Mode()&os.ModeSymlink != 0 {
			continue
		}
		target, jerr := safeJoin(destDir, f.Name)
		if jerr != nil {
			continue
		}
		if fileCount >= maxFiles {
			return fmt.Errorf("package exceeds max file count (%d)", maxFiles)
		}
		size := int64(f.UncompressedSize64)
		if size > fetchMaxFileBytes {
			continue
		}
		if totalBytes+size > maxTotalBytes {
			return fmt.Errorf("package exceeds max total size (%d bytes)", maxTotalBytes)
		}
		rc, oerr := f.Open()
		if oerr != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			rc.Close()
			continue
		}
		written, werr := writeArchiveFile(target, rc, size)
		rc.Close()
		if werr != nil {
			continue
		}
		totalBytes += written
		fileCount++
	}
	return nil
}

// writeArchiveFile copies up to limit+1 bytes from src into a new file at
// target, guarding against a declared-size mismatch (lie in the header).
func writeArchiveFile(target string, src io.Reader, limit int64) (int64, error) {
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	// Cap the copy in case the header under-reports the real size.
	n, err := io.Copy(out, io.LimitReader(src, limit+1))
	if err != nil {
		return n, err
	}
	if n > limit {
		return n, fmt.Errorf("file exceeded declared size")
	}
	return n, nil
}

// resolveFromPackageFetch fetches and extracts the published source of a
// package-runner server (npx/uvx) WITHOUT executing it, returning a
// ResolvedSource pointing at the extracted tree. The caller must invoke the
// returned Cleanup to remove the temp dir.
//
// Returns an error (and the caller falls back to tool_definitions_only) when
// the toolchain is missing, the host is offline, or the fetch/extract fails —
// guaranteeing no regression versus today's behavior.
func (r *SourceResolver) resolveFromPackageFetch(ctx context.Context, info ServerInfo) (*ResolvedSource, error) {
	cmdBase := strings.ToLower(filepath.Base(info.Command))
	spec := firstPackageArg(info.Args)
	if spec == "" {
		return nil, fmt.Errorf("no package spec in args for %s", info.Name)
	}

	switch cmdBase {
	case "npx", "bunx", "pnpm":
		return r.fetchNpmPackage(ctx, info, spec)
	case "uvx", "pipx":
		return r.fetchPythonPackage(ctx, info, spec)
	}
	return nil, fmt.Errorf("package fetch unsupported for command %q", cmdBase)
}

// fetchNpmPackage downloads a published npm package via `npm pack` and extracts
// the resulting tarball.
func (r *SourceResolver) fetchNpmPackage(ctx context.Context, info ServerInfo, spec string) (*ResolvedSource, error) {
	npmBin, err := exec.LookPath("npm")
	if err != nil {
		return nil, fmt.Errorf("npm not found on PATH: %w", err)
	}

	dlDir, err := os.MkdirTemp("", "mcpproxy-fetch-npm-")
	if err != nil {
		return nil, err
	}
	srcDir := filepath.Join(dlDir, "extracted")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		os.RemoveAll(dlDir)
		return nil, err
	}
	cleanup := func() { os.RemoveAll(dlDir) }

	cmd := exec.CommandContext(ctx, npmBin, npmPackArgs(spec, dlDir)...)
	cmd.Dir = dlDir
	if out, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return nil, fmt.Errorf("npm pack failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	archive, kind, err := findDownloadedArchive(dlDir)
	if err != nil || kind != archiveTarGz {
		cleanup()
		return nil, fmt.Errorf("npm pack produced no tarball: %w", err)
	}
	f, err := os.Open(archive)
	if err != nil {
		cleanup()
		return nil, err
	}
	defer f.Close()
	if err := extractTarballGz(f, srcDir, fetchMaxFiles, fetchMaxTotalBytes); err != nil {
		cleanup()
		return nil, fmt.Errorf("extract npm tarball: %w", err)
	}

	r.logger.Info("Fetched published npm package source for scan",
		zap.String("server", info.Name),
		zap.String("spec", spec),
		zap.String("source_dir", srcDir),
	)
	return &ResolvedSource{SourceDir: srcDir, Method: "npm_pack", Cleanup: cleanup}, nil
}

// fetchPythonPackage downloads a published Python package via `uv pip download`
// (falling back to `pip download`) and extracts the wheel or sdist.
func (r *SourceResolver) fetchPythonPackage(ctx context.Context, info ServerInfo, spec string) (*ResolvedSource, error) {
	dlDir, err := os.MkdirTemp("", "mcpproxy-fetch-py-")
	if err != nil {
		return nil, err
	}
	srcDir := filepath.Join(dlDir, "extracted")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		os.RemoveAll(dlDir)
		return nil, err
	}
	cleanup := func() { os.RemoveAll(dlDir) }

	// Prefer uv, fall back to pip. Both pass --only-binary=:all: so only a wheel
	// is ever downloaded — building an sdist would execute the package's code.
	if err := r.runPythonDownload(ctx, dlDir, spec); err != nil {
		cleanup()
		return nil, err
	}

	archive, kind, err := findDownloadedArchive(dlDir)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("python download produced no archive: %w", err)
	}
	// Wheel-only: --only-binary=:all: guarantees a wheel, but we refuse anything
	// else as defense-in-depth — extracting (or building) an sdist would run the
	// untrusted package's setup.py / PEP 517 backend. A package that ships no
	// wheel falls back to tool-definitions-only.
	if kind != archiveZip {
		cleanup()
		return nil, fmt.Errorf("python download produced a non-wheel archive; refusing to build/extract an sdist (would execute untrusted code)")
	}
	if err := extractZip(archive, srcDir, fetchMaxFiles, fetchMaxTotalBytes); err != nil {
		cleanup()
		return nil, fmt.Errorf("extract wheel: %w", err)
	}

	r.logger.Info("Fetched published Python package source for scan",
		zap.String("server", info.Name),
		zap.String("spec", spec),
		zap.String("source_dir", srcDir),
	)
	return &ResolvedSource{SourceDir: srcDir, Method: "pip_download", Cleanup: cleanup}, nil
}

// runPythonDownload tries `uv pip download` first, then `pip download`. Both
// pass --only-binary=:all:, so only a prebuilt wheel is fetched and no code runs
// (a sdist download would build the package). If the package ships no wheel the
// command fails and the caller falls back to tool-definitions-only.
func (r *SourceResolver) runPythonDownload(ctx context.Context, dlDir, spec string) error {
	if uvBin, err := exec.LookPath("uv"); err == nil {
		cmd := exec.CommandContext(ctx, uvBin, uvDownloadArgs(spec, dlDir)...)
		cmd.Dir = dlDir
		if out, err := cmd.CombinedOutput(); err == nil {
			return nil
		} else {
			r.logger.Debug("uv pip download failed, trying pip",
				zap.String("spec", spec), zap.Error(err),
				zap.String("output", strings.TrimSpace(string(out))))
		}
	}
	pipBin, err := exec.LookPath("pip")
	if err != nil {
		pipBin, err = exec.LookPath("pip3")
		if err != nil {
			return fmt.Errorf("neither uv nor pip found on PATH")
		}
	}
	cmd := exec.CommandContext(ctx, pipBin, pipDownloadArgs(spec, dlDir)...)
	cmd.Dir = dlDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pip download failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
