package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/logs"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/management"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 123 PR-3 (FR-032): `mcpproxy auth tokens status|encrypt|decrypt`.
//
// These are offline commands over the data directory. They open config.db
// through the key-aware helper (openStorageForCLIWithKey), so a running core,
// which holds the database lock, makes them fail with exit 3 and change
// nothing, and the key comes from the same resolution the daemon uses (the
// environment override is the offline form for a key that exists only inside
// a service). Nothing they print contains a token value, key bytes, the
// contents of a key source or the key reference: rows appear by server name,
// state, reason and public key id only.

var (
	authTokensDryRun             bool
	authTokensYes                bool
	authTokensLeaveUndecryptable bool

	authTokensCmd = &cobra.Command{
		Use:   "tokens",
		Short: "Inspect, encrypt or decrypt the stored OAuth tokens (offline)",
		Long: `Offline commands for the upstream OAuth tokens stored in config.db when
oauth_token_encryption_key is configured (see docs/features/oauth-token-encryption.md).

They open the database directly, so mcpproxy must be stopped: with a running
core they fail with exit code 3 and change nothing. The key is resolved exactly
as the daemon does; set MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE to supply a key
that only exists inside the service (a systemd credential, a container secret).`,
	}

	authTokensStatusCmd = &cobra.Command{
		Use:   "status",
		Short: "Show how the stored OAuth tokens are protected",
		Long: `Print the OAuth token storage section of 'mcpproxy doctor' from the database:
whether encryption is on, the public key id, how many rows are sealed, plaintext
or undecryptable (and why), whether compaction is pending, and the state of every
server's row. Token values are never printed.`,
		Example: `  mcpproxy auth tokens status
  mcpproxy auth tokens status -o json`,
		RunE: runAuthTokensStatus,
	}

	authTokensEncryptCmd = &cobra.Command{
		Use:   "encrypt",
		Short: "Seal every plaintext OAuth token row under the configured key",
		Long: `Seal every plaintext OAuth token row under the configured key in one
transaction, exactly as the startup sweep does. Rows that are already sealed,
undecryptable rows and rows that are not valid records are left alone. Running
it again changes nothing. Exits 1 when a row could not be sealed or remains
undecryptable.

Finish with 'mcpproxy db compact --keep-backup=false': sealing frees the pages
that held the old plaintext but does not erase them.`,
		Example: `  mcpproxy auth tokens encrypt --dry-run
  mcpproxy auth tokens encrypt`,
		RunE: runAuthTokensEncrypt,
	}

	authTokensDecryptCmd = &cobra.Command{
		Use:   "decrypt",
		Short: "Rewrite every sealed OAuth token row as plaintext (downgrade, disable, rotate)",
		Long: `Rewrite every row the configured key opens as plaintext JSON in one
transaction. Use it before downgrading to a release without encryption, before
removing the key, and as the first step of a key rotation:

  1. mcpproxy auth tokens decrypt --yes          (with the OLD key)
  2. change the key
  3. mcpproxy auth tokens encrypt                (with the NEW key)
  4. mcpproxy db compact --keep-backup=false     and delete any config.db.bak

The tokens are readable in config.db until step 3: copies of the file or of the
data directory made in between hold them in plaintext. Requires --yes. Refuses,
and writes nothing, when a row cannot be opened, unless --leave-undecryptable
says to leave those rows as they are (the command still exits 1).`,
		Example: `  mcpproxy auth tokens decrypt --yes
  MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE=/etc/credstore/oauth-key mcpproxy auth tokens decrypt --yes`,
		RunE: runAuthTokensDecrypt,
	}
)

func init() {
	authCmd.AddCommand(authTokensCmd)
	authTokensCmd.AddCommand(authTokensStatusCmd, authTokensEncryptCmd, authTokensDecryptCmd)

	for _, c := range []*cobra.Command{authTokensStatusCmd, authTokensEncryptCmd, authTokensDecryptCmd} {
		c.Flags().StringVarP(&authLogLevel, "log-level", "l", "warn", "Log level (trace, debug, info, warn, error)")
		addConfigFlag(c.Flags(), &authConfigPath, "Path to MCP configuration file (default: ~/.mcpproxy/mcp_config.json)")
	}
	authTokensEncryptCmd.Flags().BoolVar(&authTokensDryRun, "dry-run", false, "Report what would be sealed; change no OAuth row")
	authTokensDecryptCmd.Flags().BoolVar(&authTokensYes, "yes", false, "Confirm that the tokens are to be stored as plaintext")
	authTokensDecryptCmd.Flags().BoolVar(&authTokensLeaveUndecryptable, "leave-undecryptable", false,
		"Rewrite the rows the key opens and leave rows it cannot open as they are")
}

