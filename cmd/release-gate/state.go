package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// issuedCall records one correlated tool call made during the matrix run.
// Each call embeds a unique nonce in its arguments; FR-011 later proves the
// call landed in the activity log and that its recorded request id resolves
// via GET /api/v1/activity?request_id=.
type issuedCall struct {
	Cell            string `json:"cell"`
	Via             string `json:"via"` // "mcp" | "rest"
	Tool            string `json:"tool"`
	Nonce           string `json:"nonce"`
	HeaderRequestID string `json:"header_request_id,omitempty"`
}

// counterSnapshot captures the FR-012 counters at a point in time.
type counterSnapshot struct {
	TakenAt time.Time `json:"taken_at"`

	TokensToolListSize int   `json:"tokens_tool_list_size"`
	TokensSaved        int   `json:"tokens_saved"`
	UsageCalls         int64 `json:"usage_calls"`

	TelemetryAvailable bool  `json:"telemetry_available"`
	TelemetryBuiltin   int64 `json:"telemetry_builtin_tool_calls"`
}

// stateFixture is a driver-owned fixture process recorded for the attach
// phase (teardown + oauth restart).
type stateFixture struct {
	Name   string   `json:"name"`
	PID    int      `json:"pid"`
	Port   int      `json:"port"`
	Binary string   `json:"binary"`
	Args   []string `json:"args"`
}

// gateState is written by `release-gate matrix --state-file` so that
// `release-gate invariants` can attach to the SAME live core instance the
// matrix traffic ran against (FR-011/FR-012 are assertions over that
// traffic).
type gateState struct {
	BaseURL           string           `json:"base_url"`
	APIKey            string           `json:"api_key"`
	CorePID           int              `json:"core_pid"`
	CoreBinary        string           `json:"core_binary"`
	FixtureBinary     string           `json:"fixture_binary"`
	OAuthBinary       string           `json:"oauth_binary,omitempty"`
	DataDir           string           `json:"data_dir"`
	ConfigPath        string           `json:"config_path"`
	WorkDir           string           `json:"work_dir"`
	Cells             []string         `json:"cells"`
	Fixtures          []stateFixture   `json:"fixtures"`
	StdioKillPatterns []string         `json:"stdio_kill_patterns"`
	DockerNamePrefix  string           `json:"docker_name_prefix,omitempty"`
	IssuedCalls       []issuedCall     `json:"issued_calls"`
	Before            *counterSnapshot `json:"before"`
}

func writeState(path string, st *gateState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal gate state: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func readState(path string) (*gateState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read gate state: %w", err)
	}
	var st gateState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse gate state: %w", err)
	}
	return &st, nil
}
