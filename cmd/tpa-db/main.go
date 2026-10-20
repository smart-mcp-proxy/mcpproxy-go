// Command tpa-db is the operator tool for Spec 101 signed TPA bundles.
//
// Subcommands:
//
//	keygen --out DIR
//	    write DIR/operator.key (0600, base64 of the 64-byte Ed25519 private
//	    key) and DIR/operator.pub (base64 of the 32-byte public key); print the
//	    public key and its key id. Never overwrites existing files.
//	sign --bundle PATH --key KEYFILE [--out PATH]
//	    write the detached sidecar <out>.sig for the bundle. Bundle bytes are
//	    never edited: the operator authors sequence, key_id, trust_epoch and
//	    generated_at in the bundle itself. Refuses a bundle whose manifest
//	    key_id differs from the signing key's id.
//	verify --bundle PATH --signer BASE64PUB
//	    verify <bundle>.sig against one trusted signer and the manifest key_id;
//	    print OK, or the contract rejection code with exit status 1.
//
// Exit status: 0 success, 1 failure, 2 usage error.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/bundlesig"
)

const (
	privKeyFile = "operator.key"
	pubKeyFile  = "operator.pub"
	// authority labels the single operator root inside the one-key trust set
	// that verify builds; this tool has no notion of a second root.
	authority = "operator"
	// maxBundleBytes mirrors the loader's FR-011 bundle ceiling
	// (scanner.MaxBundleBytes); a bundle the proxy would refuse is not worth
	// signing or verifying here either.
	maxBundleBytes = 8 << 20
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: tpa-db <command> [flags]

commands:
  keygen --out DIR
  sign   --bundle PATH --key KEYFILE [--out PATH]
  verify --bundle PATH --signer BASE64PUB
`)
}

// run is main without the process exit, so tests can drive it.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "keygen":
		return cmdKeygen(args[1:], stdout, stderr)
	case "sign":
		return cmdSign(args[1:], stdout, stderr)
	case "verify":
		return cmdVerify(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "tpa-db: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("tpa-db "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func base64Std(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func cmdKeygen(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("keygen", stderr)
	out := fs.String("out", "", "directory to write operator.key and operator.pub into")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *out == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "keygen: --out DIR is required")
		return 2
	}

	privPath, pubPath := filepath.Join(*out, privKeyFile), filepath.Join(*out, pubKeyFile)
	for _, p := range []string{privPath, pubPath} {
		if _, err := os.Lstat(p); err == nil {
			fmt.Fprintf(stderr, "keygen: %s already exists; refusing to overwrite\n", p)
			return 1
		}
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		fmt.Fprintf(stderr, "keygen: %v\n", err)
		return 1
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintf(stderr, "keygen: %v\n", err)
		return 1
	}
	if err := createExclusive(privPath, []byte(base64Std(priv)+"\n"), 0o600); err != nil {
		fmt.Fprintf(stderr, "keygen: %v\n", err)
		return 1
	}
	if err := createExclusive(pubPath, []byte(base64Std(pub)+"\n"), 0o644); err != nil {
		_ = os.Remove(privPath) // do not leave half a key pair behind
		fmt.Fprintf(stderr, "keygen: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "public_key: %s\nkey_id: %s\n", base64Std(pub), bundlesig.KeyID(pub))
	return 0
}

// createExclusive writes a new file and fails if it already exists, closing
// the race between the existence check and the write.
func createExclusive(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists; refusing to overwrite", path)
		}
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// loadPrivateKey reads a keygen-format key file and checks that its embedded
// public half really is the one derived from its seed.
func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := strings.TrimSpace(string(raw))
	b, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil || len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s is not a base64-encoded %d-byte Ed25519 private key", path, ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(b)
	if !priv.Equal(ed25519.NewKeyFromSeed(priv.Seed())) {
		return nil, fmt.Errorf("%s: public half does not match the private seed", path)
	}
	return priv, nil
}

// manifestKeyID extracts only the top-level "key_id" string of a bundle. It
// returns ok=false for a bundle that is not a JSON object or has no string
// key_id.
func manifestKeyID(bundle []byte) (string, bool) {
	var m struct {
		KeyID *string `json:"key_id"`
	}
	if err := json.Unmarshal(bundle, &m); err != nil || m.KeyID == nil {
		return "", false
	}
	return *m.KeyID, true
}

func cmdSign(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("sign", stderr)
	bundlePath := fs.String("bundle", "", "bundle file to sign")
	keyPath := fs.String("key", "", "operator private key file (from keygen)")
	outPath := fs.String("out", "", "write the bundle copy here and the sidecar to <out>.sig (default: in place)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *bundlePath == "" || *keyPath == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "sign: --bundle and --key are required")
		return 2
	}

	bundle, err := readBounded(*bundlePath, maxBundleBytes)
	if err != nil {
		fmt.Fprintf(stderr, "sign: %v\n", err)
		return 1
	}
	priv, err := loadPrivateKey(*keyPath)
	if err != nil {
		fmt.Fprintf(stderr, "sign: %v\n", err)
		return 1
	}

	// An artifact whose manifest names another key would be rejected by every
	// loader at contract row 13, so refuse to produce it.
	signerID := bundlesig.KeyID(priv.Public().(ed25519.PublicKey))
	got, ok := manifestKeyID(bundle)
	if !ok {
		fmt.Fprintf(stderr, "sign: %s: bundle is not a JSON object with a string \"key_id\"\n", *bundlePath)
		return 1
	}
	if got != signerID {
		fmt.Fprintf(stderr, "sign: %s: manifest key_id %q != signing key id %q\n", bundlesig.CodeKeyIDMismatch, got, signerID)
		return 1
	}

	dest := *bundlePath
	if *outPath != "" && *outPath != *bundlePath {
		dest = *outPath
		if err := writeReplace(dest, bundle, 0o644); err != nil {
			fmt.Fprintf(stderr, "sign: %v\n", err)
			return 1
		}
	}
	if err := writeReplace(dest+".sig", bundlesig.SignSidecar(priv, bundle), 0o644); err != nil {
		fmt.Fprintf(stderr, "sign: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "signed %s with key %s\nsidecar: %s.sig\n", dest, signerID, dest)
	return 0
}

func cmdVerify(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("verify", stderr)
	bundlePath := fs.String("bundle", "", "bundle file; its sidecar is <bundle>.sig")
	signer := fs.String("signer", "", "trusted signer: base64 of the 32-byte Ed25519 public key")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *bundlePath == "" || *signer == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "verify: --bundle and --signer are required")
		return 2
	}

	fail := func(err error) int {
		if code := bundlesig.CodeOf(err); code != "" {
			fmt.Fprintln(stdout, code)
		}
		fmt.Fprintf(stderr, "verify: %v\n", err)
		return 1
	}

	pub, err := bundlesig.ParsePublicKey(*signer)
	if err != nil {
		fmt.Fprintf(stderr, "verify: --signer: %v\n", err)
		return 2
	}
	// Sidecar first, through its own small cap, exactly as the loader orders
	// it (FR-008): it is the input verification depends on.
	raw, err := readBounded(*bundlePath+".sig", bundlesig.MaxSidecarBytes)
	if err != nil {
		if errors.Is(err, errTooLarge) {
			// Contract row 1: report the code, as for any other rejection.
			return fail(&bundlesig.Error{Code: bundlesig.CodeSidecarTooLarge, Detail: err.Error()})
		}
		fmt.Fprintf(stderr, "verify: %v\n", err)
		return 1
	}
	sc, err := bundlesig.ParseSidecar(raw)
	if err != nil {
		return fail(err)
	}
	bundle, err := readBounded(*bundlePath, maxBundleBytes)
	if err != nil {
		fmt.Fprintf(stderr, "verify: %v\n", err)
		return 1
	}
	ts, err := bundlesig.NewTrustSet(bundlesig.Key{ID: bundlesig.KeyID(pub), Public: pub, Authority: authority})
	if err != nil {
		fmt.Fprintf(stderr, "verify: %v\n", err)
		return 1
	}
	verified, err := ts.Verify(bundle, sc)
	if err != nil {
		return fail(err)
	}
	// An unparseable manifest is not a contract code; treat it as an empty
	// key id, which CheckKeyIDAgreement rejects as a mismatch.
	manifestID, _ := manifestKeyID(bundle)
	if err := bundlesig.CheckKeyIDAgreement(manifestID, sc, verified); err != nil {
		return fail(err)
	}
	fmt.Fprintln(stdout, "OK")
	return 0
}

// errTooLarge marks a readBounded refusal for size, so verify can still print
// the contract code for an oversized sidecar.
var errTooLarge = errors.New("over the size limit")

// beforeOpenHook is a test-only seam called between readBounded's lstat and
// its open, so a test can swap the path in that window.
var beforeOpenHook func(path string)

// readBounded reads a regular file of at most limit bytes. The bundle and its
// sidecar may sit in a directory someone else can write, so a symlink, FIFO or
// device is refused outright (a FIFO would block the read forever) and the
// read is capped rather than trusting the size. The open is checked against
// the pre-open lstat with os.SameFile so a swap between the two is refused.
func readBounded(path string, limit int64) ([]byte, error) {
	pre, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !pre.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if beforeOpenHook != nil {
		beforeOpenHook(path)
	}
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	post, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !post.Mode().IsRegular() || !os.SameFile(pre, post) {
		return nil, fmt.Errorf("%s is not a regular file (changed while opening)", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s exceeds the %d-byte limit: %w", path, limit, errTooLarge)
	}
	return data, nil
}

// writeReplace writes data to a fresh temporary file beside dest and renames
// it over dest. Renaming replaces the directory entry itself, so a symlink
// planted at dest is replaced rather than followed — writing through it would
// let whoever can write the bundle directory make the operator overwrite a
// file of the attacker's choosing (the private key, mcp_config.json).
func writeReplace(dest string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	// Set the mode on the DESCRIPTOR: a chmod by name would follow whatever
	// an attacker who can write this directory renamed into the temp name's
	// place (a symlink to the private key) — Sol 6.1 review, round 2.
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(name, dest); err != nil {
		cleanup()
		return err
	}
	return nil
}
