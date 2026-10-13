package main

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// tokenCommandForTest builds the token command and re-points its --config
// override at the recorder's config. GetTokenCommand binds the flag with
// StringVarP, which resets tokenConfigPath to "" on every construction, so
// without this the command falls back to the default config in $HOME and
// (order-dependently, under -shuffle) prints "Created default configuration
// file" to stderr.
func tokenCommandForTest() *cobra.Command {
	cmd := GetTokenCommand()
	tokenConfigPath = configFile
	return cmd
}

// newTokenRecorder points the token command (which has its own --config
// override) at the recording daemon.
func newTokenRecorder(t *testing.T, routes map[string]cannedResponse) *restRecorder {
	t.Helper()
	rec := newRESTRecorder(t, routes)
	prev := tokenConfigPath
	tokenConfigPath = configFile
	t.Cleanup(func() { tokenConfigPath = prev })
	return rec
}

const createdToken = `{"name":"ci","token":"mcp_agt_0123456789abcdef","allowed_servers":["*"],"permissions":["read","write","destructive"],"expires_at":"2026-10-30T00:00:00Z","profile_pin":"work-ro2"}`

func TestTokenCreate_ProfileDefaults(t *testing.T) {
	rec := newTokenRecorder(t, map[string]cannedResponse{"POST /api/v1/tokens": createdResp(createdToken)})
	out, errOut, err := runCLI(t, tokenCommandForTest, "table", "create", "--name", "ci", "--profile", "work-ro2")
	require.NoError(t, err)
	body := rec.only(t).jsonBody(t)
	require.Equal(t, map[string]any{"name": "ci", "profile": "work-ro2", "expires_in": "30d"}, body, "scope comes from the profile: no allowed_servers or permissions")
	require.Contains(t, out, "Profile:     work-ro2")
	require.Empty(t, errOut, "no legacy hint and no deprecation notice with --profile")
}

func TestTokenCreate_ProfilePinDeprecatedNotice(t *testing.T) {
	rec := newTokenRecorder(t, map[string]cannedResponse{"POST /api/v1/tokens": createdResp(createdToken)})
	_, errOut, err := runCLI(t, tokenCommandForTest, "table", "create", "--name", "ci", "--profile-pin", "work-ro2")
	require.NoError(t, err)
	require.Contains(t, errOut, "--profile-pin is deprecated; use --profile")
	require.Equal(t, "work-ro2", rec.only(t).jsonBody(t)["profile"])

	rec = newTokenRecorder(t, map[string]cannedResponse{})
	_, _, err = runCLI(t, tokenCommandForTest, "table", "create", "--name", "ci", "--profile", "a", "--profile-pin", "b")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	require.Empty(t, rec.all(), "a differing pair is refused locally")

	newTokenRecorder(t, map[string]cannedResponse{"POST /api/v1/tokens": createdResp(createdToken)})
	_, _, err = runCLI(t, tokenCommandForTest, "table", "create", "--name", "ci", "--profile", "a", "--profile-pin", "a")
	require.NoError(t, err, "an equal pair is fine")
}

func TestTokenCreate_ProfilePinIsHidden(t *testing.T) {
	for _, c := range GetTokenCommand().Commands() {
		if c.Name() == "create" {
			require.True(t, c.Flags().Lookup("profile-pin").Hidden)
			require.NotNil(t, c.Flags().Lookup("profile"))
			return
		}
	}
	t.Fatal("create not registered")
}

func TestTokenCreate_LegacyHint(t *testing.T) {
	rec := newTokenRecorder(t, map[string]cannedResponse{"POST /api/v1/tokens": createdResp(`{"name":"old","token":"mcp_agt_x","allowed_servers":["github"],"permissions":["read"],"expires_at":"2026-10-30T00:00:00Z"}`)})
	_, errOut, err := runCLI(t, tokenCommandForTest, "table", "create", "--name", "old", "--servers", "github", "--permissions", "read")
	require.NoError(t, err)
	require.Contains(t, errOut, "hint: consider --profile <name> instead of --servers/--permissions (legacy scope)")
	body := rec.only(t).jsonBody(t)
	require.Equal(t, []any{"github"}, body["allowed_servers"])
	require.Equal(t, []any{"read"}, body["permissions"])
	require.NotContains(t, body, "profile")
}

func TestTokenCreate_RequiresProfileOrLegacy(t *testing.T) {
	rec := newTokenRecorder(t, map[string]cannedResponse{})
	_, _, err := runCLI(t, tokenCommandForTest, "table", "create", "--name", "x")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	require.Equal(t, "either --profile or --servers/--permissions is required", err.Error())
	require.Empty(t, rec.all())

	// The required-flag rules: only --name is mandatory.
	for _, c := range GetTokenCommand().Commands() {
		if c.Name() == "create" {
			for _, name := range []string{"servers", "permissions", "profile"} {
				require.NotContains(t, c.Flags().Lookup(name).Annotations, "cobra_annotation_bash_completion_one_required_flag", name)
			}
		}
	}
}

