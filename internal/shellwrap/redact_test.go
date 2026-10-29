package shellwrap

import (
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const (
	secretValueSlack = "xoxc-abc123456789"
	secretValueJira  = "xoxd-zzz987654321"
)

// assertNoSecret fails if s contains any of the cleartext secret values that
// must never reach a log.
func assertNoSecret(t *testing.T, s string) {
	t.Helper()
	for _, sv := range []string{secretValueSlack, secretValueJira} {
		if strings.Contains(s, sv) {
			t.Errorf("cleartext secret %q leaked in %q", sv, s)
		}
	}
}

func TestRedactDockerArgs_TwoTokenForm(t *testing.T) {
	in := []string{"run", "--rm", "-i", "-e", "SLACK_TOKEN=" + secretValueSlack, "myimage:latest"}
	out := RedactDockerArgs(in)

	joined := strings.Join(out, " ")
	assertNoSecret(t, joined)

	// Non-secret args untouched.
	for _, want := range []string{"run", "--rm", "-i", "myimage:latest"} {
		if !contains(out, want) {
			t.Errorf("expected non-secret arg %q to be preserved, got %v", want, out)
		}
	}
	// Key and flag preserved.
	if !strings.Contains(joined, "-e") || !strings.Contains(joined, "SLACK_TOKEN=") {
		t.Errorf("expected -e and key SLACK_TOKEN= preserved, got %q", joined)
	}
	// Input slice must not be mutated.
	if in[4] != "SLACK_TOKEN="+secretValueSlack {
		t.Errorf("RedactDockerArgs mutated its input slice: %q", in[4])
	}
}

func TestRedactDockerArgs_SingleGluedToken(t *testing.T) {
	in := []string{"run", "-eSLACK_TOKEN=" + secretValueSlack, "myimage"}
	out := RedactDockerArgs(in)
	joined := strings.Join(out, " ")
	assertNoSecret(t, joined)
	if !strings.Contains(joined, "SLACK_TOKEN=") {
		t.Errorf("expected key preserved, got %q", joined)
	}
}

func TestRedactDockerArgs_CommandStringElement(t *testing.T) {
	in := []string{"-l", "-c", "docker run --rm -e SLACK_TOKEN=" + secretValueSlack + " -e JIRA_TOKEN=" + secretValueJira + " myimage:latest"}
	out := RedactDockerArgs(in)
	joined := strings.Join(out, " ")
	assertNoSecret(t, joined)
	for _, want := range []string{"-l", "docker run", "--rm", "myimage:latest", "SLACK_TOKEN=", "JIRA_TOKEN="} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q preserved in %q", want, joined)
		}
	}
}

func TestRedactDockerCommandString(t *testing.T) {
	in := "docker run --rm -e SLACK_TOKEN=" + secretValueSlack + " --env JIRA_TOKEN=" + secretValueJira + " myimage"
	out := RedactDockerCommandString(in)
	assertNoSecret(t, out)
	for _, want := range []string{"docker run", "--rm", "myimage", "SLACK_TOKEN=", "JIRA_TOKEN="} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q preserved in %q", want, out)
		}
	}
}

// The glued spellings are masked as separate argv elements; they must be
// masked the same way once a login-shell wrap folds them into one -c string.
func TestRedactDockerCommandString_GluedForms(t *testing.T) {
	cases := map[string]struct{ in, keep string }{
		"long glued":         {"docker run --rm --env=SLACK_TOKEN=" + secretValueSlack + " myimage", "--env=SLACK_TOKEN="},
		"short glued":        {"docker run --rm -eSLACK_TOKEN=" + secretValueSlack + " myimage", "-eSLACK_TOKEN="},
		"shellescaped glued": {"docker run --rm '--env=SLACK_TOKEN=" + secretValueSlack + " x' myimage", "'--env=SLACK_TOKEN="},
		"double quoted":      {`docker run --rm "-eSLACK_TOKEN=` + secretValueSlack + `" myimage`, `"-eSLACK_TOKEN=`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out := RedactDockerCommandString(tc.in)
			assertNoSecret(t, out)
			for _, want := range []string{"docker run", "--rm", "myimage", tc.keep} {
				if !strings.Contains(out, want) {
					t.Errorf("expected %q preserved in %q", want, out)
				}
			}
			args := RedactDockerArgs([]string{"-l", "-c", tc.in})
			assertNoSecret(t, strings.Join(args, " "))
		})
	}
	// Flags that merely start with -e and carry no KEY=VALUE are untouched.
	for _, in := range []string{"node -e console.log(1)", "grep -enum file", "docker run --entrypoint /bin/sh img"} {
		if out := RedactDockerCommandString(in); out != in {
			t.Errorf("non-env command altered: %q -> %q", in, out)
		}
	}
}

func TestRedactDockerArgs_NoEnvFlagsUnchanged(t *testing.T) {
	in := []string{"run", "--rm", "-i", "ghcr.io/foo/bar:latest", "--config", "/etc/x.json"}
	out := RedactDockerArgs(in)
	if strings.Join(in, " ") != strings.Join(out, " ") {
		t.Errorf("non-env args were altered: in=%v out=%v", in, out)
	}
}

// Guard test: the WrapWithUserShell debug log must not emit cleartext secrets
// in its wrapped_command / original_args fields.
func TestWrapWithUserShell_LogsAreRedacted(t *testing.T) {
	obsCore, recorded := observer.New(zapcore.DebugLevel)
	logger := zap.New(obsCore)

	WrapWithUserShell(logger, "docker", []string{"run", "--rm", "-e", "SLACK_TOKEN=" + secretValueSlack, "myimage"})

	if recorded.Len() == 0 {
		t.Fatal("expected a debug log entry from WrapWithUserShell")
	}
	for _, entry := range recorded.All() {
		for k, v := range entry.ContextMap() {
			assertNoSecret(t, k)
			assertNoSecret(t, fmt.Sprintf("%v", v))
		}
		assertNoSecret(t, entry.Message)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
