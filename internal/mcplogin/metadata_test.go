package mcplogin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func golden(t *testing.T, name string, v any) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ") // maps marshal with sorted keys
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if string(got)+"\n" != string(want) {
		t.Fatalf("%s mismatch:\n%s\nwant:\n%s", name, got, want)
	}
}

func TestProtectedResourceMetadataGolden(t *testing.T) {
	golden(t, "protected_resource_client.json", ProtectedResourceMetadata("http://127.0.0.1:8080", "cursor"))
	golden(t, "protected_resource_generic.json", ProtectedResourceMetadata("http://127.0.0.1:8080", ""))
}

func TestAuthorizationServerMetadataGolden(t *testing.T) {
	golden(t, "authorization_server.json", AuthorizationServerMetadata("http://127.0.0.1:8080"))
}

func TestChallenge(t *testing.T) {
	if got := Challenge("http://127.0.0.1:8080", "cursor"); got !=
		`Bearer resource_metadata="http://127.0.0.1:8080/.well-known/oauth-protected-resource/mcp/oauth/cursor"` {
		t.Fatal(got)
	}
	if got := Challenge("http://127.0.0.1:8080", ""); got !=
		`Bearer resource_metadata="http://127.0.0.1:8080/.well-known/oauth-protected-resource/mcp/oauth"` {
		t.Fatal(got)
	}
}
