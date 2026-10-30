package experiments

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedTransport answers each request with the next scripted response.
type scriptedTransport struct {
	responses []func(*http.Request) (*http.Response, error)
	calls     int
}

func (s *scriptedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	i := s.calls
	s.calls++
	if i >= len(s.responses) {
		return nil, errors.New("unexpected request")
	}
	return s.responses[i](r)
}

func npmStatus(code int, body string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: code,
			Status:     http.StatusText(code),
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    r,
		}, nil
	}
}

const npmFoundBody = `{"name":"@acme/tool","description":"d","dist-tags":{"latest":"1.0.0"}}`

// A failed lookup (timeout, network error, 5xx) says nothing about whether the
// package exists, so it must not be cached for the 2h TTL: a registry refresh
// does not clear the guess cache, and the npm install hint would stay missing.
func TestCheckNPMPackage_TransientFailureNotCached(t *testing.T) {
	cases := map[string]func(*http.Request) (*http.Response, error){
		"network error": func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp: i/o timeout")
		},
		"server error": npmStatus(http.StatusServiceUnavailable, ""),
		"rate limited": npmStatus(http.StatusTooManyRequests, ""),
	}
	for name, failure := range cases {
		t.Run(name, func(t *testing.T) {
			guesser, db := setupTestGuesser(t)
			defer db.Close()

			rt := &scriptedTransport{responses: []func(*http.Request) (*http.Response, error){
				failure,
				npmStatus(http.StatusOK, npmFoundBody),
			}}
			client := &http.Client{Transport: rt}

			first := guesser.checkNPMPackageWithClient(context.Background(), "@acme/tool", client)
			require.False(t, first.Exists)

			second := guesser.checkNPMPackageWithClient(context.Background(), "@acme/tool", client)
			assert.Equal(t, 2, rt.calls, "the retry must reach the registry, not the cached failure")
			assert.True(t, second.Exists)
			assert.Equal(t, "1.0.0", second.Version)
		})
	}
}

func TestCheckNPMPackage_DefinitiveAnswersCached(t *testing.T) {
	for name, answer := range map[string]func(*http.Request) (*http.Response, error){
		"found":     npmStatus(http.StatusOK, npmFoundBody),
		"not found": npmStatus(http.StatusNotFound, ""),
	} {
		t.Run(name, func(t *testing.T) {
			guesser, db := setupTestGuesser(t)
			defer db.Close()

			rt := &scriptedTransport{responses: []func(*http.Request) (*http.Response, error){answer}}
			client := &http.Client{Transport: rt}

			first := guesser.checkNPMPackageWithClient(context.Background(), "@acme/tool", client)
			second := guesser.checkNPMPackageWithClient(context.Background(), "@acme/tool", client)
			assert.Equal(t, 1, rt.calls)
			assert.Equal(t, first.Exists, second.Exists)
		})
	}
}
