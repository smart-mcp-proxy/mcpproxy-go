package cliclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// captureCodeExecBody stands up a daemon stub that records the JSON body of the
// code execution request and answers with a trivial success.
func captureCodeExecBody(t *testing.T, body *map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/code/exec" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(body); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "result": nil})
	}))
}

// TestClient_CodeExec_SendsScriptName (Spec 097, T006) pins the stored-script
// wire contract: the CLI sends the script NAME and no source at all — the
// daemon's code_execution handler is the only execution-time resolver.
func TestClient_CodeExec_SendsScriptName(t *testing.T) {
	var body map[string]interface{}
	srv := captureCodeExecBody(t, &body)
	defer srv.Close()

	result, err := NewClient(srv.URL, nil).CodeExec(
		context.Background(), "", map[string]interface{}{}, 60000, 0, nil,
		CodeExecOptions{Script: "daily-report"},
	)
	if err != nil {
		t.Fatalf("CodeExec returned error: %v", err)
	}
	if !result.OK {
		t.Fatalf("CodeExec result = %+v, want OK", result)
	}

	if got := body["script"]; got != "daily-report" {
		t.Fatalf("request body script = %v, want %q", got, "daily-report")
	}
	if code, present := body["code"]; present && code != "" {
		t.Fatalf("a stored-script request must carry no inline code, got %q", code)
	}
}

// TestClient_CodeExec_LanguageForwardedVerbatim: the client is a dumb
// transport for `language`. Deciding whether the user meant it belongs to the
// CLI (which sends it only when --language was explicitly set); silently
// dropping an explicit "javascript" here would hide a contradiction with a
// stored .ts script instead of reporting it.
func TestClient_CodeExec_LanguageForwardedVerbatim(t *testing.T) {
	for _, language := range []string{"javascript", "typescript"} {
		t.Run(language, func(t *testing.T) {
			var body map[string]interface{}
			srv := captureCodeExecBody(t, &body)
			defer srv.Close()

			_, err := NewClient(srv.URL, nil).CodeExec(
				context.Background(), "({})", map[string]interface{}{}, 60000, 0, nil,
				CodeExecOptions{Language: language},
			)
			if err != nil {
				t.Fatalf("CodeExec returned error: %v", err)
			}
			if got := body["language"]; got != language {
				t.Fatalf("request body language = %v, want %q", got, language)
			}
		})
	}

	t.Run("unset language is not sent", func(t *testing.T) {
		var body map[string]interface{}
		srv := captureCodeExecBody(t, &body)
		defer srv.Close()

		_, err := NewClient(srv.URL, nil).CodeExec(
			context.Background(), "({})", map[string]interface{}{}, 60000, 0, nil,
		)
		if err != nil {
			t.Fatalf("CodeExec returned error: %v", err)
		}
		if _, present := body["language"]; present {
			t.Fatalf("request body must omit language when the caller set none: %v", body)
		}
	})
}

// TestClient_GetCodeScripts (Spec 097, T010) pins the daemon listing seam: the
// client unwraps the {success,data} envelope into the entries and the
// directory the daemon read them from.
func TestClient_GetCodeScripts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/code/scripts" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"dir": "/home/u/.mcpproxy/scripts",
				"scripts": []map[string]interface{}{
					{"name": "alpha", "paths": []string{"/home/u/.mcpproxy/scripts/alpha.js"}, "status": "ok"},
					{"name": "blank", "paths": []string{"/home/u/.mcpproxy/scripts/blank.js"}, "status": "invalid", "reason": "empty"},
				},
			},
		})
	}))
	defer srv.Close()

	dir, entries, err := NewClient(srv.URL, nil).GetCodeScripts(context.Background())
	if err != nil {
		t.Fatalf("GetCodeScripts returned error: %v", err)
	}
	if dir != "/home/u/.mcpproxy/scripts" {
		t.Fatalf("dir = %q", dir)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2", entries)
	}
	if entries[0].Name != "alpha" || string(entries[0].Status) != "ok" {
		t.Fatalf("entries[0] = %+v", entries[0])
	}
	if entries[1].Reason != "empty" {
		t.Fatalf("entries[1] = %+v, want reason empty", entries[1])
	}
}
