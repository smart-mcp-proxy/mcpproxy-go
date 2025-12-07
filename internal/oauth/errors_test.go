package oauth

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrorTypes(t *testing.T) {
	// Verify error types are distinct sentinel errors
	tests := []struct {
		name string
		err  error
		msg  string
	}{
		{"ErrServerNotOAuth", ErrServerNotOAuth, "server does not use OAuth"},
		{"ErrTokenExpired", ErrTokenExpired, "OAuth token has expired"},
		{"ErrRefreshFailed", ErrRefreshFailed, "OAuth token refresh failed"},
		{"ErrNoRefreshToken", ErrNoRefreshToken, "no refresh token available"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.msg, tt.err.Error())
		})
	}
}

func TestErrorsIs(t *testing.T) {
	// Verify errors.Is works correctly with wrapped errors
	wrappedErr := errors.Join(errors.New("context"), ErrServerNotOAuth)
	assert.True(t, errors.Is(wrappedErr, ErrServerNotOAuth))
	assert.False(t, errors.Is(wrappedErr, ErrTokenExpired))
}

func TestErrorsAreDistinct(t *testing.T) {
	// Verify all errors are distinct from each other
	errs := []error{
		ErrServerNotOAuth,
		ErrTokenExpired,
		ErrRefreshFailed,
		ErrNoRefreshToken,
	}

	for i, err1 := range errs {
		for j, err2 := range errs {
			if i != j {
				assert.False(t, errors.Is(err1, err2), "%v should not match %v", err1, err2)
			}
		}
	}
}
