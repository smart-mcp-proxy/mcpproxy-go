package transport

import (
	"os"
	"strings"
	"testing"
)

// The OAuth token store reaches config.db and, with Spec 123, the OAuth
// token encryption key. Logging the store value itself hands the whole
// object graph to the log encoder, so the transport logs only its type.
func TestTransportDoesNotLogTokenStoreValue(t *testing.T) {
	for _, name := range []string{"http.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), `zap.Any("token_store"`) {
			t.Errorf("%s logs the OAuth token store value; log its type instead", name)
		}
	}
}
