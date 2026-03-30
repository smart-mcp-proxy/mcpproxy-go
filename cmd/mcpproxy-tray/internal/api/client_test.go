package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestClientGetServers_LogsZeroServersOnlyOnStateChange(t *testing.T) {
	t.Parallel()

	responses := []string{
		`{"success":true,"data":{"servers":[]}}`,
		`{"success":true,"data":{"servers":[]}}`,
		`{"success":true,"data":{"servers":[{"name":"demo","connected":true,"enabled":true,"quarantined":false,"tool_count":3,"status":"connected"}]}}`,
		`{"success":true,"data":{"servers":[{"name":"demo","connected":true,"enabled":true,"quarantined":false,"tool_count":3,"status":"connected"}]}}`,
		`{"success":true,"data":{"servers":[]}}`,
	}

	var (
		mu    sync.Mutex
		index int
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/servers", r.URL.Path)

		mu.Lock()
		current := responses[index]
		if index < len(responses)-1 {
			index++
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(current))
		if err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()

	core, recorded := observer.New(zapcore.DebugLevel)
	client := &Client{
		baseURL:    server.URL,
		httpClient: server.Client(),
		logger:     zap.New(core).Sugar(),
	}

	expectedCounts := []int{0, 0, 1, 1, 0}
	for _, expectedCount := range expectedCounts {
		servers, err := client.GetServers()
		require.NoError(t, err)
		assert.Len(t, servers, expectedCount)
	}

	assert.Equal(t, 2, recorded.FilterMessage("API returned zero upstream servers").Len())
	assert.Equal(t, 1, recorded.FilterMessage("Server state changed").Len())
}
