package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cliclient"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestCallToolJSONFailureIsParseableAndNonzero(t *testing.T) {
	oldFormat, oldArgs := callOutputFormat, callJSONArgs
	t.Cleanup(func() { callOutputFormat, callJSONArgs = oldFormat, oldArgs })
	callOutputFormat, callJSONArgs = "json", "{invalid"
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	oldStdout := os.Stdout
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = oldStdout })

	cmd := &cobra.Command{}
	callErr := runCallToolVariant(cmd, "call_tool_read", "read")
	require.Error(t, callErr)
	require.True(t, cmd.SilenceUsage)
	require.NoError(t, writer.Close())
	bytes, err := io.ReadAll(reader)
	require.NoError(t, err)
	var got output.StructuredError
	require.NoError(t, json.Unmarshal(bytes, &got))
	require.Equal(t, output.ErrCodeOperationFailed, got.Code)
	require.Contains(t, got.Message, "invalid JSON arguments")
}

func TestCallToolDaemonJSONRetainsResultAndErrorStatus(t *testing.T) {
	oldFormat := callOutputFormat
	callOutputFormat = "json"
	t.Cleanup(func() { callOutputFormat = oldFormat })
	for _, tc := range []struct {
		name, data string
		wantError  bool
	}{
		{"success", `{"content":[{"type":"text","text":"{\"ok\":true}"},{"type":"image","data":"abc","mimeType":"image/png"}],"structuredContent":{"ok":true},"_meta":{"trace":"test"}}`, false},
		{"tool error", `{"content":[{"type":"text","text":"denied"}],"isError":true}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/status" {
					_, _ = w.Write([]byte(`{"status":"ok"}`))
					return
				}
				require.Equal(t, "/api/v1/tools/call", r.URL.Path)
				_, _ = w.Write([]byte(`{"success":true,"data":` + tc.data + `}`))
			}))
			defer srv.Close()
			reader, writer, err := os.Pipe()
			require.NoError(t, err)
			oldStdout := os.Stdout
			os.Stdout = writer
			defer func() { os.Stdout = oldStdout }()
			err = runCallToolVariantClientMode(cliclient.NewClient(srv.URL, nil), "call_tool_read", map[string]interface{}{"name": "fixture:read"}, zap.NewNop())
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, writer.Close())
			actual, err := io.ReadAll(reader)
			require.NoError(t, err)
			var got map[string]interface{}
			require.NoError(t, json.Unmarshal(actual, &got))
			var want map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(tc.data), &want))
			require.Equal(t, want["content"], got["content"])
			require.Equal(t, want["structuredContent"], got["structuredContent"])
			require.Equal(t, want["_meta"], got["_meta"])
			require.Equal(t, tc.wantError, got["isError"])
		})
	}
}

func TestCallToolJSONErrorRetainsRequestID(t *testing.T) {
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	oldStdout := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = oldStdout }()
	writeCallJSONError(callDaemonError{cause: &cliclient.APIError{Message: "missing server", RequestID: "req-123"}})
	require.NoError(t, writer.Close())
	bytes, err := io.ReadAll(reader)
	require.NoError(t, err)
	var got output.StructuredError
	require.NoError(t, json.Unmarshal(bytes, &got))
	require.Equal(t, "req-123", got.RequestID)
	require.Equal(t, output.ErrCodeOperationFailed, got.Code)
}

func TestCallToolDaemonPrettyErrorIsNonzeroWithoutSuccessMessage(t *testing.T) {
	oldFormat := callOutputFormat
	callOutputFormat = "pretty"
	defer func() { callOutputFormat = oldFormat }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/status" {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"content":[{"type":"text","text":"fixture denied"}],"isError":true}}`))
	}))
	defer srv.Close()
	stdoutReader, stdoutWriter, err := os.Pipe()
	require.NoError(t, err)
	stderrReader, stderrWriter, err := os.Pipe()
	require.NoError(t, err)
	oldStdout, oldStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutWriter, stderrWriter
	defer func() { os.Stdout, os.Stderr = oldStdout, oldStderr }()

	err = runCallToolVariantClientMode(cliclient.NewClient(srv.URL, nil), "call_tool_read", map[string]interface{}{"name": "fixture:fail"}, zap.NewNop())
	require.ErrorAs(t, err, new(callResultError))
	require.NoError(t, stdoutWriter.Close())
	require.NoError(t, stderrWriter.Close())
	stdout, err := io.ReadAll(stdoutReader)
	require.NoError(t, err)
	stderr, err := io.ReadAll(stderrReader)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(stdout), "fixture denied"))
	require.NotContains(t, string(stderr), "completed successfully")
	require.Contains(t, string(stderr), "returned an error")
}

func TestStandalonePrettyResultErrorIsNonzero(t *testing.T) {
	result := &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("standalone denied")}, IsError: true}
	err := outputPrettyCallResult(result, result.IsError)
	require.ErrorAs(t, err, new(callResultError))
}
