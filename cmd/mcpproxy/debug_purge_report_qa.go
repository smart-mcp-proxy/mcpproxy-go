//go:build qa_hooks

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/toolreview"
)

// Spec 116 PR2 T058: a test-only inspection command for the live checks
// (quickstart L2). It is compiled only with the qa_hooks build tag, so it is
// never part of a released binary.
//
//	mcpproxy debug purge-report --data-dir <dir> <server>
//
// prints, per purge-set bucket, how many keys of <server> are stored in
// <dir>/config.db (data-model §PurgeSet), plus the server's row stamp. The
// daemon must be stopped: bbolt's file lock refuses a second opener.

func registerQADebugCommands(root *cobra.Command) {
	var dataDir string
	debug := &cobra.Command{Use: "debug", Short: "QA-only inspection commands (qa_hooks build)", Hidden: true}
	report := &cobra.Command{
		Use:   "purge-report <server>",
		Short: "Count the purge-set keys a server still has in config.db",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if dataDir == "" {
				return fmt.Errorf("--data-dir is required")
			}
			mgr, err := storage.NewManager(dataDir, zap.NewNop().Sugar())
			if err != nil {
				return fmt.Errorf("open %s/config.db (is the daemon still running?): %w", dataDir, err)
			}
			defer mgr.Close()
			name := args[0]
			counts, err := mgr.PurgeSetCounts(name)
			if err != nil {
				return err
			}
			out := map[string]any{"server": name, "counts": counts}
			if row, err := mgr.GetStoredServer(name); err == nil {
				out["row"] = map[string]int64{"generation": row.Stamp.Gen, "incarnation": row.Stamp.Incarnation}
			}
			total := 0
			keys := make([]string, 0, len(counts))
			for k, n := range counts {
				keys = append(keys, k)
				total += n
			}
			sort.Strings(keys)
			out["buckets"] = keys
			out["total"] = total
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		},
	}
	report.Flags().StringVar(&dataDir, "data-dir", "", "mcpproxy data directory holding config.db")
	debug.AddCommand(report)

	var seedDir, oauthKeySuffix string
	seed := &cobra.Command{
		Use:   "seed-purge-set <server>",
		Short: "Write one record of every purge-set kind for a server (live-check fixture)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if seedDir == "" {
				return fmt.Errorf("--data-dir is required")
			}
			mgr, err := storage.NewManager(seedDir, zap.NewNop().Sugar())
			if err != nil {
				return fmt.Errorf("open %s/config.db (is the daemon still running?): %w", seedDir, err)
			}
			defer mgr.Close()
			return seedPurgeSet(mgr, args[0], oauthKeySuffix)
		},
	}
	seed.Flags().StringVar(&seedDir, "data-dir", "", "mcpproxy data directory holding config.db")
	seed.Flags().StringVar(&oauthKeySuffix, "oauth-key-suffix", "0123456789abcdef", "16 lowercase hex characters of the token key (name_<suffix>)")
	debug.AddCommand(seed)
	root.AddCommand(debug)
}

// seedPurgeSet writes the quickstart §T2 fixture for name: approved and
// changed prompt approvals, an OAuth token with a DCR client under
// name_<suffix>, an OAuth completion event, an integrity baseline, a scan job
// and report, and a tool hash. Tool approvals and call history come from the
// live daemon itself.
func seedPurgeSet(mgr *storage.Manager, name, suffix string) error {
	now := time.Now().UTC()
	steps := []func() error{
		func() error {
			return mgr.MarkToolBaselineDecided(name, toolreview.BaselineDecision{Source: toolreview.BaselineSourceServerApproval, By: "qa-seed"})
		},
		func() error {
			return mgr.SavePromptApproval(&storage.PromptApprovalRecord{ServerName: name, PromptName: "greet", Status: storage.ToolApprovalStatusApproved,
				ApprovedHash: "seed", CurrentHash: "seed", ApprovedAt: now, ApprovedBy: "qa-seed"})
		},
		func() error {
			return mgr.SavePromptApproval(&storage.PromptApprovalRecord{ServerName: name, PromptName: "drift", Status: storage.ToolApprovalStatusChanged,
				ApprovedHash: "seed", CurrentHash: "seed2", ApprovedAt: now, ApprovedBy: "qa-seed"})
		},
		func() error {
			return mgr.GetBoltDB().SaveOAuthToken(&storage.OAuthTokenRecord{ServerName: name + "_" + suffix, DisplayName: name,
				AccessToken: "qa-at-" + name, RefreshToken: "qa-rt-" + name, TokenType: "Bearer",
				ClientID: "qa-client-" + name, ClientSecret: "qa-secret-" + name, Created: now, Updated: now})
		},
		func() error {
			return mgr.GetBoltDB().SaveOAuthCompletionEvent(&storage.OAuthCompletionEvent{ServerName: name, CompletedAt: now})
		},
		func() error {
			return mgr.SaveIntegrityBaseline(&scanner.IntegrityBaseline{ServerName: name, ApprovedAt: now, ApprovedBy: "qa-seed"})
		},
		func() error {
			return mgr.SaveScanJob(&scanner.ScanJob{ID: "scan-" + name + "-qa", ServerName: name, Status: scanner.ScanJobStatusCompleted,
				ScanPass: scanner.ScanPassSecurityScan, StartedAt: now, CompletedAt: now, Scanners: []string{"tpa-descriptions"}})
		},
		func() error {
			return mgr.SaveScanReport(&scanner.ScanReport{ID: "report-" + name + "-qa", JobID: "scan-" + name + "-qa", ServerName: name,
				ScannerID: "tpa-descriptions", ScannedAt: now})
		},
		func() error { return mgr.SaveToolHash(name+":qa_tool", "seed") },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			return fmt.Errorf("seed step %d for %s: %w", i, name, err)
		}
	}
	fmt.Printf("seeded the purge set of %s\n", name)
	return nil
}
