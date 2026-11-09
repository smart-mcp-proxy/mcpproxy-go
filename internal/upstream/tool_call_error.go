package upstream

import "fmt"

// genericToolCallError retains the manager's original diagnostic text while
// identifying only its generic enrichment branch. Other branches may carry
// genuine authentication/transport context and must not be stripped.
type genericToolCallError struct {
	server string
	tool   string
	cause  error
}

func (e *genericToolCallError) Error() string {
	return fmt.Sprintf("tool '%s' on server '%s' failed: %v. Check server configuration, authentication, and tool parameters", e.tool, e.server, e.cause)
}

func (e *genericToolCallError) Unwrap() error { return e.cause }

// GenericToolCallCause returns the cause only when err itself is the manager's
// trusted generic wrapper. It deliberately does not search an error chain:
// doing so could discard meaningful authentication or transport context.
func GenericToolCallCause(err error) (error, bool) {
	generic, ok := err.(*genericToolCallError)
	if !ok || generic == nil {
		return nil, false
	}
	return generic.cause, true
}
