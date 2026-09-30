package transport

import (
	"encoding/json"
	"errors"
)

// ScrubbedCopy returns a copy of e with every text field passed through scrub
// (Spec 112 FR-016: forwarded header values an upstream echoes back must not
// reach a sink through errors.As). The receiver is not modified.
func (e *HTTPError) ScrubbedCopy(scrub func(string) string) error {
	if e == nil {
		return e
	}
	c := *e
	c.Body = scrub(e.Body)
	c.URL = scrub(e.URL)
	if e.Headers != nil {
		c.Headers = make(map[string]string, len(e.Headers))
		for k, v := range e.Headers {
			c.Headers[k] = scrub(v)
		}
	}
	if e.Err != nil {
		c.Err = errors.New(scrub(e.Err.Error()))
	}
	return &c
}

// ScrubbedCopy returns a copy of e with Message, Data and the nested HTTP
// error scrubbed. Data is scrubbed through its JSON form; if that fails it is
// dropped rather than kept raw. The receiver is not modified.
func (e *JSONRPCError) ScrubbedCopy(scrub func(string) string) error {
	if e == nil {
		return e
	}
	c := *e
	c.Message = scrub(e.Message)
	if e.Data != nil {
		c.Data = nil
		if b, err := json.Marshal(e.Data); err == nil {
			var d interface{}
			if json.Unmarshal([]byte(scrub(string(b))), &d) == nil {
				c.Data = d
			}
		}
	}
	if e.HTTPError != nil {
		if h, ok := e.HTTPError.ScrubbedCopy(scrub).(*HTTPError); ok {
			c.HTTPError = h
		}
	}
	return &c
}
