package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mcpproxy-go/internal/testutil"
)

func assertServerReady(t *testing.T, server *testutil.TestServer) {
	t.Helper()
	if server.ConnectionStatus != "" {
		assert.Equal(t, "Ready", server.ConnectionStatus)
	}
	assert.True(t, server.Connected, "expected server to report connected")
	assert.False(t, server.Connecting, "expected server not to be connecting")
	assert.Greater(t, server.ToolCount, 0, "expected server to have indexed tools")
}

// TestBinaryStartupAndShutdown tests basic binary startup and shutdown
func TestBinaryStartupAndShutdown(t *testing.T) {
	env := testutil.NewBinaryTestEnv(t)
	defer env.Cleanup()

	// Start the binary
	env.Start()

	// Verify server is responding
	client := testutil.NewHTTPClient(env.GetAPIURL())
	var response testutil.TestServerList
	err := client.GetJSON("/servers", &response)
	require.NoError(t, err)
	assert.True(t, response.Success)
}

// TestBinaryAPIEndpoints tests all REST API endpoints with the binary
func TestBinaryAPIEndpoints(t *testing.T) {
	env := testutil.NewBinaryTestEnv(t)
	defer env.Cleanup()

	env.Start()
	env.WaitForEverythingServer()

	client := testutil.NewHTTPClient(env.GetAPIURL())

	t.Run("GET /servers", func(t *testing.T) {
		var response testutil.TestServerList
		err := client.GetJSON("/servers", &response)
		require.NoError(t, err)
		assert.True(t, response.Success)
		assert.Len(t, response.Data.Servers, 1)
		assert.Equal(t, "memory", response.Data.Servers[0].Name)
		assertServerReady(t, &response.Data.Servers[0])
	})

	t.Run("GET /servers/memory/tools", func(t *testing.T) {
		var response testutil.TestToolList
		err := client.GetJSON("/servers/memory/tools", &response)
		require.NoError(t, err)
		assert.True(t, response.Success)
		assert.Equal(t, "memory", response.Data.Server)
		assert.Greater(t, len(response.Data.Tools), 0)

		// Check for some expected tools from memory server
		// Memory server has knowledge graph tools
		assert.Greater(t, len(response.Data.Tools), 0, "memory server should have tools")
	})

	t.Run("GET /index/search", func(t *testing.T) {
		var response testutil.TestSearchResults
		err := client.GetJSON("/index/search?q=create", &response)
		require.NoError(t, err)
		assert.True(t, response.Success)
		assert.Equal(t, "create", response.Data.Query)
		assert.Greater(t, len(response.Data.Results), 0)

		// Should find some tool from memory server
		found := false
		for _, result := range response.Data.Results {
			if result.Server == "memory" {
				found = true
				break
			}
		}
		assert.True(t, found, "Should find tools from memory server in search results")
	})

	t.Run("GET /index/search with limit", func(t *testing.T) {
		var response testutil.TestSearchResults
		err := client.GetJSON("/index/search?q=tool&limit=3", &response)
		require.NoError(t, err)
		assert.True(t, response.Success)
		assert.LessOrEqual(t, len(response.Data.Results), 3)
	})

	t.Run("GET /servers/memory/logs", func(t *testing.T) {
		var response struct {
			Success bool `json:"success"`
			Data    struct {
				Server string   `json:"server"`
				Logs   []string `json:"logs"`
				Tail   int      `json:"tail"`
			} `json:"data"`
		}
		err := client.GetJSON("/servers/memory/logs?tail=5", &response)
		require.NoError(t, err)
		assert.True(t, response.Success)
		assert.Equal(t, "memory", response.Data.Server)
		assert.Equal(t, 5, response.Data.Tail)
	})

	t.Run("POST /servers/memory/disable", func(t *testing.T) {
		resp, err := client.PostJSONExpectStatus("/servers/memory/disable", nil, http.StatusOK)
		require.NoError(t, err)

		var response struct {
			Success bool `json:"success"`
			Data    struct {
				Server  string `json:"server"`
				Enabled bool   `json:"enabled"`
			} `json:"data"`
		}
		err = testutil.ParseJSONResponse(resp, &response)
		require.NoError(t, err)
		assert.True(t, response.Success)
		assert.Equal(t, "memory", response.Data.Server)
		assert.False(t, response.Data.Enabled)
	})

	t.Run("POST /servers/memory/enable", func(t *testing.T) {
		resp, err := client.PostJSONExpectStatus("/servers/memory/enable", nil, http.StatusOK)
		require.NoError(t, err)

		var response struct {
			Success bool `json:"success"`
			Data    struct {
				Server  string `json:"server"`
				Enabled bool   `json:"enabled"`
			} `json:"data"`
		}
		err = testutil.ParseJSONResponse(resp, &response)
		require.NoError(t, err)
		assert.True(t, response.Success)
		assert.Equal(t, "memory", response.Data.Server)
		assert.True(t, response.Data.Enabled)
	})

	t.Run("POST /servers/memory/restart", func(t *testing.T) {
		resp, err := client.PostJSONExpectStatus("/servers/memory/restart", nil, http.StatusOK)
		require.NoError(t, err)

		var response struct {
			Success bool `json:"success"`
			Data    struct {
				Server    string `json:"server"`
				Restarted bool   `json:"restarted"`
			} `json:"data"`
		}
		err = testutil.ParseJSONResponse(resp, &response)
		require.NoError(t, err)
		assert.True(t, response.Success)
		assert.Equal(t, "memory", response.Data.Server)
		assert.True(t, response.Data.Restarted)

		// Wait for server to reconnect
		time.Sleep(3 * time.Second)
		env.WaitForEverythingServer()
	})
}

