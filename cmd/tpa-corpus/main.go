// Command tpa-corpus builds, verifies and validates the embedded TPA signature
// bundle (spec 101 US2). It is dev/CI tooling and is run from the repo root.
//
//	tpa-corpus build    [--src DIR] [--out FILE]
//	    compile the per-signature sources into the bundle. SOURCE_DATE_EPOCH, when
//	    set, overrides the manifest's pinned source_date_epoch.
//	tpa-corpus verify   [--src DIR] [--out FILE]
//	    rebuild in memory and byte-compare with FILE; exit 1 on drift.
//	tpa-corpus validate --bundle FILE --dataset FILE
//	    load FILE through the production loader, then check the corpus policy
//	    (provenance, licence, class vocabulary, eval pairs) against the dataset.
//
// Exit codes: 0 success, 1 failure or drift, 2 usage error.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/tpacorpus"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2

	defaultSrc = "internal/security/tpacorpus/signatures"
	defaultOut = "internal/security/scanner/bundled/scanner-bundle.json"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "build":
		return runBuild(args[1:], stdout, stderr, false)
	case "verify":
		return runBuild(args[1:], stdout, stderr, true)
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "tpa-corpus: unknown command %q\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage: tpa-corpus build    [--src DIR] [--out FILE]
       tpa-corpus verify   [--src DIR] [--out FILE]
       tpa-corpus validate --bundle FILE --dataset FILE`)
}

func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("tpa-corpus "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// parseFlags maps flag parsing onto exit codes; -h is a success.
func parseFlags(fs *flag.FlagSet, args []string) (int, bool) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK, false
		}
		return exitUsage, false
	}
	if fs.NArg() > 0 {
		// flag parsing stops at the first positional argument, silently
		// ignoring every flag after it; refuse rather than act on defaults.
		fmt.Fprintf(fs.Output(), "tpa-corpus: unexpected argument %q\n", fs.Arg(0))
		return exitUsage, false
	}
	return exitOK, true
}

func runBuild(args []string, stdout, stderr io.Writer, verify bool) int {
	name := "build"
	if verify {
		name = "verify"
	}
	fs := newFlags(name, stderr)
	src := fs.String("src", defaultSrc, "directory holding manifest.json and the TPA-YYYY-NNNN.json sources")
	out := fs.String("out", defaultOut, "bundle file to write (build) or compare against (verify)")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	var opts tpacorpus.Options
	if v := os.Getenv("SOURCE_DATE_EPOCH"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			fmt.Fprintf(stderr, "tpa-corpus: SOURCE_DATE_EPOCH %q is not an integer\n", v)
			return exitFailure
		}
		opts.SourceDateEpoch = &n
	}

	built, err := tpacorpus.Build(os.DirFS(*src), opts)
	if err != nil {
		fmt.Fprintf(stderr, "tpa-corpus: %s: %v\n", name, err)
		return exitFailure
	}

	if verify {
		have, err := os.ReadFile(*out)
		if err != nil {
			fmt.Fprintf(stderr, "tpa-corpus: drift: cannot read %s: %v\nrun: go run ./cmd/tpa-corpus build\n", *out, err)
			return exitFailure
		}
		if !bytes.Equal(have, built) {
			fmt.Fprintf(stderr, "tpa-corpus: drift: %s does not match a rebuild of %s (hand-edited, or sources changed without a rebuild)\nrun: go run ./cmd/tpa-corpus build\n", *out, *src)
			return exitFailure
		}
		fmt.Fprintf(stdout, "ok: %s is up to date with %s\n", *out, *src)
		return exitOK
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintf(stderr, "tpa-corpus: build: %v\n", err)
		return exitFailure
	}
	if err := os.WriteFile(*out, built, 0o644); err != nil { //nolint:gosec // a committed, world-readable corpus file
		fmt.Fprintf(stderr, "tpa-corpus: build: %v\n", err)
		return exitFailure
	}
	fmt.Fprintf(stdout, "wrote %s (%d bytes)\n", *out, len(built))
	return exitOK
}

func runValidate(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("validate", stderr)
	bundlePath := fs.String("bundle", "", "bundle file to validate (required)")
	datasetPath := fs.String("dataset", "", "eval dataset (detect_corpus_v1.json) to check the eval pairs against (required)")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if *bundlePath == "" || *datasetPath == "" {
		fmt.Fprintln(stderr, "tpa-corpus: validate needs both --bundle and --dataset")
		return exitUsage
	}

	bundle, err := os.ReadFile(*bundlePath)
	if err != nil {
		fmt.Fprintf(stderr, "tpa-corpus: validate: %v\n", err)
		return exitFailure
	}
	dataset, err := os.ReadFile(*datasetPath)
	if err != nil {
		fmt.Fprintf(stderr, "tpa-corpus: validate: %v\n", err)
		return exitFailure
	}

	_, info, err := scanner.LoadBundleCheck(bundle)
	if err != nil {
		fmt.Fprintf(stderr, "tpa-corpus: validate: production loader rejects %s: %v\n", *bundlePath, err)
		return exitFailure
	}
	if err := tpacorpus.ValidatePolicy(bundle, dataset); err != nil {
		fmt.Fprintf(stderr, "tpa-corpus: validate: %v\n", err)
		return exitFailure
	}
	fmt.Fprintf(stdout, "ok: %s v%s, %d signatures, %d runnable rules\n",
		*bundlePath, info.BundleVersion, info.SignatureCount, info.RunnableRules)
	return exitOK
}