// ---- output model ----

// tokenRowState is one stored row as `auth tokens` reports it.
type tokenRowState struct {
	Server    string `json:"server"`
	BucketKey string `json:"bucket_key"`
	State     string `json:"state"` // sealed | plaintext | undecryptable
	Reason    string `json:"reason,omitempty"`
	RowKeyID  string `json:"row_key_id,omitempty"`
}

type tokensStatusOutput struct {
	OAuthTokenStorage *contracts.OAuthTokenStorageReport `json:"oauth_token_storage"`
	Rows              []tokenRowState                    `json:"rows"`
	NextSteps         []string                           `json:"next_steps"`
}

type tokensCounts struct {
	Plaintext     int `json:"plaintext"`
	Sealed        int `json:"sealed"`
	Undecryptable int `json:"undecryptable"`
}

type tokensEncryptOutput struct {
	Action            string          `json:"action"`
	DryRun            bool            `json:"dry_run"`
	KeyID             string          `json:"key_id"`
	Sealed            int             `json:"sealed"`
	Skipped           []tokenRowState `json:"skipped"`
	Undecryptable     []tokenRowState `json:"undecryptable"`
	After             tokensCounts    `json:"after"`
	CompactionAdvised bool            `json:"compaction_advised"`
	NextSteps         []string        `json:"next_steps"`
}

type tokensDecryptOutput struct {
	Action        string          `json:"action"`
	KeyID         string          `json:"key_id"`
	Decrypted     int             `json:"decrypted"`
	Undecryptable []tokenRowState `json:"undecryptable"`
	After         tokensCounts    `json:"after"`
	NextSteps     []string        `json:"next_steps"`
}

// ---- command entry points ----

func runAuthTokensStatus(cmd *cobra.Command, _ []string) error {
	cmd.SilenceUsage = true
	cfg, err := loadAuthConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}
	return authTokensStatus(cmd.Context(), cfg, os.Stdout, ResolveOutputFormat())
}

func runAuthTokensEncrypt(cmd *cobra.Command, _ []string) error {
	cmd.SilenceUsage = true
	cfg, err := loadAuthConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}
	return authTokensEncrypt(cmd.Context(), cfg, os.Stdout, ResolveOutputFormat(), authTokensDryRun)
}

func runAuthTokensDecrypt(cmd *cobra.Command, _ []string) error {
	cmd.SilenceUsage = true
	cfg, err := loadAuthConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}
	return authTokensDecrypt(cmd.Context(), cfg, os.Stdout, ResolveOutputFormat(), authTokensYes, authTokensLeaveUndecryptable)
}

// ---- implementations (testable: config, writer and format are parameters) ----

func authTokensLogger() (*zap.SugaredLogger, func(), error) {
	logger, err := logs.SetupCommandLogger(false, authLogLevel, false, "")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create logger: %w", err)
	}
	return logger.Sugar(), func() { _ = logger.Sync() }, nil
}

// openTokensStorage opens config.db for an `auth tokens` command. The
// database file must already exist: these commands never create one.
func openTokensStorage(cfg *config.Config, requireKey bool) (*storage.Manager, func(), string, config.KeySource, error) {
	logger, syncLog, err := authTokensLogger()
	if err != nil {
		return nil, nil, "", config.KeySource{}, err
	}
	// Resolve (and fail on) the key before looking at the file, so a bad or
	// missing key is exit 4 whether or not a database exists yet.
	resolved, _, kerr := config.ResolveOAuthTokenKey(cfg)
	if kerr == nil && resolved == nil && requireKey {
		kerr = errNoOAuthTokenKey()
	}
	if kerr != nil {
		syncLog()
		return nil, nil, "", config.KeySource{}, kerr
	}
	if _, statErr := os.Stat(filepath.Join(cfg.DataDir, dbFileName)); statErr != nil {
		syncLog()
		if os.IsNotExist(statErr) {
			return nil, nil, "", config.KeySource{}, fmt.Errorf("no %s in the data directory %s: nothing to inspect", dbFileName, cfg.DataDir)
		}
		return nil, nil, "", config.KeySource{}, fmt.Errorf("cannot read %s in %s: %w", dbFileName, cfg.DataDir, statErr)
	}
	mgr, key, src, err := openStorageForCLIWithKey(cfg, logger, requireKey)
	if err != nil {
		syncLog()
		var lockErr *storage.DatabaseLockedError
		if errors.As(err, &lockErr) {
			fmt.Fprintln(os.Stderr, lockedRemediation)
		}
		return nil, nil, "", config.KeySource{}, err
	}
	return mgr, func() { _ = mgr.Close(); syncLog() }, key.ID(), src, nil
}

