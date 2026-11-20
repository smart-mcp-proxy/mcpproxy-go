package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// gatedKeyringProvider is a fakeKeyringProvider that also answers the reasoned
// availability probe and the macOS write opt-in question, the way the real
// KeyringProvider does.
type gatedKeyringProvider struct {
	*fakeKeyringProvider
	reason        string
	optInRequired bool
}

func (g *gatedKeyringProvider) IsAvailableWithReason() (bool, string) {
	if g.optInRequired || g.reason != "" {
		return false, g.reason
	}
	return g.available, ""
}

func (g *gatedKeyringProvider) WriteOptInRequired() bool { return g.optInRequired }

// consumedReader records whether anything tried to read the secret value.
type consumedReader struct {
	r    io.Reader
	read bool
}

func (c *consumedReader) Read(p []byte) (int, error) {
	c.read = true
	return c.r.Read(p)
}

func runSecretsSetForTest(t *testing.T, provider *gatedKeyringProvider, args []string, opts secretsSetOptions, stdin string) (string, *consumedReader, error) {
	t.Helper()
	resolver := newTestSecretResolver(provider.fakeKeyringProvider)
	resolver.RegisterProvider("keyring", provider)
	in := &consumedReader{r: strings.NewReader(stdin)}
	var out bytes.Buffer
	if opts.secretType == "" {
		opts.secretType = "keyring"
	}
	err := runSecretsSet(resolver, in, &out, args, opts)
	return out.String(), in, err
}

// GH #1924: without the macOS write opt-in, `secrets set` used to ask for the
// secret and only then fail with "keyring unavailable (backend not usable on
// this system …)". It must refuse BEFORE asking, say that the opt-in is what
// is missing, and give the one command that works.
func TestSecretsSet_MacOptInMissing_RefusesBeforeAskingForTheValue(t *testing.T) {
	provider := &gatedKeyringProvider{
		fakeKeyringProvider: newFakeKeyringProvider(true),
		reason:              "OS keychain writes require opting in on macOS outside the tray app",
		optInRequired:       true,
	}

	out, in, err := runSecretsSetForTest(t, provider, []string{"github-pat"}, secretsSetOptions{}, "typed-secret\n")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if in.read {
		t.Error("the secret value must not be read when the write cannot happen")
	}
	if strings.Contains(out, "Enter secret value") {
		t.Errorf("the value must not be prompted for, got output %q", out)
	}
	if provider.storeCalls != 0 {
		t.Errorf("Store must not be called, got %d calls", provider.storeCalls)
	}
	msg := err.Error()
	if !strings.Contains(msg, "MCPPROXY_KEYRING_WRITE=1 mcpproxy secrets set github-pat") {
		t.Errorf("the refusal must give the opt-in command for this secret, got %q", msg)
	}
	if strings.Contains(msg, "backend not usable") || strings.Contains(msg, "keyring unavailable") {
		t.Errorf("a missing opt-in must not be reported as an unavailable keyring, got %q", msg)
	}
	if strings.Contains(msg, "typed-secret") {
		t.Errorf("the refusal must not echo a value, got %q", msg)
	}
}

// The opt-in command repeats how the value was going to be supplied, so it
// can be re-run as printed.
func TestSecretsSet_MacOptInMissing_CommandKeepsTheValueSource(t *testing.T) {
	newProvider := func() *gatedKeyringProvider {
		return &gatedKeyringProvider{fakeKeyringProvider: newFakeKeyringProvider(true), reason: "opt-in", optInRequired: true}
	}

	_, _, err := runSecretsSetForTest(t, newProvider(), []string{"github-pat"}, secretsSetOptions{fromStdin: true}, "v\n")
	if err == nil || !strings.Contains(err.Error(), "MCPPROXY_KEYRING_WRITE=1 mcpproxy secrets set github-pat --from-stdin") {
		t.Errorf("expected the --from-stdin form, got %v", err)
	}

	_, _, err = runSecretsSetForTest(t, newProvider(), []string{"github-pat"}, secretsSetOptions{fromEnv: "GH_PAT"}, "")
	if err == nil || !strings.Contains(err.Error(), "MCPPROXY_KEYRING_WRITE=1 mcpproxy secrets set github-pat --from-env=GH_PAT") {
		t.Errorf("expected the --from-env form, got %v", err)
	}

	// A value given as an argument is never repeated in the message.
	_, _, err = runSecretsSetForTest(t, newProvider(), []string{"github-pat", "inline-secret-value"}, secretsSetOptions{}, "")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if strings.Contains(err.Error(), "inline-secret-value") {
		t.Errorf("the refusal must not echo the value argument, got %q", err.Error())
	}
}

