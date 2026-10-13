package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureStdoutOf(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	fn()
	require.NoError(t, w.Close())
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

// Spec 113-c FR-046: --error-class / --fault-domain reach the REST query.
func TestActivityFilter_ErrorClassAndFaultDomain(t *testing.T) {
	f := &ActivityFilter{ErrorClass: "http", FaultDomain: "upstream", Limit: 10}
	require.NoError(t, f.Validate())
	q := f.ToQueryParams()
	assert.Equal(t, "http", q.Get("error_class"))
	assert.Equal(t, "upstream", q.Get("fault_domain"))

	none := (&ActivityFilter{Limit: 10}).ToQueryParams()
	assert.False(t, none.Has("error_class"))
	assert.False(t, none.Has("fault_domain"))
}

func TestActivityFilter_ErrorClassValidation(t *testing.T) {
	for _, c := range validErrorClasses {
		assert.NoError(t, (&ActivityFilter{ErrorClass: c, Limit: 10}).Validate(), c)
	}
	for _, d := range validFaultDomains {
		assert.NoError(t, (&ActivityFilter{FaultDomain: d, Limit: 10}).Validate(), d)
	}
	err := (&ActivityFilter{ErrorClass: "bogus", Limit: 10}).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error-class")
	err = (&ActivityFilter{FaultDomain: "bogus", Limit: 10}).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fault-domain")
}

func TestActivityFlagsRegistered(t *testing.T) {
	for _, name := range []string{"error-class", "fault-domain"} {
		assert.NotNil(t, activityListCmd.Flags().Lookup(name), "list --%s", name)
		assert.NotNil(t, activityExportCmd.Flags().Lookup(name), "export --%s", name)
	}
}

// `activity show` adds an "Error class" line only when the record carries one.
func TestDisplayErrorClassLine(t *testing.T) {
	decode := func(js string) map[string]interface{} {
		var m map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(js), &m))
		return m
	}
	out := captureStdoutOf(t, func() {
		displayErrorClassLine(decode(`{"error_class":"http","fault_domain":"upstream","upstream_http_status":502}`))
	})
	assert.Equal(t, "Error class:  http (fault: upstream, HTTP 502)\n", out)

	out = captureStdoutOf(t, func() {
		displayErrorClassLine(decode(`{"error_class":"tool_error","fault_domain":"upstream"}`))
	})
	assert.Equal(t, "Error class:  tool_error (fault: upstream)\n", out)

	out = captureStdoutOf(t, func() {
		displayErrorClassLine(decode(`{"error_class":"cancelled"}`))
	})
	assert.Equal(t, "Error class:  cancelled\n", out)

	// Success and pre-113 records: nothing.
	assert.Empty(t, captureStdoutOf(t, func() { displayErrorClassLine(decode(`{"status":"success"}`)) }))
}