func authTokensStatus(_ context.Context, cfg *config.Config, w io.Writer, format string) error {
	mgr, closeFn, keyID, src, err := openTokensStorage(cfg, false)
	if err != nil {
		return err
	}
	defer closeFn()

	rows, err := mgr.ListOAuthTokenRows()
	if err != nil {
		return fmt.Errorf("failed to list OAuth token rows: %w", err)
	}
	advised, err := mgr.OAuthCompactAdvised()
	if err != nil {
		return fmt.Errorf("failed to read the compaction marker: %w", err)
	}
	report := management.BuildOAuthTokenStorageReport(management.OAuthTokenStorageState{
		KeyID:          keyID,
		KeySource:      src,
		Rows:           rows,
		CompactAdvised: advised,
		BackupPresent:  backupFilePresent(cfg.DataDir),
	}, nil, true)

	out := tokensStatusOutput{OAuthTokenStorage: report, Rows: tokenRowStates(rows), NextSteps: statusNextSteps(report)}
	return writeTokensOutput(w, format, out, func() { renderTokensStatusText(w, out) })
}

func authTokensEncrypt(ctx context.Context, cfg *config.Config, w io.Writer, format string, dryRun bool) error {
	mgr, closeFn, keyID, _, err := openTokensStorage(cfg, true)
	if err != nil {
		return err
	}
	defer closeFn()

	rep, err := mgr.SealOAuthTokens(ctx, dryRun)
	if err != nil {
		return fmt.Errorf("failed to seal OAuth token rows: %w", err)
	}
	rows, err := mgr.ListOAuthTokenRows()
	if err != nil {
		return fmt.Errorf("failed to list OAuth token rows: %w", err)
	}
	advised, err := mgr.OAuthCompactAdvised()
	if err != nil {
		return fmt.Errorf("failed to read the compaction marker: %w", err)
	}

	skipped := map[string]string{}
	out := tokensEncryptOutput{Action: "encrypt", DryRun: dryRun, KeyID: keyID, Sealed: rep.Sealed,
		Skipped: []tokenRowState{}, Undecryptable: []tokenRowState{}, CompactionAdvised: advised}
	for _, s := range rep.Skipped {
		skipped[s.BucketKey] = s.Reason
		out.Skipped = append(out.Skipped, tokenRowState{
			Server: management.OAuthRowServer(s.BucketKey), BucketKey: s.BucketKey, State: string(storage.OAuthRowPlaintext), Reason: s.Reason,
		})
	}
	out.After = countRows(rows)
	for _, st := range tokenRowStates(rows) {
		if st.State == string(storage.OAuthRowUndecryptable) {
			if _, dup := skipped[st.BucketKey]; !dup {
				out.Undecryptable = append(out.Undecryptable, st)
			}
		}
	}
	if dryRun {
		// a dry run wrote nothing: what remains is what a real run leaves
		out.After.Plaintext -= rep.Sealed
		out.After.Sealed += rep.Sealed
	}
	out.NextSteps = encryptNextSteps(out)

	if err := writeTokensOutput(w, format, out, func() { renderTokensEncryptText(w, out) }); err != nil {
		return err
	}
	if len(out.Skipped) > 0 || len(out.Undecryptable) > 0 {
		return cliRefusalError{fmt.Errorf("%d row(s) could not be sealed and %d remain undecryptable; they were left as they are",
			len(out.Skipped), len(out.Undecryptable))}
	}
	return nil
}

