package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clioutput "github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"
)

// 1451-3: conflicting --token/--agent must fail up front for watch.
func TestRunActivityWatch_ConflictingScopeFlagsFailFast(t *testing.T) {
	resetActivityScopeFlags(t)
	prevView, prevType := activityView, activityType
	t.Cleanup(func() { activityView, activityType = prevView, prevType })
	activityView, activityType = "all", ""
	activityToken, activityAgent = "A", "B"
	setOutputGlobals(t, "table", false)

	err := runActivityWatch(activityWatchCmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must name the same token")
}

// 1451-8: the confirmation prompt never touches stdout.
func TestReadConfirmation_PromptGoesToWriter(t *testing.T) {
	var out bytes.Buffer
	ok, err := readConfirmation(strings.NewReader("y\n"), &out, "Revoke?")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "Revoke? [y/N]: ", out.String())
}

// 1451-9: a REST refusal exits 1 even when its text mentions config.
func TestParseAPIError_RefusalsExitOne(t *testing.T) {
	for _, body := range []string{
		`{"error":"invalid config value for scope"}`,
		`{"error":"config error: server missing","field":"servers"}`,
		`not json: config error invalid`,
	} {
		err := parseAPIError([]byte(body), 400, "create token")
		require.Error(t, err)
		assert.Equal(t, ExitCodeGeneralError, classifyError(err), body)
	}
	assert.Equal(t, ExitCodeConfigError, classifyError(errors.New("invalid configuration: x")))
}

// 1435-3b: a refused connect exits non-zero in every format, stdout unchanged.
func TestPrintConnectResult_FailureReturnsError(t *testing.T) {
	result := &connect.ConnectResult{Success: false, Client: "cursor", Action: "failed", Message: "cursor config write failed"}
	for _, format := range []string{"table", "json", "yaml"} {
		formatter, err := clioutput.NewFormatter(format)
		require.NoError(t, err)
		var perr error
		out := captureStdout(t, func() { perr = printConnectResult(result, formatter, format) })
		require.Error(t, perr, format)
		assert.Equal(t, ExitCodeGeneralError, classifyError(perr))
		assert.Contains(t, out, "cursor config write failed", format)
	}
	dup := &connect.ConnectResult{Success: false, Client: "cursor", Action: "already_exists", Message: "dup"}
	jf, _ := clioutput.NewFormatter("json")
	captureStdout(t, func() { require.NoError(t, printConnectResult(dup, jf, "json")) })
	ok := &connect.ConnectResult{Success: true, Client: "cursor", Message: "ok", ConfigPath: "/x"}
	formatter, _ := clioutput.NewFormatter("json")
	captureStdout(t, func() { require.NoError(t, printConnectResult(ok, formatter, "json")) })
}

// 1466-12 / 1466-27 live in review tests; wording for --all:
func TestReviewApproveWording_All(t *testing.T) {
	_, summary := reviewApproveWording("m", 9, 9, nil, true)
	assert.Equal(t, "Allowing all pending or changed tools; previously blocked tools stay blocked", summary)
	_, summary = reviewApproveWording("m", 9, 9, nil, false)
	assert.Equal(t, "Allowing 9 of 9 tools; blocking none", summary)
}

func TestReviewScanLine_EmptyCoverageIsNone(t *testing.T) {
	assert.Equal(t, "Scan: none", reviewScanLine(map[string]interface{}{"name": "n", "scan": map[string]interface{}{"verdict": "clean"}}))
}

// 1466-15: an explicit, unloadable --config is an error, not a silent default.
func TestLoadRegistryConfig_MissingExplicitPathErrors(t *testing.T) {
	prev := registryConfigPath
	t.Cleanup(func() { registryConfigPath = prev })
	registryConfigPath = t.TempDir() + "/nope.json"
	_, err := loadRegistryConfig()
	require.Error(t, err)
}

// 1466-16: --data-dir with no config still gets the MCPPROXY_LISTEN overlay.
func TestLoadCLIConfig_DataDirOnlyAppliesEnvOverlay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MCPPROXY_LISTEN", "127.0.0.1:18999")
	prev := dataDir
	t.Cleanup(func() { dataDir = prev })
	dataDir = t.TempDir()
	cfg, err := loadCLIConfig("")
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:18999", cfg.Listen)
}

// 1466-17: an apostrophe inside a URL must not end the redaction early.
func TestRedactDoctorString_ApostropheInURL(t *testing.T) {
	got := redactDoctorString("see https://h.example/x?label=Bob's&%74oken=sekret-1 ok")
	assert.NotContains(t, got, "sekret-1")
	quoted := redactDoctorString("dial 'https://h.example/x?token=sekret-2' failed")
	assert.NotContains(t, quoted, "sekret-2")
	assert.Contains(t, quoted, "REDACTED'")
}

// 1394-5: FR-075 verbatim message.
func TestActivityPeriodFromRelative_FR075Message(t *testing.T) {
	_, err := activityPeriodFromRelative("-3d")
	require.Error(t, err)
	assert.Equal(t, "summary supports --from -1h|-24h|-7d|-30d only", err.Error())
}

// Disconnecting a never-registered client is a no-op and must exit 0.
func TestConnectResultError_NotFoundDisconnectExitsZero(t *testing.T) {
	nf := &connect.ConnectResult{Success: false, Client: "cursor", Action: "not_found", Message: "no entry"}
	require.NoError(t, connectResultError(nf))
	require.Error(t, connectResultError(&connect.ConnectResult{Action: "failed", Message: "x"}))
}

// The confirmation prompt must default to stderr, never stdout.
func TestConfirmPromptOut_DefaultsToStderr(t *testing.T) {
	assert.Same(t, os.Stderr, confirmPromptOut)
}
