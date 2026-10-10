package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	reviewFetchDefaultWait = 30 * time.Second
	reviewFetchMaxWait     = 120 * time.Second
)

type reviewDoer interface {
	DoRaw(ctx context.Context, method, path string, body []byte) (*http.Response, error)
}

// reviewFetchResult is the -o json/yaml shape of `review fetch`.
type reviewFetchResult struct {
	Server      string `json:"server" yaml:"server"`
	Captured    bool   `json:"captured" yaml:"captured"`
	ToolCount   int    `json:"tool_count" yaml:"tool_count"`
	Quarantined bool   `json:"quarantined" yaml:"quarantined"`
	Error       string `json:"error,omitempty" yaml:"error,omitempty"`
}

func newReviewFetchCommand() *cobra.Command {
	var wait time.Duration
	fetch := &cobra.Command{
		Use:   "fetch <server>",
		Short: "Capture tool definitions for review (does not approve)",
		Long: `Ask the daemon to connect to a server and capture its tool definitions so
they can be reviewed with 'mcpproxy review show'. This is the CLI form of
"Fetch tool definitions" on the Web and macOS review screens.

It never approves, releases or enables the server: a quarantined server stays
quarantined and a disabled server is refused (enable it first). The command
exits nonzero when the server is unknown, disabled, unreachable, or returns no
tool definitions.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			// Failures are operational, not usage mistakes: keep the error readable.
			c.SilenceUsage = true
			return runReviewFetch(args[0], wait, ResolveOutputFormat())
		},
	}
	fetch.Flags().DurationVar(&wait, "wait", reviewFetchDefaultWait, "Maximum time to wait for the capture (max 2m)")
	return fetch
}

func reviewFetchDo(ctx context.Context, client reviewDoer, method, path string) (int, []byte, error) {
	resp, err := client.DoRaw(ctx, method, path, nil)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// reviewFetchState is what the review payload says about a capture.
type reviewFetchState struct {
	Quarantined bool
	// LiveTools counts stored records the upstream listed in its latest
	// capture; it is not the size of the stored-record inventory, which keeps
	// operator decisions for tools the upstream no longer lists.
	LiveTools     int
	LastCaptureAt string
}

// reviewFetchReview reads the review payload and returns its capture state.
func reviewFetchReview(ctx context.Context, client reviewDoer, server string) (reviewFetchState, error) {
	status, body, err := reviewFetchDo(ctx, client, http.MethodGet, "/api/v1/servers/"+url.PathEscape(server)+"/review")
	if err != nil {
		return reviewFetchState{}, err
	}
	if status == http.StatusNotFound {
		return reviewFetchState{}, cliRefusalError{fmt.Errorf("server '%s' not found; list servers with: mcpproxy upstream list", server)}
	}
	if status != http.StatusOK {
		return reviewFetchState{}, parseAPIError(body, status, "read review")
	}
	var env struct {
		Data struct {
			Server struct {
				Quarantined   bool   `json:"quarantined"`
				LiveToolCount int    `json:"live_tool_count"`
				LastCaptureAt string `json:"last_capture_at"`
			} `json:"server"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return reviewFetchState{}, err
	}
	srv := env.Data.Server
	return reviewFetchState{Quarantined: srv.Quarantined, LiveTools: srv.LiveToolCount, LastCaptureAt: srv.LastCaptureAt}, nil
}

