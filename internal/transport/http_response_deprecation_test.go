package transport

import (
	"errors"
	"testing"
)

func TestNonGoneResponseBodyCannotDeprecateEndpoint(t *testing.T) {
	for _, message := range []string{
		"request failed with status 500: MySQL server has gone away",
		"request failed with status 404: tool removed",
		"request failed with status 401: deprecated credentials",
		"request failed with status 500: order 410 not found",
	} {
		if LooksLikeDeprecatedEndpoint(errors.New(message)) {
			t.Errorf("classified response as endpoint deprecation: %s", message)
		}
	}
}
