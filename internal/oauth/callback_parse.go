package oauth

import (
	"errors"
	"fmt"
	"net/url"
)

// ErrInvalidCallback is wrapped by every ParseCallbackQuery rejection.
var ErrInvalidCallback = errors.New("invalid OAuth callback")

// CallbackResult is what a flow waiting on a state receives: the full
// authorization response query, already accepted by ParseCallbackQuery.
type CallbackResult struct {
	Query url.Values
}

// Get returns the first value of key ("" when absent).
func (r CallbackResult) Get(key string) string { return r.Query.Get(key) }

// ParseCallbackQuery is the one strict parse of an authorization response
// query (Spec 135 FR-001). Every callback surface runs it first, before any
// waiter is taken, exchange is attempted, provider error is reported or page
// is chosen. It rejects:
//   - a query that does not parse (bad escapes, ";" separators);
//   - a repeated code, state, iss or error;
//   - code and error together, or neither of them;
//   - a present but empty code or error;
//   - a missing or empty state.
//
// A present but empty iss is NOT rejected here: it reaches the issuer
// decision intact (len(q["iss"]) == 1), which treats it as a mismatch. The
// returned values are the full multi-valued query.
func ParseCallbackQuery(rawQuery string) (url.Values, error) {
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed query", ErrInvalidCallback)
	}
	for _, key := range []string{"code", "state", "iss", "error"} {
		if len(q[key]) > 1 {
			return nil, fmt.Errorf("%w: repeated %s parameter", ErrInvalidCallback, key)
		}
	}
	_, hasCode := q["code"]
	_, hasError := q["error"]
	switch {
	case hasCode && hasError:
		return nil, fmt.Errorf("%w: both code and error are present", ErrInvalidCallback)
	case !hasCode && !hasError:
		return nil, fmt.Errorf("%w: neither code nor error is present", ErrInvalidCallback)
	case hasCode && q.Get("code") == "":
		return nil, fmt.Errorf("%w: empty code parameter", ErrInvalidCallback)
	case hasError && q.Get("error") == "":
		return nil, fmt.Errorf("%w: empty error parameter", ErrInvalidCallback)
	}
	if q.Get("state") == "" {
		return nil, fmt.Errorf("%w: missing or empty state parameter", ErrInvalidCallback)
	}
	return q, nil
}
