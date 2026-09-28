package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// SEC-01: on auto-generation the raw admin API key was written into the
// persistent log sink (~/Library/Logs/mcpproxy/main.log) three times - as the
// `api_key` field, embedded in the `web_ui_url` field, and again on the
// config-save-failure path. The operator still has to SEE the key once, so it
// goes to the human sink (stderr) only; the log file gets the masked prefix and
// a redacted URL.
const bannerSecret = "9f2c4d6e8a0b1c3d5e7f9a1b3c5d7e9f"

func renderObserved(t *testing.T, logs *observer.ObservedLogs) string {
	t.Helper()
	var sb strings.Builder
	for _, e := range logs.All() {
		sb.WriteString(e.Message)
		for k, v := range e.ContextMap() {
			fmt.Fprintf(&sb, " %s=%v", k, v)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func TestGeneratedAPIKeyBanner_KeyNeverReachesLogSink(t *testing.T) {
	for _, tc := range []struct {
		name    string
		saveErr error
	}{
		{name: "persisted"},
		{name: "save failed", saveErr: errors.New("permission denied")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			var out bytes.Buffer

			announceGeneratedAPIKey(zap.New(core), &out, true, generatedAPIKeyInfo{
				APIKey:     bannerSecret,
				Listen:     "127.0.0.1:8080",
				Source:     "generated",
				ConfigPath: "/tmp/mcp_config.json",
				SaveErr:    tc.saveErr,
			})

			rendered := renderObserved(t, logs)
			if strings.Contains(rendered, bannerSecret) {
				t.Fatalf("SEC-01: raw API key reached the persistent log sink:\n%s", rendered)
			}
			if !strings.Contains(rendered, maskAPIKey(bannerSecret)) {
				t.Fatalf("log sink should still carry the masked key prefix, got:\n%s", rendered)
			}

			banner := out.String()
			if !strings.Contains(banner, bannerSecret) {
				t.Fatalf("the operator must be able to see the key once on the human sink, got:\n%s", banner)
			}
			if !strings.Contains(banner, "http://127.0.0.1:8080/ui/?apikey="+bannerSecret) {
				t.Fatalf("banner should carry the ready-to-use Web UI URL, got:\n%s", banner)
			}
			if !strings.Contains(banner, "/tmp/mcp_config.json") {
				t.Fatalf("banner should name the config path, got:\n%s", banner)
			}
		})
	}
}

// When stderr is not a terminal (systemd, launchd, a CI redirect) the raw key
// must not be blurted into whatever file the stream was redirected to; the
// operator is pointed at the config file instead.
func TestGeneratedAPIKeyBanner_NonTTYDoesNotPrintTheKey(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	var out bytes.Buffer

	announceGeneratedAPIKey(zap.New(core), &out, false, generatedAPIKeyInfo{
		APIKey:     bannerSecret,
		Listen:     "127.0.0.1:8080",
		Source:     "generated",
		ConfigPath: "/tmp/mcp_config.json",
	})

	if strings.Contains(out.String(), bannerSecret) {
		t.Fatalf("non-TTY stderr must not carry the raw key, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "/tmp/mcp_config.json") {
		t.Fatalf("non-TTY banner must point at the config file, got:\n%s", out.String())
	}
	if strings.Contains(renderObserved(t, logs), bannerSecret) {
		t.Fatal("SEC-01: raw API key reached the persistent log sink")
	}
}

// codex rounds 1 and 3 finding 2. Round 1 objected that withholding the key on
// a non-terminal stream leaves an unsaved key unrecoverable; round 3 objected
// that printing it there is the very exposure this change removes, because
// launchd, systemd and CI all persist that stream. Round 3 wins: the raw key is
// never written to a non-terminal sink. What the operator gets instead is the
// action that fixes it.
func TestGeneratedAPIKeyBanner_NonTTYWithdrawsTheKeyEvenWhenUnsaved(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	var out bytes.Buffer

	announceGeneratedAPIKey(zap.New(core), &out, false, generatedAPIKeyInfo{
		APIKey:     bannerSecret,
		Listen:     "127.0.0.1:8080",
		Source:     "generated",
		ConfigPath: "/read-only/mcp_config.json",
		SaveErr:    errors.New("permission denied"),
	})

	banner := out.String()
	if strings.Contains(banner, bannerSecret) {
		t.Fatalf("SEC-01: raw key written to a non-terminal stream:\n%s", banner)
	}
	if !strings.Contains(banner, "MCPPROXY_API_KEY") {
		t.Fatalf("banner must name the way out when the key cannot be stored, got:\n%s", banner)
	}
	if strings.Contains(renderObserved(t, logs), bannerSecret) {
		t.Fatal("SEC-01: raw API key reached the persistent log sink")
	}
}

// codex round 3 finding 1: the logged web_ui_url used to be BUILT with the raw
// key and then handed to a redactor. `listen` is operator-supplied, so one
// value url.Parse rejects sent the redactor down a regex fallback with no
// `apikey` rule and republished the key. It is now built pre-masked, so no
// string carrying the raw key is ever handed to the logger at all.
func TestGeneratedAPIKeyBanner_MalformedListenCannotLeakTheKey(t *testing.T) {
	for _, listen := range []string{
		"127.0.0.1:8080",
		"%zz",
		"127.0.0.1:8080/\x7f",
		"",
		":0",
		"user:pw@host:8080",
	} {
		core, logs := observer.New(zapcore.DebugLevel)
		announceGeneratedAPIKey(zap.New(core), io.Discard, false, generatedAPIKeyInfo{
			APIKey:     bannerSecret,
			Listen:     listen,
			Source:     "generated",
			ConfigPath: "/tmp/mcp_config.json",
		})
		if rendered := renderObserved(t, logs); strings.Contains(rendered, bannerSecret) {
			t.Fatalf("listen=%q put the raw key in the log sink:\n%s", listen, rendered)
		}
	}
}
