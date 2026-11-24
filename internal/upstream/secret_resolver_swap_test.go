package upstream

import (
	"sync"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
)

// A secret_file_dirs hot reload swaps the resolver while connects read it;
// under -race this fails if the swap is unsynchronised.
func TestManager_SetSecretResolverConcurrentWithReads(t *testing.T) {
	m := &Manager{secretResolver: secret.NewTestWildcardResolver()}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				m.SetSecretResolver(secret.NewTestWildcardResolver())
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if m.currentSecretResolver() == nil {
					t.Error("resolver is nil")
					return
				}
			}
		}()
	}
	wg.Wait()
}
