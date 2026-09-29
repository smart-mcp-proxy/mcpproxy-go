package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreflightFixtureLogsToolCallsWhenEnabled(t *testing.T) {
	node, err := exec.LookPath("node")
	require.NoError(t, err)

	callLog := filepath.Join(t.TempDir(), "calls.log")
	cmd := exec.Command(node, "testdata/preflight_fixture_server.js")
	cmd.Env = append(os.Environ(), "FIXTURE_CALL_LOG="+callLog)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	cmd.Stderr = io.Discard
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	_, err = fmt.Fprintln(stdin, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"blocked_tool","arguments":{}}}`)
	require.NoError(t, err)
	line, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	var response struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(line), &response))
	require.Equal(t, "2.0", response.JSONRPC)
	require.Equal(t, 1, response.ID)
	require.NotEmpty(t, response.Result.Content)
	require.Contains(t, response.Result.Content[0].Text, "blocked_tool")

	logged, err := os.ReadFile(callLog)
	require.NoError(t, err)
	require.Equal(t, "blocked_tool\n", strings.ReplaceAll(string(logged), "\r\n", "\n"))
}