// TestBinaryErrorHandling tests error scenarios with the binary
func TestBinaryErrorHandling(t *testing.T) {
	env := testutil.NewBinaryTestEnv(t)
	defer env.Cleanup()

	env.Start()
	env.WaitForEverythingServer()

	client := testutil.NewHTTPClient(env.GetAPIURL())

	t.Run("GET /servers/nonexistent/tools", func(t *testing.T) {
		resp, err := client.Get("/servers/nonexistent/tools")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})

	t.Run("GET /index/search without query", func(t *testing.T) {
		resp, err := client.Get("/index/search")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("POST /servers/nonexistent/enable", func(t *testing.T) {
		resp, err := client.PostJSON("/servers/nonexistent/enable", nil)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})
}

// TestBinarySSEEvents tests Server-Sent Events with the binary
func TestBinarySSEEvents(t *testing.T) {
	env := testutil.NewBinaryTestEnv(t)
	defer env.Cleanup()

	env.Start()

	client := testutil.NewHTTPClient(env.GetBaseURL())
	resp, err := client.Get("/events")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"))

	// Read at least one SSE event
	sseReader := testutil.NewSSEReader(resp)
	event, err := sseReader.ReadEvent(5 * time.Second)
	require.NoError(t, err)
	assert.NotEmpty(t, event["data"])

	// Verify the event data is valid JSON
	var eventData map[string]interface{}
	err = json.Unmarshal([]byte(event["data"]), &eventData)
	require.NoError(t, err)
	assert.Contains(t, eventData, "running")
	assert.Contains(t, eventData, "timestamp")
}

// TestBinaryConcurrentRequests tests concurrent API requests with the binary
func TestBinaryConcurrentRequests(t *testing.T) {
	env := testutil.NewBinaryTestEnv(t)
	defer env.Cleanup()

	env.Start()
	env.WaitForEverythingServer()

	client := testutil.NewHTTPClient(env.GetAPIURL())

	// Make multiple concurrent requests
	done := make(chan bool, 5)
	errors := make(chan error, 5)

	for i := 0; i < 5; i++ {
		go func(_ int) {
			var response testutil.TestServerList
			err := client.GetJSON("/servers", &response)
			if err != nil {
				errors <- err
				return
			}

			if !response.Success {
				errors <- assert.AnError
				return
			}

			done <- true
		}(i)
	}

	// Wait for all requests to complete
	successCount := 0
	for i := 0; i < 5; i++ {
		select {
		case <-done:
			successCount++
		case err := <-errors:
			t.Errorf("Concurrent request failed: %v", err)
		case <-time.After(10 * time.Second):
			t.Fatal("Timeout waiting for concurrent requests")
		}
	}

	assert.Equal(t, 5, successCount, "All concurrent requests should succeed")
}

// TestBinaryPerformance tests basic performance metrics with the binary
func TestBinaryPerformance(t *testing.T) {
	env := testutil.NewBinaryTestEnv(t)
	defer env.Cleanup()

	env.Start()
	env.WaitForEverythingServer()

	client := testutil.NewHTTPClient(env.GetAPIURL())

	t.Run("Server list response time", func(t *testing.T) {
		start := time.Now()
		var response testutil.TestServerList
		err := client.GetJSON("/servers", &response)
		elapsed := time.Since(start)

		require.NoError(t, err)
		assert.True(t, response.Success)
		assert.Less(t, elapsed, 1*time.Second, "Server list should respond quickly")
	})

	t.Run("Tool search response time", func(t *testing.T) {
		start := time.Now()
		var response testutil.TestSearchResults
		err := client.GetJSON("/index/search?q=echo", &response)
		elapsed := time.Since(start)

		require.NoError(t, err)
		assert.True(t, response.Success)
		assert.Less(t, elapsed, 2*time.Second, "Tool search should respond quickly")
	})

	t.Run("Multiple rapid requests", func(t *testing.T) {
		start := time.Now()
		for i := 0; i < 10; i++ {
			var response testutil.TestServerList
			err := client.GetJSON("/servers", &response)
			require.NoError(t, err)
			assert.True(t, response.Success)
		}
		elapsed := time.Since(start)

		assert.Less(t, elapsed, 5*time.Second, "10 rapid requests should complete quickly")
	})
}

// TestBinaryHealthAndRecovery tests health checks and recovery scenarios
// Note: This test is skipped in CI due to flakiness with slow server startup/restart times
// in CI environments. It can take over 60 seconds to complete and may timeout.
func TestBinaryHealthAndRecovery(t *testing.T) {
	env := testutil.NewBinaryTestEnv(t)
	defer env.Cleanup()

	env.Start()
	env.WaitForEverythingServer()

	client := testutil.NewHTTPClient(env.GetAPIURL())

	t.Run("Server restart and recovery", func(t *testing.T) {
		// Restart the memory server
		resp, err := client.PostJSONExpectStatus("/servers/memory/restart", nil, http.StatusOK)
		require.NoError(t, err)
		resp.Body.Close()

		// Wait for server to reconnect
		time.Sleep(3 * time.Second)
		env.WaitForEverythingServer()

		// Verify server is working after restart
		var response testutil.TestServerList
		err = client.GetJSON("/servers", &response)
		require.NoError(t, err)
		assert.True(t, response.Success)
		assert.Len(t, response.Data.Servers, 1)
		assertServerReady(t, &response.Data.Servers[0])
	})

	t.Run("Disable and re-enable server", func(t *testing.T) {
		// Disable server
		resp, err := client.PostJSONExpectStatus("/servers/memory/disable", nil, http.StatusOK)
		require.NoError(t, err)
		resp.Body.Close()

		// Verify server is disabled
		var response testutil.TestServerList
		err = client.GetJSON("/servers", &response)
		require.NoError(t, err)
		assert.True(t, response.Success)
		assert.False(t, response.Data.Servers[0].Enabled)

		// Re-enable server
		resp, err = client.PostJSONExpectStatus("/servers/memory/enable", nil, http.StatusOK)
		require.NoError(t, err)
		resp.Body.Close()

		// Wait for server to reconnect
		time.Sleep(2 * time.Second)
		env.WaitForEverythingServer()

		// Verify server is working again
		err = client.GetJSON("/servers", &response)
		require.NoError(t, err)
		assert.True(t, response.Success)
		assert.True(t, response.Data.Servers[0].Enabled)
		assertServerReady(t, &response.Data.Servers[0])
	})
}