// The printed command is meant to be copied: a name the shell would split or
// expand is quoted, so the retry stores under the same name.
func TestSecretsSet_MacOptInMissing_CommandQuotesShellUnsafeNames(t *testing.T) {
	newProvider := func() *gatedKeyringProvider {
		return &gatedKeyringProvider{fakeKeyringProvider: newFakeKeyringProvider(true), reason: "opt-in", optInRequired: true}
	}

	_, _, err := runSecretsSetForTest(t, newProvider(), []string{"prod api"}, secretsSetOptions{}, "")
	if err == nil || !strings.Contains(err.Error(), "mcpproxy secrets set 'prod api'\n") {
		t.Errorf("a name with a space must be quoted, got %v", err)
	}

	_, _, err = runSecretsSetForTest(t, newProvider(), []string{"it's; $(x)"}, secretsSetOptions{fromEnv: "A B"}, "")
	if err == nil || !strings.Contains(err.Error(), `mcpproxy secrets set 'it'\''s; $(x)' --from-env='A B'`) {
		t.Errorf("quotes and metacharacters must be neutralised, got %v", err)
	}

	for arg, want := range map[string]string{
		"github-pat":      "github-pat",
		"team/api_key.v2": "team/api_key.v2",
		"":                "''",
		"a b":             "'a b'",
		"$HOME":           "'$HOME'",
		"=sh":             "'=sh'",
	} {
		if got := shellQuoteArg(arg); got != want {
			t.Errorf("shellQuoteArg(%q) = %q, want %q", arg, got, want)
		}
	}
}

// A keyring that really is unavailable (locked, unresponsive, headless) is a
// different condition: its own reason is reported, with no opt-in advice, and
// still before the value is asked for.
func TestSecretsSet_KeyringUnavailable_ReportsTheReasonBeforeAsking(t *testing.T) {
	provider := &gatedKeyringProvider{
		fakeKeyringProvider: newFakeKeyringProvider(false),
		reason:              "the OS keychain did not respond to a probe request",
	}

	out, in, err := runSecretsSetForTest(t, provider, []string{"github-pat"}, secretsSetOptions{}, "typed-secret\n")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if in.read || strings.Contains(out, "Enter secret value") {
		t.Error("the value must not be asked for when the keyring is unavailable")
	}
	msg := err.Error()
	if !strings.Contains(msg, "did not respond to a probe request") {
		t.Errorf("the provider's reason must be reported, got %q", msg)
	}
	if strings.Contains(msg, "MCPPROXY_KEYRING_WRITE") {
		t.Errorf("an unavailable backend must not be answered with opt-in advice, got %q", msg)
	}
}

// With the keyring writable the command behaves as before: it prompts, reads
// and stores.
func TestSecretsSet_Available_PromptsAndStores(t *testing.T) {
	provider := &gatedKeyringProvider{fakeKeyringProvider: newFakeKeyringProvider(true)}

	out, _, err := runSecretsSetForTest(t, provider, []string{"github-pat"}, secretsSetOptions{}, "typed-secret\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Enter secret value") {
		t.Errorf("an interactive set must prompt, got %q", out)
	}
	if got := provider.store["github-pat"]; got != "typed-secret" {
		t.Errorf("stored %q, want the typed value", got)
	}
	if !strings.Contains(out, "${keyring:github-pat}") {
		t.Errorf("the reference to use must be printed, got %q", out)
	}
}

// --from-stdin is for piped input: no prompt belongs on stdout.
func TestSecretsSet_FromStdin_DoesNotPrompt(t *testing.T) {
	provider := &gatedKeyringProvider{fakeKeyringProvider: newFakeKeyringProvider(true)}

	out, _, err := runSecretsSetForTest(t, provider, []string{"github-pat"}, secretsSetOptions{fromStdin: true}, "piped-secret\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "Enter secret value") {
		t.Errorf("--from-stdin must not prompt, got %q", out)
	}
	if got := provider.store["github-pat"]; got != "piped-secret" {
		t.Errorf("stored %q, want the piped value", got)
	}
}

// The env provider has no keyring gate; the preflight must not apply to it.
func TestSecretsSet_NonKeyringTypeSkipsTheKeyringPreflight(t *testing.T) {
	provider := &gatedKeyringProvider{fakeKeyringProvider: newFakeKeyringProvider(true), reason: "opt-in", optInRequired: true}

	_, _, err := runSecretsSetForTest(t, provider, []string{"SOME_VAR", "v"}, secretsSetOptions{secretType: "env"}, "")
	if err != nil && strings.Contains(err.Error(), "MCPPROXY_KEYRING_WRITE") {
		t.Errorf("--type env must not be refused by the keyring opt-in, got %v", err)
	}
}

func TestSecretsSetCommand_HelpExplainsTheMacOptIn(t *testing.T) {
	long := getSecretsSetCommand().Long
	if !strings.Contains(long, "MCPPROXY_KEYRING_WRITE=1") {
		t.Errorf("`secrets set --help` must explain the macOS write opt-in, got:\n%s", long)
	}
}
