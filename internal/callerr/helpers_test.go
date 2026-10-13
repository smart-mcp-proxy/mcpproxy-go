package callerr

import (
	"context"
	"fmt"
	"net/http"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
	proxytransport "github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

func contextCanceled() error { return fmt.Errorf("w: %w", context.Canceled) }
func contextDeadline() error { return fmt.Errorf("w: %w", context.DeadlineExceeded) }
func sanitisationErr() error { return fmt.Errorf("w: %w", audit.ErrSanitisationFailed) }
func limitErr() error        { return &limiter.LimitError{Reason: limiter.ReasonQueueFull} }
func validationErr() error   { return &jsonschema.ValidationError{} }

// newRecordingClientForTest returns an http.Client using the production
// recorder round tripper.
func newRecordingClientForTest() *http.Client {
	return &http.Client{Transport: proxytransport.NewCallRecorderTransport(http.DefaultTransport)}
}
