package testutil

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPClient wraps http.Client with convenience methods for testing
type HTTPClient struct {
	client  *http.Client
	baseURL string
}

// NewHTTPClient creates a new HTTP client for testing
func NewHTTPClient(baseURL string) *HTTPClient {
	return &HTTPClient{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

// Get performs a GET request
func (c *HTTPClient) Get(path string) (*http.Response, error) {
	url := c.baseURL + path
	return c.client.Get(url)
}

// Post performs a POST request
func (c *HTTPClient) Post(path string, body io.Reader) (*http.Response, error) {
	url := c.baseURL + path
	return c.client.Post(url, "application/json", body)
}

// PostJSON performs a POST request with JSON data
func (c *HTTPClient) PostJSON(path string, data interface{}) (*http.Response, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return c.Post(path, strings.NewReader(string(jsonData)))
}

// GetJSON performs a GET request and parses JSON response
func (c *HTTPClient) GetJSON(path string, result interface{}) error {
	resp, err := c.Get(path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(result)
}

// PostJSONExpectStatus performs a POST request and checks the status code
func (c *HTTPClient) PostJSONExpectStatus(path string, data interface{}, expectedStatus int) (*http.Response, error) {
	resp, err := c.PostJSON(path, data)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != expectedStatus {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, fmt.Errorf("expected status %d, got %d: %s", expectedStatus, resp.StatusCode, string(body))
	}

	return resp, nil
}

// ParseJSONResponse parses a JSON response into the given interface
func ParseJSONResponse(resp *http.Response, result interface{}) error {
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(result)
}

// ParseJSONToString converts an interface to a JSON string
func ParseJSONToString(data interface{}) (string, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(jsonData), nil
}

// ReadResponseBody reads the entire response body as a string
func ReadResponseBody(resp *http.Response) (string, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// CheckJSONResponse checks if a response contains valid JSON
func CheckJSONResponse(resp *http.Response) error {
	defer resp.Body.Close()

	var result interface{}
	err := json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return fmt.Errorf("invalid JSON response: %w", err)
	}

	return nil
}

// APIResponse represents a standard API response
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// ParseAPIResponse parses a standard API response
func ParseAPIResponse(resp *http.Response) (*APIResponse, error) {
	var result APIResponse
	err := ParseJSONResponse(resp, &result)
	return &result, err
}

// ExpectAPISuccess checks that an API response indicates success
func ExpectAPISuccess(resp *http.Response) (*APIResponse, error) {
	apiResp, err := ParseAPIResponse(resp)
	if err != nil {
		return nil, err
	}

	if !apiResp.Success {
		return apiResp, fmt.Errorf("API request failed: %s", apiResp.Error)
	}

	return apiResp, nil
}

// SSEReader reads Server-Sent Events
type SSEReader struct {
	resp   *http.Response
	reader io.ReadCloser
}

// NewSSEReader creates a new SSE reader
func NewSSEReader(resp *http.Response) *SSEReader {
	return &SSEReader{
		resp:   resp,
		reader: resp.Body,
	}
}

// ReadEvent reads a single SSE event
func (r *SSEReader) ReadEvent(timeout time.Duration) (map[string]string, error) {
	// Simple SSE parsing for testing
	// In a real implementation, you'd want a more robust parser

	done := make(chan map[string]string, 1)
	errCh := make(chan error, 1)

	go func() {
		// Keep reading until we get an event with actual data
		for {
			event := make(map[string]string)
			buffer := make([]byte, 4096)

			n, err := r.reader.Read(buffer)
			if err != nil {
				errCh <- err
				return
			}

			data := string(buffer[:n])
			lines := strings.Split(data, "\n")

			for _, line := range lines {
				// Skip comment lines (starting with :)
				if strings.HasPrefix(line, ":") {
					continue
				}
				if strings.HasPrefix(line, "data: ") {
					event["data"] = strings.TrimPrefix(line, "data: ")
				} else if strings.HasPrefix(line, "event: ") {
					event["event"] = strings.TrimPrefix(line, "event: ")
				}
			}

			// If we found data, return this event
			if len(event) > 0 && event["data"] != "" {
				done <- event
				return
			}
			// Otherwise, keep reading for the next chunk
		}
	}()

	select {
	case event := <-done:
		return event, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout reading SSE event")
	}
}

// Close closes the SSE reader
func (r *SSEReader) Close() error {
	return r.reader.Close()
}