func TestTokenCreate_ClientPrefix400Passthrough(t *testing.T) {
	text := `token names starting with "client-" are reserved for client credentials`
	rec := newTokenRecorder(t, map[string]cannedResponse{"POST /api/v1/tokens": refuse(http.StatusBadRequest, `{"success":false,"error":`+mustJSON(t, text)+`,"field":"name"}`)})
	_, _, err := runCLI(t, tokenCommandForTest, "table", "create", "--name", "client-x", "--servers", "*", "--permissions", "read")
	require.Error(t, err)
	require.Equal(t, ExitCodeGeneralError, classifyError(err))
	require.Contains(t, err.Error(), text)
	require.Contains(t, err.Error(), "field: name")
	require.Len(t, rec.all(), 1, "the prefix is checked by the one server validator, not locally")
}

const tokenListFixture = `{"tokens":[
{"name":"ci","token_prefix":"mcp_agt_ab12","kind":"agent","allowed_servers":["*"],"permissions":["read","write","destructive"],"expires_at":"2026-10-30T00:00:00Z","revoked":false,"profile_pin":"work-ro2","legacy_scope":false},
{"name":"old","token_prefix":"mcp_agt_cd34","kind":"agent","allowed_servers":["github"],"permissions":["read"],"expires_at":"2026-10-30T00:00:00Z","revoked":false,"legacy_scope":true},
{"name":"client-cursor","token_prefix":"mcp_cli_ef56","kind":"client","client_id":"cursor","allowed_servers":["*"],"permissions":["read","write","destructive"],"expires_at":"2027-09-25T00:00:00Z","revoked":false,"profile_pin":"work-ro2","profile_mode":"locked","legacy_scope":false}]}`

func TestTokenList_ColumnsAndFilters(t *testing.T) {
	rec := newTokenRecorder(t, map[string]cannedResponse{"GET /api/v1/tokens": okResp(tokenListFixture)})
	out, _, err := runCLI(t, tokenCommandForTest, "table", "list")
	require.NoError(t, err)
	assertGolden108(t, "token-list.golden", out)
	require.Empty(t, rec.only(t).RawQuery)

	for _, tc := range []struct {
		args  []string
		query url.Values
	}{
		{[]string{"--profile", "work-ro2"}, url.Values{"profile": {"work-ro2"}}},
		{[]string{"--profile", "-"}, url.Values{"profile": {"-"}}},
		{[]string{"--token", "ci"}, url.Values{"token": {"ci"}}},
	} {
		rec = newTokenRecorder(t, map[string]cannedResponse{"GET /api/v1/tokens": okResp(tokenListFixture)})
		_, _, err = runCLI(t, tokenCommandForTest, "table", append([]string{"list"}, tc.args...)...)
		require.NoError(t, err)
		q, _ := url.ParseQuery(rec.only(t).RawQuery)
		require.Equal(t, tc.query, q)
	}
}

func TestTokenList_JSON(t *testing.T) {
	newTokenRecorder(t, map[string]cannedResponse{"GET /api/v1/tokens": okResp(tokenListFixture)})
	out, _, err := runCLI(t, tokenCommandForTest, "json", "list")
	require.NoError(t, err)
	require.JSONEq(t, tokenListFixture, out)
	out, _, err = runCLI(t, tokenCommandForTest, "yaml", "list")
	require.NoError(t, err)
	require.Contains(t, out, "legacy_scope: true", "yaml goes through the formatter too")
}

func TestTokenShow_LegacyScopeHintAndClientKind(t *testing.T) {
	newTokenRecorder(t, map[string]cannedResponse{
		"GET /api/v1/tokens/old":           okResp(`{"name":"old","token_prefix":"mcp_agt_cd34","kind":"agent","allowed_servers":["github"],"permissions":["read"],"expires_at":"2026-10-30T00:00:00Z","revoked":false,"legacy_scope":true}`),
		"GET /api/v1/tokens/client-cursor": okResp(`{"name":"client-cursor","token_prefix":"mcp_cli_ef56","kind":"client","client_id":"cursor","allowed_servers":["*"],"permissions":["read","write","destructive"],"profile_pin":"work-ro2","profile_mode":"locked","legacy_scope":false}`),
	})
	out, _, err := runCLI(t, tokenCommandForTest, "table", "show", "old")
	require.NoError(t, err)
	require.Contains(t, out, "Kind:           agent")
	require.Contains(t, out, "Legacy scope:   yes (servers github, permissions read) — migrate: mcpproxy token create --profile <p>")
	require.NotContains(t, out, "Profile Pin")

	out, _, err = runCLI(t, tokenCommandForTest, "table", "show", "client-cursor")
	require.NoError(t, err)
	require.Contains(t, out, "Kind:           client")
	require.Contains(t, out, "Client:         cursor")
	require.Contains(t, out, "Profile:        work-ro2")
	require.Contains(t, out, "Mode:           locked")
	require.Contains(t, out, "Manage with:    mcpproxy client show cursor")
	require.NotContains(t, out, "Legacy scope")
}
