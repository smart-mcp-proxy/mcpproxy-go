//go:build unix

package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secureenv"
)

const spawnSecret = "SUPERSECRETBETAARGVVALUE"

// assertNoSecretInLog fails on any recorded field, message or fragment that
// carries the secret.
func assertNoSecretInLog(t *testing.T, recorded *observer.ObservedLogs, secret string) {
	t.Helper()
	for _, entry := range recorded.All() {
		if strings.Contains(entry.Message, secret) {
			t.Errorf("secret in log MESSAGE %q", entry.Message)
		}
		for k, v := range entry.ContextMap() {
			if strings.Contains(fmt.Sprintf("%v", v), secret) {
				t.Errorf("secret in log field %q of %q: %v", k, entry.Message, v)
			}
		}
	}
}

// findField returns the stringified value of the first occurrence of the named
// field on the named log message.
func findField(recorded *observer.ObservedLogs, msg, field string) (string, bool) {
	for _, entry := range recorded.All() {
		if entry.Message != msg {
			continue
		}
		if v, ok := entry.ContextMap()[field]; ok {
			return fmt.Sprintf("%v", v), true
		}
	}
	return "", false
}

// TestBuildLauncherCmd_RedactsPlainArgvSecret drives the real buildLauncherCmd
// on the plain (non-docker) path — the shape the live repro leaked, where
// wrapWithUserShell folds the whole command line into one `-c "..."` element.
func TestBuildLauncherCmd_RedactsPlainArgvSecret(t *testing.T) {
	obsCore, recorded := observer.New(zapcore.DebugLevel)

	c := &Client{
		config: &config.ServerConfig{
			Name:    "beta",
			Command: "npx",
			Args:    []string{"some-mcp", "--api-key", spawnSecret},
			URL:     "http://127.0.0.1:9000",
		},
		logger:           zap.New(obsCore),
		isolationManager: NewIsolationManager(config.DefaultDockerIsolationConfig()),
		envManager:       secureenv.NewManager(nil),
	}

	cmd, _, _, err := c.buildLauncherCmd(context.Background(), false)
	require.NoError(t, err)

	// Positive control: the SPAWNED command really does still carry the secret,
	// so the log assertion below is about redaction and not about a fixture
	// that never contained it.
	require.Contains(t, strings.Join(cmd.Args, " "), spawnSecret,
		"fixture did not land: the child argv must actually carry the secret")

	assertNoSecretInLog(t, recorded, spawnSecret)

	// Positive control on the same log line: the flag must survive, or the
	// masking has destroyed the diagnostic it exists for.
	args, ok := findField(recorded, "launcher command prepared", "args")
	require.True(t, ok, "the launcher log line under test was not emitted")
	assert.Contains(t, args, "--api-key",
		"masking must keep the flag name: %s", args)
}

// TestBuildLauncherCmd_RedactsInjectedDockerEnv covers the other class: an env
// var that config turns into `-e KEY=VALUE` argv, under a BENIGN key name that
// no name matcher can recognise.
func TestBuildLauncherCmd_RedactsInjectedDockerEnv(t *testing.T) {
	obsCore, recorded := observer.New(zapcore.DebugLevel)

	c := &Client{
		config: &config.ServerConfig{
			Name:    "beta-docker",
			Command: "docker",
			Args:    []string{"run", "--rm", "-e", "BENIGN_NAME=" + spawnSecret, "myimage"},
			URL:     "http://127.0.0.1:9000",
		},
		logger:           zap.New(obsCore),
		isolationManager: NewIsolationManager(config.DefaultDockerIsolationConfig()),
		envManager:       secureenv.NewManager(nil),
	}

	_, _, _, err := c.buildLauncherCmd(context.Background(), true)
	require.NoError(t, err)

	assertNoSecretInLog(t, recorded, spawnSecret)

	args, ok := findField(recorded, "launcher command prepared", "args")
	require.True(t, ok, "the launcher log line under test was not emitted")
	assert.Contains(t, args, "BENIGN_NAME=",
		"masking must keep the env KEY: %s", args)
}

// TestConnectStdioLogFields_RedactPlainArgvSecret covers wrapWithUserShell's own
// debug line (connection_stdio.go), which the repro showed leaking
// `original_args` twice per startup.
func TestConnectStdioLogFields_RedactPlainArgvSecret(t *testing.T) {
	obsCore, recorded := observer.New(zapcore.DebugLevel)

	c := &Client{
		config: &config.ServerConfig{
			Name:    "beta",
			Command: "npx",
			Args:    []string{"some-mcp", "--api-key", spawnSecret},
		},
		logger: zap.New(obsCore),
	}

	_, shellArgs := c.wrapWithUserShell(c.config.Command, c.config.Args)
	require.Contains(t, strings.Join(shellArgs, " "), spawnSecret,
		"fixture did not land: the wrapped command must actually carry the secret")

	assertNoSecretInLog(t, recorded, spawnSecret)

	args, ok := findField(recorded,
		"Wrapping command with user shell for full environment inheritance", "original_args")
	require.True(t, ok, "the wrapWithUserShell log line under test was not emitted")
	assert.Contains(t, args, "--api-key", "masking must keep the flag name: %s", args)
}

// TestInsertCidfileIntoShellDockerCommand_RedactsCommandStrings covers the
// `original_cmd` / `modified_cmd` string fields, the one shape a []string-only
// redactor structurally cannot reach.
func TestInsertCidfileIntoShellDockerCommand_RedactsCommandStrings(t *testing.T) {
	obsCore, recorded := observer.New(zapcore.DebugLevel)

	c := &Client{
		config: &config.ServerConfig{Name: "beta-docker"},
		logger: zap.New(obsCore),
	}

	shellArgs := []string{"-l", "-c",
		"docker run --rm -e BENIGN_NAME=" + spawnSecret + " myimage uvx mcp --api-key " + spawnSecret}
	out := c.insertCidfileIntoShellDockerCommand(shellArgs, "/tmp/cid.txt")
	require.Contains(t, strings.Join(out, " "), spawnSecret,
		"fixture did not land: the rewritten command must still carry the secret")

	assertNoSecretInLog(t, recorded, spawnSecret)

	cmdField, ok := findField(recorded, "Inserted cidfile into shell-wrapped Docker command", "modified_cmd")
	require.True(t, ok, "the cidfile log line under test was not emitted")
	assert.Contains(t, cmdField, "-e BENIGN_NAME=",
		"masking must keep the command shape: %s", cmdField)
}