func authTokensDecrypt(ctx context.Context, cfg *config.Config, w io.Writer, format string, yes, leaveUndecryptable bool) error {
	if !yes {
		return newFlagValidationError("decrypt stores the OAuth tokens as plaintext in config.db: pass --yes to confirm (see 'mcpproxy auth tokens decrypt --help')")
	}
	mgr, closeFn, keyID, _, err := openTokensStorage(cfg, true)
	if err != nil {
		return err
	}
	defer closeFn()

	before, err := mgr.ListOAuthTokenRows()
	if err != nil {
		return fmt.Errorf("failed to list OAuth token rows: %w", err)
	}
	if !leaveUndecryptable {
		var bad []string
		for _, st := range tokenRowStates(before) {
			if st.State == string(storage.OAuthRowUndecryptable) {
				bad = append(bad, fmt.Sprintf("%s (%s)", st.Server, st.Reason))
			}
		}
		if len(bad) > 0 {
			sort.Strings(bad)
			return cliRefusalError{fmt.Errorf("%d row(s) cannot be opened with the configured key and nothing was changed: %s. "+
				"Restore the right key, discard a row with 'mcpproxy auth logout --server=<name>', "+
				"or pass --leave-undecryptable to rewrite the other rows and leave these as they are",
				len(bad), strings.Join(bad, ", "))}
		}
	}

	rep, err := mgr.DecryptOAuthTokens(ctx)
	if err != nil {
		return fmt.Errorf("failed to decrypt OAuth token rows: %w", err)
	}
	rows, err := mgr.ListOAuthTokenRows()
	if err != nil {
		return fmt.Errorf("failed to list OAuth token rows: %w", err)
	}
	out := tokensDecryptOutput{Action: "decrypt", KeyID: keyID, Decrypted: rep.Decrypted, Undecryptable: []tokenRowState{}, After: countRows(rows)}
	for _, st := range tokenRowStates(rows) {
		if st.State == string(storage.OAuthRowUndecryptable) {
			out.Undecryptable = append(out.Undecryptable, st)
		}
	}
	out.NextSteps = decryptNextSteps()

	if err := writeTokensOutput(w, format, out, func() { renderTokensDecryptText(w, out) }); err != nil {
		return err
	}
	if len(out.Undecryptable) > 0 {
		return cliRefusalError{fmt.Errorf("%d row(s) could not be opened and were left as they are", len(out.Undecryptable))}
	}
	return nil
}

// ---- helpers ----

func backupFilePresent(dataDir string) bool {
	if dataDir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dataDir, dbFileName+".bak"))
	return err == nil
}

func tokenRowStates(rows []storage.OAuthTokenRow) []tokenRowState {
	out := make([]tokenRowState, 0, len(rows))
	for _, r := range rows {
		st := tokenRowState{
			Server:    management.OAuthRowServer(r.BucketKey),
			BucketKey: r.BucketKey,
			State:     string(r.Class),
			RowKeyID:  r.RowKID,
		}
		if r.Class == storage.OAuthRowUndecryptable {
			st.Reason = string(r.Reason)
			if st.Reason == "" {
				st.Reason = "malformed"
			}
		} else {
			st.RowKeyID = "" // only an undecryptable row's id is of interest
		}
		out = append(out, st)
	}
	return out
}

func countRows(rows []storage.OAuthTokenRow) tokensCounts {
	var c tokensCounts
	for _, r := range rows {
		switch r.Class {
		case storage.OAuthRowPlaintext:
			c.Plaintext++
		case storage.OAuthRowSealed:
			c.Sealed++
		default:
			c.Undecryptable++
		}
	}
	return c
}

// writeTokensOutput renders data as JSON or YAML, or calls text for the
// human format. YAML goes through a JSON round trip so its keys match the
// JSON ones (yaml.v3 would otherwise lower-case the Go field names).
func writeTokensOutput(w io.Writer, format string, data interface{}, text func()) error {
	switch strings.ToLower(format) {
	case "json", "yaml":
		formatter, err := clioutput.NewFormatter(format)
		if err != nil {
			return err
		}
		generic := data
		if strings.EqualFold(format, "yaml") {
			b, err := json.Marshal(data)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(b, &generic); err != nil {
				return err
			}
		}
		s, err := formatter.Format(generic)
		if err != nil {
			return fmt.Errorf("failed to format output: %w", err)
		}
		fmt.Fprintln(w, strings.TrimRight(s, "\n"))
	default:
		text()
	}
	return nil
}

