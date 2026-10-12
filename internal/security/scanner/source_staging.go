package scanner

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Limits for staging a metadata-dependent Docker scanner's source dir. A
// source over either limit is not scanned by such a scanner (fail closed).
// They are variables so tests can lower them.
var (
	stagedSourceMaxBytes   int64 = 1 << 30 // 1 GiB of regular-file content
	stagedSourceMaxEntries       = 100_000 // files, dirs and symlinks
)

var errStagedSourceTooLarge = errors.New("source dir is too large to stage for a scanner that reads tool metadata")

// stageScannerSource returns the host directory a Docker scanner mounts as
// its source (/scan/source and /src), and a cleanup that removes it.
//
// A scanner that reads the exported tool definitions gets a proxy-owned copy
// of the source dir with tools.json replaced by the exported snapshot (RT-06).
// The upstream may be able to write its own source dir (a stdio server's
// working dir), so nothing the scanner sees may resolve through a path in that
// dir once the container exists: laying the snapshot over
// <source>/tools.json with a bind mount let a symlink there decide where the
// snapshot landed, and retargeting the link mid-run handed the scanner clean
// metadata (Sol r20 P2). The copy is made through an os.Root, so no symlink
// in the source dir leads the copy outside it; symlinks are copied as
// symlinks (they resolve inside the container, as they did before), and
// devices, sockets and FIFOs are skipped.
//
// Scanners independent of tool metadata, and scans that exported nothing,
// mount the source dir itself as before.
func (e *Engine) stageScannerSource(s *ScannerPlugin, req ScanRequest) (string, func(), error) {
	noop := func() {}
	if toolMetadataIndependentScanners[s.ID] || len(req.ToolDefinitions) == 0 || req.SourceDir == "" {
		return req.SourceDir, noop, nil
	}
	base := filepath.Join(e.dataDir, "scanner-snapshots")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", noop, fmt.Errorf("prepare scanner source: %w", err)
	}
	// A constant pattern (Sol r6 P2): os.MkdirTemp rejects a pattern with a
	// path separator, and the server name may hold '/' (MCP-2123).
	dir, err := os.MkdirTemp(base, "source-")
	if err != nil {
		return "", noop, fmt.Errorf("prepare scanner source: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := copySourceTree(req.SourceDir, dir); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("stage scanner source: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools.json"), req.ToolDefinitions, 0o444); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("write tool definitions snapshot: %w", err)
	}
	return dir, cleanup, nil
}

// copySourceTree copies src into the empty directory dst without following
// any symlink out of src. The top-level tools.json is skipped: the caller
// writes the exported snapshot in its place.
func copySourceTree(src, dst string) error {
	root, err := os.OpenRoot(src)
	if err != nil {
		return err
	}
	defer root.Close()

	var entries int
	var budget = stagedSourceMaxBytes
	var walk func(rel string) error
	walk = func(rel string) error {
		name := rel
		if name == "" {
			name = "."
		}
		d, err := root.Open(name)
		if err != nil {
			return err
		}
		list, err := d.ReadDir(-1)
		_ = d.Close()
		if err != nil {
			return err
		}
		for _, de := range list {
			if rel == "" && de.Name() == "tools.json" {
				continue
			}
			entries++
			if entries > stagedSourceMaxEntries {
				return fmt.Errorf("%w (more than %d entries)", errStagedSourceTooLarge, stagedSourceMaxEntries)
			}
			child := filepath.Join(rel, de.Name())
			target := filepath.Join(dst, child)
			info, err := root.Lstat(child)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue // removed while copying
				}
				return err
			}
			switch mode := info.Mode(); {
			case mode.IsDir():
				if err := os.Mkdir(target, 0o755); err != nil {
					return err
				}
				if err := walk(child); err != nil {
					return err
				}
			case mode&os.ModeSymlink != 0:
				link, err := root.Readlink(child)
				if err != nil {
					continue
				}
				// Best effort: a host that cannot create symlinks (Windows
				// without the privilege) scans without them.
				_ = os.Symlink(link, target)
			case mode.IsRegular():
				if err := copyRegularFromRoot(root, child, target, mode.Perm(), &budget); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk("")
}

// copyRegularFromRoot copies one regular file out of root, charging its size
// to budget. The file is opened non-blocking and re-checked after open, so a
// FIFO swapped in after the Lstat is skipped instead of blocking the copy.
func copyRegularFromRoot(root *os.Root, name, target string, perm os.FileMode, budget *int64) error {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm|0o444)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(f, *budget+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	*budget -= n
	if *budget < 0 {
		return fmt.Errorf("%w (more than %d bytes)", errStagedSourceTooLarge, stagedSourceMaxBytes)
	}
	return nil
}