// reviewFetchEnabled reads the server's enabled flag from the server list; the
// review payload does not carry it.
func reviewFetchEnabled(ctx context.Context, client reviewDoer, server string) (bool, error) {
	status, body, err := reviewFetchDo(ctx, client, http.MethodGet, "/api/v1/servers")
	if err != nil {
		return false, err
	}
	if status != http.StatusOK {
		return false, parseAPIError(body, status, "list servers")
	}
	var env struct {
		Data struct {
			Servers []struct {
				Name    string `json:"name"`
				Enabled bool   `json:"enabled"`
			} `json:"servers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return false, err
	}
	for _, s := range env.Data.Servers {
		if s.Name == server {
			return s.Enabled, nil
		}
	}
	return false, cliRefusalError{fmt.Errorf("server '%s' not found; list servers with: mcpproxy upstream list", server)}
}

// reviewFetchClient builds the daemon client under ctx. The probe inside
// newClient has its own short timeout and is not context-aware, so it runs in
// a goroutine and the caller stops waiting when the shared deadline passes.
func reviewFetchClient(ctx context.Context, newClient func() (reviewDoer, error)) (reviewDoer, error) {
	type built struct {
		client reviewDoer
		err    error
	}
	ch := make(chan built, 1)
	go func() {
		c, err := newClient()
		ch <- built{c, err}
	}()
	select {
	case b := <-ch:
		return b.client, b.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func runReviewFetch(server string, wait time.Duration, format string) error {
	return runReviewFetchWith(server, wait, format, func() (reviewDoer, error) {
		client, _, err := newSecurityCLIClient()
		if err != nil {
			return nil, err
		}
		return client, nil
	})
}

func runReviewFetchWith(server string, wait time.Duration, format string, newClient func() (reviewDoer, error)) error {
	if wait <= 0 || wait > reviewFetchMaxWait {
		return flagValidationError{fmt.Errorf("--wait must be between 1s and %s", reviewFetchMaxWait)}
	}
	// One deadline covers the whole command, including the daemon probe that
	// builds the client and every read after the POST.
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()

	result := reviewFetchResult{Server: server}
	fail := func(err error) error {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			var refusal cliRefusalError
			if !errors.As(err, &refusal) {
				err = cliRefusalError{fmt.Errorf("timed out after %s waiting for '%s'; retry with a larger --wait or check: mcpproxy upstream logs %s", wait, server, server)}
			}
		}
		if format != "table" {
			result.Error = err.Error()
			if printErr := formatReviewResult(format, result); printErr != nil {
				return printErr
			}
		}
		return err
	}

	client, err := reviewFetchClient(ctx, newClient)
	if err != nil {
		return fail(err)
	}

	before, err := reviewFetchReview(ctx, client, server)
	if err != nil {
		return fail(err)
	}
	result.Quarantined = before.Quarantined
	enabled, err := reviewFetchEnabled(ctx, client, server)
	if err != nil {
		return fail(err)
	}
	if !enabled {
		return fail(cliRefusalError{fmt.Errorf("server '%s' is disabled, so its tools cannot be fetched; enable it first: mcpproxy upstream enable %s (it stays quarantined until you approve it)", server, server)})
	}

	status, body, err := reviewFetchDo(ctx, client, http.MethodPost, "/api/v1/servers/"+url.PathEscape(server)+"/discover-tools")
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fail(cliRefusalError{fmt.Errorf("timed out after %s waiting for '%s'; retry with a larger --wait or check: mcpproxy upstream logs %s", wait, server, server)})
		}
		return fail(err)
	}
	if status != http.StatusOK {
		apiErr := parseAPIError(body, status, "fetch tool definitions")
		return fail(cliRefusalError{fmt.Errorf("%w; check connectivity with: mcpproxy upstream logs %s", apiErr, server)})
	}

	// A 200 is not proof: the daemon reports success for servers it skipped,
	// and a disable between the precheck and the POST must not read as success.
	if stillEnabled, err := reviewFetchEnabled(ctx, client, server); err != nil {
		return fail(err)
	} else if !stillEnabled {
		result.Captured = false
		return fail(cliRefusalError{fmt.Errorf("server '%s' was disabled while fetching, so nothing was captured; enable it first: mcpproxy upstream enable %s", server, server)})
	}
	// Re-read the review: the stored records are the proof of capture.
	after, err := reviewFetchReview(ctx, client, server)
	if err != nil {
		return fail(err)
	}
	result.Quarantined = after.Quarantined
	// Fresh-capture proof: the capture stamp must have advanced past the one
	// seen before the POST, and the upstream must have listed tools in it.
	// Retained approval records alone are never evidence of a capture.
	fresh := after.LastCaptureAt != "" && after.LastCaptureAt != before.LastCaptureAt
	result.Captured, result.ToolCount = fresh && after.LiveTools > 0, 0
	if result.Captured {
		result.ToolCount = after.LiveTools
	}
	if !result.Captured {
		return fail(cliRefusalError{fmt.Errorf("no tool definitions captured for '%s' (the server returned %d tools); check it with: mcpproxy upstream logs %s", server, after.LiveTools, server)})
	}
	return formatReviewResult(format, result)
}

func formatReviewResult(format string, result reviewFetchResult) error {
	switch format {
	case "json":
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(encoded))
	case "yaml":
		encoded, err := yaml.Marshal(result)
		if err != nil {
			return err
		}
		fmt.Print(string(encoded))
	default:
		state := "server remains quarantined; nothing was approved"
		if !result.Quarantined {
			state = "nothing was approved"
		}
		fmt.Printf("Captured %d tool definitions for %s (%s).\nReview them with: mcpproxy review show %s\n", result.ToolCount, result.Server, state, result.Server)
	}
	return nil
}