// oauthStorageWarningText explains a warning code of the OAuth token storage
// section (shared by doctor and `auth tokens status`).
func oauthStorageWarningText(code string) string {
	switch code {
	case management.OAuthStorageWarnLiteralKey:
		return "the key is written in the configuration file, so a copy of the data directory includes it; prefer a ${file:}, ${env:} or ${keyring:} reference"
	case management.OAuthStorageWarnKeyFileInDataDir:
		return "the key file lies inside the data directory, so a copy of the data directory includes it; move it to another volume"
	case management.OAuthStorageWarnPlaintextWithKey:
		return "some rows are still plaintext although a key is configured (a restored backup, or a write by an older binary); they are sealed at the next start, or run: mcpproxy auth tokens encrypt"
	case management.OAuthStorageWarnCompaction:
		return "freed pages may still hold the old plaintext; with mcpproxy stopped, run: mcpproxy db compact --keep-backup=false"
	case management.OAuthStorageWarnBackupPresent:
		return "config.db.bak exists in the data directory and may hold the tokens as plaintext; delete it once mcpproxy starts cleanly"
	}
	return code
}

func statusNextSteps(rep *contracts.OAuthTokenStorageReport) []string {
	steps := []string{}
	if rep.Undecryptable > 0 {
		steps = append(steps, "Undecryptable rows: restore the key they were sealed with, or discard a row with 'mcpproxy auth logout --server=<name>' and sign in again.")
	}
	if rep.EncryptionEnabled && rep.Plaintext > 0 {
		steps = append(steps, "Plaintext rows with a key configured: run 'mcpproxy auth tokens encrypt' (mcpproxy stopped).")
	}
	if rep.CompactionAdvised {
		steps = append(steps, "Compaction pending: with mcpproxy stopped, run 'mcpproxy db compact --keep-backup=false', and delete any config.db.bak.")
	}
	return steps
}

func encryptNextSteps(out tokensEncryptOutput) []string {
	steps := []string{}
	if out.DryRun {
		steps = append(steps, "Dry run: no row was changed. Run without --dry-run to seal these rows.")
		return steps
	}
	if out.Sealed > 0 || out.CompactionAdvised {
		steps = append(steps, "Run 'mcpproxy db compact --keep-backup=false' (mcpproxy stopped) so freed pages no longer hold the old plaintext, and delete any config.db.bak.")
	}
	if len(out.Skipped) > 0 {
		steps = append(steps, "Rows listed under skipped were left as plaintext: a row that is not a valid record, or over 1 MiB, is not sealed. Discard it with 'mcpproxy auth logout --server=<name>'.")
	}
	if len(out.Undecryptable) > 0 {
		steps = append(steps, "Undecryptable rows were left as they are: restore the key they were sealed with, or discard a row with 'mcpproxy auth logout --server=<name>'.")
	}
	return steps
}

func decryptNextSteps() []string {
	return []string{
		"The tokens are now readable JSON in config.db; copies of config.db or of the data directory made now hold them in plaintext.",
		"To disable or downgrade: remove oauth_token_encryption_key from the configuration (with a key set, startup seals the rows again).",
		"To rotate the key: change the key, run 'mcpproxy auth tokens encrypt', then run 'mcpproxy db compact --keep-backup=false' and delete any config.db.bak.",
	}
}

// ---- text rendering ----

// renderOAuthTokenStorage writes the OAuth token storage section the way
// doctor and `auth tokens status` show it. It prints nothing key-shaped.
func renderOAuthTokenStorage(w io.Writer, rep *contracts.OAuthTokenStorageReport, indent string) {
	if rep.EncryptionEnabled {
		line := "on"
		if rep.KeyID != "" {
			line += " (key id " + rep.KeyID
			if rep.KeySourceKind != "" {
				line += ", source: " + rep.KeySourceKind
			}
			line += ")"
		}
		fmt.Fprintf(w, "%sEncryption: %s\n", indent, line)
	} else {
		fmt.Fprintf(w, "%sEncryption: off\n", indent)
	}
	fmt.Fprintf(w, "%sRows:       sealed %d, plaintext %d, undecryptable %d\n", indent, rep.Sealed, rep.Plaintext, rep.Undecryptable)
	if rep.Undecryptable > 0 {
		r := rep.UndecryptableByReason
		var parts []string
		add := func(n int, name string) {
			if n > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", name, n))
			}
		}
		add(r.NoKey, "no_key")
		add(r.KeyMismatch, "key_mismatch")
		add(r.AuthFailed, "auth_failed")
		add(r.UnsupportedVersion, "unsupported_version")
		add(r.Malformed, "malformed")
		fmt.Fprintf(w, "%s            undecryptable by reason: %s\n", indent, strings.Join(parts, ", "))
		for _, s := range rep.Servers {
			line := fmt.Sprintf("%s  - %s: %s", indent, s.Server, s.Reason)
			if s.RowKeyID != "" {
				line += " (row key id " + s.RowKeyID + ")"
			}
			fmt.Fprintln(w, line)
		}
	}
	for _, code := range rep.Warnings {
		fmt.Fprintf(w, "%sWarning:    %s\n", indent, oauthStorageWarningText(code))
	}
}

