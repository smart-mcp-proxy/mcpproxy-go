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
	Server      string `json:"server"`
	Captured    bool   `json:"captured"`
	ToolCount   int    `json:"tool_count"`
	Quarantined bool   `json:"quarantined"`
	Error       string `json:"error,omitempty"`
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
		RunE: func(_ *cobra.Command, args []string) error {
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

// reviewFetchReview reads the review payload and returns its capture state.
func reviewFetchReview(ctx context.Context, client reviewDoer, server string) (quarantined, captured bool, tools int, err error) {
	status, body, err := reviewFetchDo(ctx, client, http.MethodGet, "/api/v1/servers/"+url.PathEscape(server)+"/review")
	if err != nil {
		return false, false, 0, err
	}
	if status == http.StatusNotFound {
		return false, false, 0, cliRefusalError{fmt.Errorf("server '%s' not found; list servers with: mcpproxy upstream list", server)}
	}
	if status != http.StatusOK {
		return false, false, 0, parseAPIError(body, status, "read review")
	}
	var env struct {
		Data struct {
			Server struct {
				Quarantined         bool `json:"quarantined"`
				DefinitionsCaptured bool `json:"definitions_captured"`
			} `json:"server"`
			Tools []json.RawMessage `json:"tools"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return false, false, 0, err
	}
	return env.Data.Server.Quarantined, env.Data.Server.DefinitionsCaptured, len(env.Data.Tools), nil
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

func runReviewFetch(server string, wait time.Duration, format string) error {
	if wait <= 0 || wait > reviewFetchMaxWait {
		return flagValidationError{fmt.Errorf("--wait must be between 1s and %s", reviewFetchMaxWait)}
	}
	client, _, err := newSecurityCLIClient()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()

	result := reviewFetchResult{Server: server}
	fail := func(err error) error {
		if format != "table" {
			result.Error = err.Error()
			if printErr := formatReviewResult(format, result); printErr != nil {
				return printErr
			}
		}
		return err
	}

	var captured bool
	var tools int
	if result.Quarantined, captured, tools, err = reviewFetchReview(ctx, client, server); err != nil {
		return fail(err)
	}
	result.Captured, result.ToolCount = captured && tools > 0, tools
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

	// A 200 is not proof: the daemon reports success for servers it skipped.
	if result.Quarantined, captured, tools, err = reviewFetchReview(ctx, client, server); err != nil {
		return fail(err)
	}
	result.Captured, result.ToolCount = captured && tools > 0, tools
	if !result.Captured {
		return fail(cliRefusalError{fmt.Errorf("no tool definitions captured for '%s' (the server returned %d tools); check it with: mcpproxy upstream logs %s", server, tools, server)})
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
		fmt.Printf("server: %s\ncaptured: %t\ntool_count: %d\nquarantined: %t\n", result.Server, result.Captured, result.ToolCount, result.Quarantined)
		if result.Error != "" {
			fmt.Printf("error: %q\n", result.Error)
		}
	default:
		state := "server remains quarantined; nothing was approved"
		if !result.Quarantined {
			state = "nothing was approved"
		}
		fmt.Printf("Captured %d tool definitions for %s (%s).\nReview them with: mcpproxy review show %s\n", result.ToolCount, result.Server, state, result.Server)
	}
	return nil
}
