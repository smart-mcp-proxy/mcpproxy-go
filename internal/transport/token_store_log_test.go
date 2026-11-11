package transport

import (
	"os"
	"strings"
	"testing"
)

// The OAuth token store reaches config.db and, with Spec 123, the OAuth
// token encryption key. Logging the store value itself hands the whole
// object graph to the log encoder, so the transport logs only whether one is
// set. Nothing derived from the store value (not even its type name) goes to
// a log call.
func TestTransportDoesNotLogTokenStoreValue(t *testing.T) {
	for _, name := range []string{"http.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(src), "\n") {
			if strings.Contains(line, "zap.") && strings.Contains(line, "TokenStore") &&
				!strings.Contains(line, "TokenStore != nil") {
				t.Errorf("%s passes the OAuth token store to a log field: %s", name, strings.TrimSpace(line))
			}
		}
	}
}
