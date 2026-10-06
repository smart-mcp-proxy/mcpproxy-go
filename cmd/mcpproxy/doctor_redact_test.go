package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	redactTestAdminKey = "SECRET-ADMIN-KEY-0123456789"
	redactTestToken    = "tok-SECRET-999"
)

func redactTestInputs() (diag, info map[string]interface{}) {
	diag = map[string]interface{}{
		"total_issues": 1,
		"upstream_errors": []interface{}{
			map[string]interface{}{
				"server":        "github",
				"error_message": "dial https://h.example/mcp?token=" + redactTestToken + "&x=1 failed",
			},
		},
	}
	info = map[string]interface{}{
		"version":    "v0.0.0",
		"web_ui_url": "http://127.0.0.1:8080/ui/?apikey=" + redactTestAdminKey,
	}
	return diag, info
}

func TestDoctorOutput_RedactsAdminKeyInEveryFormat(t *testing.T) {
	oldFmt := doctorOutput
	t.Cleanup(func() { doctorOutput = oldFmt })

	for _, format := range []string{"json", "yaml", "pretty"} {
		t.Run(format, func(t *testing.T) {
			doctorOutput = format
			diag, info := redactTestInputs()
			var runErr error
			stdout, stderr := captureStd(func() {
				runErr = outputDiagnostics(diag, info, nil, "", nil)
			})
			if runErr != nil {
				t.Fatalf("outputDiagnostics: %v", runErr)
			}
			all := stdout + stderr
			for _, secret := range []string{redactTestAdminKey, redactTestToken} {
				if strings.Contains(all, secret) {
					t.Fatalf("%s output leaks %q:\n%s", format, secret, all)
				}
			}
			if format == "pretty" {
				return
			}
			if strings.TrimSpace(stdout) == "" {
				t.Fatalf("%s output is empty", format)
			}
			if !strings.Contains(stdout, "web_ui_url") || !strings.Contains(stdout, "apikey=REDACTED") {
				t.Errorf("%s output must keep web_ui_url with apikey=REDACTED:\n%s", format, stdout)
			}
			if !strings.Contains(stdout, "x=1") {
				t.Errorf("%s output must keep non-secret query params:\n%s", format, stdout)
			}
			if format == "json" {
				var parsed map[string]interface{}
				if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
					t.Fatalf("json output does not parse: %v", err)
				}
			}
		})
	}
}

func TestDoctorOutput_ScrubsAdminKeyLiteral(t *testing.T) {
	oldFmt, oldLit := doctorOutput, doctorSecretLiterals
	t.Cleanup(func() { doctorOutput, doctorSecretLiterals = oldFmt, oldLit })
	doctorOutput = "json"
	doctorSecretLiterals = []string{"LITERAL-KEY-ABCDEFGH"}

	diag := map[string]interface{}{
		"total_issues": 1,
		"note":         "the key LITERAL-KEY-ABCDEFGH leaked outside a URL",
	}
	var runErr error
	stdout, _ := captureStd(func() {
		runErr = outputDiagnostics(diag, nil, nil, "", nil)
	})
	if runErr != nil {
		t.Fatalf("outputDiagnostics: %v", runErr)
	}
	if strings.Contains(stdout, "LITERAL-KEY-ABCDEFGH") {
		t.Fatalf("literal admin key leaked:\n%s", stdout)
	}
	if !strings.Contains(stdout, "REDACTED") {
		t.Fatalf("expected REDACTED marker:\n%s", stdout)
	}
}

func TestDoctorOutput_UnsupportedFormatErrors(t *testing.T) {
	oldFmt := doctorOutput
	t.Cleanup(func() { doctorOutput = oldFmt })
	doctorOutput = "xml"

	var runErr error
	captureStd(func() {
		runErr = outputDiagnostics(map[string]interface{}{"total_issues": 0}, nil, nil, "", nil)
	})
	if runErr == nil {
		t.Fatal("expected an error for an unsupported doctor output format")
	}
	for _, want := range []string{`"xml"`, "pretty", "json", "yaml"} {
		if !strings.Contains(runErr.Error(), want) {
			t.Errorf("error %q must mention %s", runErr.Error(), want)
		}
	}
}

func TestStatusOutput_MasksKeyInEveryFormat(t *testing.T) {
	const key = "test0123456789abcdefghijklmnopqrstuvwxyz6789"
	oldShow := statusShowKey
	t.Cleanup(func() { statusShowKey = oldShow })

	for _, format := range []string{"json", "yaml", "table"} {
		for _, show := range []bool{false, true} {
			name := format + "/masked"
			if show {
				name = format + "/show-key"
			}
			t.Run(name, func(t *testing.T) {
				info := &StatusInfo{
					State:      "running",
					ListenAddr: "127.0.0.1:8080",
					APIKey:     key,
					WebUIURL:   "http://127.0.0.1:8080/ui/?apikey=" + key,
					Endpoints:  map[string]string{},
				}
				statusShowKey = show
				if !show {
					maskStatusCredentials(info)
				}
				var runErr error
				stdout, _ := captureStd(func() { runErr = printStatusOutput(info, format) })
				if runErr != nil {
					t.Fatalf("printStatusOutput: %v", runErr)
				}
				if got := strings.Contains(stdout, key); got != show {
					t.Fatalf("key present=%v want %v:\n%s", got, show, stdout)
				}
			})
		}
	}
}
