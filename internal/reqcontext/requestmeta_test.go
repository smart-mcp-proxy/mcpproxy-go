package reqcontext

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRequestMetaRoundTrip(t *testing.T) {
	_, ok := GetRequestMeta(context.Background())
	assert.False(t, ok, "no meta on a bare context")

	ctx := WithRequestMeta(context.Background(), RequestMeta{ClientIP: "198.51.100.9", Mount: MountMCP})
	got, ok := GetRequestMeta(ctx)
	assert.True(t, ok)
	assert.Equal(t, RequestMeta{ClientIP: "198.51.100.9", Mount: MountMCP}, got)
}
