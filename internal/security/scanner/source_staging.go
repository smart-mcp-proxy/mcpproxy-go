package scanner

import (
	"context"
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
	stagedSourceReadBatch        = 1024    // directory entries read per ReadDir call
)

// stagingAfterReadBatch, when set by a test, runs after each batch of
// directory entries is read, with the batch size.
var stagingAfterReadBatch func(n int)

// copyChunkSize bounds each read of a file copy so the context is checked
// between chunks.
const copyChunkSize = 256 << 10

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
func (e *Engine) stageScannerSource(ctx context.Context, s *ScannerPlugin, req ScanRequest) (string, func(), error) {
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
	// The staging area itself is never copied (Sol r21 P2): a source dir that
	// contains the proxy's data dir (a working dir of $HOME) would otherwise
	// copy the copy into itself.
	baseInfo, err := os.Stat(base)
	if err != nil {
		cleanup()
		return "", noop, fmt.Errorf("prepare scanner source: %w", err)
	}
	if err := copySourceTree(ctx, req.SourceDir, dir, baseInfo); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("stage scanner source: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools.json"), req.ToolDefinitions, 0o444); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("write tool definitions snapshot: %w", err)
	}
	return dir, cleanup, nil
}

// stagingAfterLstat, when set by a test, runs after an entry is Lstat'ed and
// before it is opened, to simulate an upstream swapping it in that window.
var stagingAfterLstat func(rel string)

// copySourceTree copies src into the empty directory dst without following
// any symlink out of src. The top-level tools.json is skipped: the caller
// writes the exported snapshot in its place. The directory skip (the staging
// area) is not copied. It stops when ctx is done.
func copySourceTree(ctx context.Context, src, dst string, skip os.FileInfo) error {
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
		// Non-blocking, and checked after open (Sol r21 P2): a directory
		// swapped for a FIFO after its Lstat must not block the scan.
		d, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		dinfo, err := d.Stat()
		if err != nil || !dinfo.IsDir() {
			_ = d.Close()
			return err
		}
		// Read in bounded batches and charge the entry budget as entries
		// arrive, so a directory with millions of entries fails closed at
		// the cap without being fully allocated (#1602).
		var list []os.DirEntry
		for {
			if err := ctx.Err(); err != nil {
				_ = d.Close()
				return err
			}
			batch, rerr := d.ReadDir(stagedSourceReadBatch)
			if stagingAfterReadBatch != nil {
				stagingAfterReadBatch(len(batch))
			}
			for _, de := range batch {
				if rel == "" && de.Name() == "tools.json" {
					continue
				}
				entries++
				if entries > stagedSourceMaxEntries {
					_ = d.Close()
					return fmt.Errorf("%w (more than %d entries)", errStagedSourceTooLarge, stagedSourceMaxEntries)
				}
				list = append(list, de)
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				_ = d.Close()
				return rerr
			}
		}
		_ = d.Close()
		for _, de := range list {
			if err := ctx.Err(); err != nil {
				return err
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
			if stagingAfterLstat != nil {
				stagingAfterLstat(child)
			}
			switch mode := info.Mode(); {
			case mode.IsDir():
				if skip != nil && os.SameFile(info, skip) {
					continue
				}
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
				if err := copyRegularFromRoot(ctx, root, child, target, mode.Perm(), &budget); err != nil {
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
func copyRegularFromRoot(ctx context.Context, root *os.Root, name, target string, perm os.FileMode, budget *int64) error {
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
	n, err := copyWithContext(ctx, out, io.LimitReader(f, *budget+1))
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

// copyWithContext copies src to dst in bounded chunks, checking ctx between
// chunks so a huge or slow file cannot outlive the scan deadline.
func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, copyChunkSize)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		nr, rerr := src.Read(buf)
		if nr > 0 {
			nw, werr := dst.Write(buf[:nr])
			total += int64(nw)
			if werr != nil {
				return total, werr
			}
			if nw < nr {
				return total, io.ErrShortWrite
			}
		}
		if rerr == io.EOF {
			return total, nil
		}
		if rerr != nil {
			return total, rerr
		}
	}
}
