package telemetry

import (
	"go.etcd.io/bbolt"
)

// ActivationBucketName is the BBolt bucket that stores retention activation
// state (spec 044). Keys inside the bucket are fixed at compile time; missing
// keys default to their zero value.
const ActivationBucketName = "activation"

// Fixed keys inside the activation bucket. Values are encoded per
// data-model.md §ActivationBucket.
const (
	activationKeyFirstConnectedServerEver   = "first_connected_server_ever"
	activationKeyFirstMCPClientEver         = "first_mcp_client_ever"
	activationKeyFirstRetrieveToolsCallEver = "first_retrieve_tools_call_ever"
	activationKeyMCPClientsSeenEver         = "mcp_clients_seen_ever"
	activationKeyRetrieveToolsCalls24h      = "retrieve_tools_calls_24h"
	activationKeyEstimatedTokensSaved24h    = "estimated_tokens_saved_24h"
	activationKeyInstallerHeartbeatPending  = "installer_heartbeat_pending"
)

// MaxMCPClientsSeen bounds the cardinality of the mcp_clients_seen_ever list.
// 17th insertion is dropped.
const MaxMCPClientsSeen = 16

// ActivationState is the in-memory / on-the-wire representation of the
// activation bucket. Instances are loaded with ActivationStore.Load and saved
// with ActivationStore.Save.
type ActivationState struct {
	FirstConnectedServerEver      bool     `json:"first_connected_server_ever"`
	FirstMCPClientEver            bool     `json:"first_mcp_client_ever"`
	FirstRetrieveToolsCallEver    bool     `json:"first_retrieve_tools_call_ever"`
	MCPClientsSeenEver            []string `json:"mcp_clients_seen_ever"`
	RetrieveToolsCalls24h         int      `json:"retrieve_tools_calls_24h"`
	EstimatedTokensSaved24hBucket string   `json:"estimated_tokens_saved_24h_bucket"`
	ConfiguredIDECount            int      `json:"configured_ide_count"`
}

// ActivationStore is the persistence contract for the activation bucket.
// Implementations back onto a BBolt database; a fake is used in tests.
//
// All methods are safe for concurrent use by the caller's discretion — the
// BBolt implementation uses transactional updates so individual method calls
// are atomic. Callers that need multi-step atomicity should serialize through
// a single goroutine.
type ActivationStore interface {
	// Load reads the full activation state. Missing bucket or keys yield
	// zero values.
	Load(db *bbolt.DB) (ActivationState, error)

	// Save writes the full activation state, enforcing monotonic flags
	// (true cannot revert to false).
	Save(db *bbolt.DB, st ActivationState) error

	// MarkFirstConnectedServer sets first_connected_server_ever=true if not
	// already set. No-op if already true.
	MarkFirstConnectedServer(db *bbolt.DB) error

	// MarkFirstMCPClient sets first_mcp_client_ever=true if not already set.
	MarkFirstMCPClient(db *bbolt.DB) error

	// MarkFirstRetrieveToolsCall sets first_retrieve_tools_call_ever=true
	// if not already set.
	MarkFirstRetrieveToolsCall(db *bbolt.DB) error

	// RecordMCPClient adds sanitized client name to the seen list. Dedups
	// on insert; drops when cap (16) is reached. Callers should pre-sanitize
	// with sanitizeClientName.
	RecordMCPClient(db *bbolt.DB, name string) error

	// IncrementRetrieveToolsCall bumps the 24h window counter by 1,
	// rolling the window if it has expired.
	IncrementRetrieveToolsCall(db *bbolt.DB) error

	// AddTokensSaved adds n to the 24h token-savings estimator counter.
	AddTokensSaved(db *bbolt.DB, n int) error

	// SetInstallerPending writes the installer_heartbeat_pending flag.
	SetInstallerPending(db *bbolt.DB, v bool) error

	// IsInstallerPending reports whether installer_heartbeat_pending is
	// currently set.
	IsInstallerPending(db *bbolt.DB) (bool, error)
}