// displayOAuthTokenStorage prints the OAuth token storage section of the
// doctor diagnostics payload (Spec 123 FR-030). It stays silent when the
// daemon did not report the section (an older daemon) or when nothing is
// configured and nothing is sealed: with no key, doctor's output is unchanged.
func displayOAuthTokenStorage(diag map[string]interface{}) {
	raw, ok := diag["oauth_token_storage"]
	if !ok || raw == nil {
		return
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return
	}
	var rep contracts.OAuthTokenStorageReport
	if json.Unmarshal(b, &rep) != nil {
		return
	}
	if !rep.EncryptionEnabled && rep.Sealed == 0 && rep.Undecryptable == 0 && !rep.CompactionAdvised {
		return
	}
	fmt.Println("🔑 OAuth Token Storage")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	renderOAuthTokenStorage(os.Stdout, &rep, "  ")
	for _, step := range statusNextSteps(&rep) {
		fmt.Println("  Next:      " + step)
	}
	fmt.Println()
}

func renderTokensStatusText(w io.Writer, out tokensStatusOutput) {
	fmt.Fprintln(w, "OAuth token storage")
	renderOAuthTokenStorage(w, out.OAuthTokenStorage, "  ")
	if len(out.Rows) > 0 {
		fmt.Fprintln(w, "  Servers:")
		for _, r := range out.Rows {
			line := fmt.Sprintf("    %-24s %s", r.Server, r.State)
			if r.Reason != "" {
				line += " (" + r.Reason
				if r.RowKeyID != "" {
					line += ", row key id " + r.RowKeyID
				}
				line += ")"
			}
			fmt.Fprintln(w, line)
		}
	}
	for _, s := range out.NextSteps {
		fmt.Fprintln(w, "  Next: "+s)
	}
}

func renderIssueRows(w io.Writer, title string, rows []tokenRowState) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(w, "  %s:\n", title)
	for _, r := range rows {
		fmt.Fprintf(w, "    %s (%s)\n", r.Server, r.Reason)
	}
}

func renderTokensEncryptText(w io.Writer, out tokensEncryptOutput) {
	verb := "Sealed"
	if out.DryRun {
		verb = "Would seal"
	}
	fmt.Fprintf(w, "%s %d plaintext OAuth token row(s) under key id %s.\n", verb, out.Sealed, out.KeyID)
	fmt.Fprintf(w, "  Rows after: sealed %d, plaintext %d, undecryptable %d\n", out.After.Sealed, out.After.Plaintext, out.After.Undecryptable)
	renderIssueRows(w, "Skipped (left as plaintext)", out.Skipped)
	renderIssueRows(w, "Undecryptable (left as they are)", out.Undecryptable)
	for _, s := range out.NextSteps {
		fmt.Fprintln(w, "  Next: "+s)
	}
}

func renderTokensDecryptText(w io.Writer, out tokensDecryptOutput) {
	fmt.Fprintf(w, "Rewrote %d OAuth token row(s) as plaintext.\n", out.Decrypted)
	fmt.Fprintf(w, "  Rows after: sealed %d, plaintext %d, undecryptable %d\n", out.After.Sealed, out.After.Plaintext, out.After.Undecryptable)
	renderIssueRows(w, "Left as they are (could not be opened)", out.Undecryptable)
	for _, s := range out.NextSteps {
		fmt.Fprintln(w, "  Note: "+s)
	}
}
