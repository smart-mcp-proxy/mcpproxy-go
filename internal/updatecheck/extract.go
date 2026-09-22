package updatecheck

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extract.go holds release-archive extraction, shared by `mcpproxy update`
// (cmd/mcpproxy) and the tray's self-update path (internal/tray). Both install
// a binary out of the same published archive, and both must name the member
// they want exactly: a release .tar.gz/.zip ships the core binary AND the tray
// binary side by side, so "the first entry" or "something ending in mcpproxy"
// picks the wrong one and swaps the wrong file into place.

// MaxArchiveMemberBytes bounds a single extracted archive member. The core
// binary is ~60-90 MB; the cap exists so a malicious archive cannot fill the
// disk before the checksum comparison would have rejected it.
const MaxArchiveMemberBytes = 512 << 20

// ExtractBinary pulls the single archive member named memberName (matched on
// base name, so a future archive that nests files still works) into destPath,
// which is created with mode 0o700 — the caller re-applies the real mode when
// swapping it into place. An archive that does not contain memberName is an
// error: callers install a specific binary, never "whatever was in there".
func ExtractBinary(archivePath, memberName, destPath string) error {
	switch {
	case strings.HasSuffix(archivePath, ".zip"):
		return extractFromZip(archivePath, memberName, destPath)
	case strings.HasSuffix(archivePath, ".tar.gz"), strings.HasSuffix(archivePath, ".tgz"):
		return extractFromTarGz(archivePath, memberName, destPath)
	default:
		return fmt.Errorf("unsupported archive format: %s", filepath.Base(archivePath))
	}
}

func extractFromTarGz(archivePath, memberName, destPath string) error {
	f, err := os.Open(archivePath) // #nosec G304 -- self-downloaded temp file
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != memberName {
			continue
		}
		return writeMember(tr, destPath)
	}
	return fmt.Errorf("archive does not contain %q", memberName)
}

func extractFromZip(archivePath, memberName, destPath string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer zr.Close()

	for _, entry := range zr.File {
		if entry.FileInfo().IsDir() || filepath.Base(entry.Name) != memberName {
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open archive member: %w", err)
		}
		defer rc.Close()
		return writeMember(rc, destPath)
	}
	return fmt.Errorf("archive does not contain %q", memberName)
}

// writeMember copies at most MaxArchiveMemberBytes from r into destPath.
func writeMember(r io.Reader, destPath string) error {
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_EXCL, 0o700) // #nosec G304 -- destPath is our own temp path
	if err != nil {
		return fmt.Errorf("create staged binary: %w", err)
	}
	written, err := io.Copy(out, io.LimitReader(r, MaxArchiveMemberBytes+1))
	if err != nil {
		out.Close()
		return fmt.Errorf("write staged binary: %w", err)
	}
	if written > MaxArchiveMemberBytes {
		out.Close()
		return fmt.Errorf("archive member exceeds the %d-byte limit", int64(MaxArchiveMemberBytes))
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return fmt.Errorf("flush staged binary: %w", err)
	}
	return out.Close()
}
