package runtime

import "time"

// Attention item kinds (Spec 109 FR-002). Kept as plain strings (not a typed
// enum) because they cross the wire verbatim in AttentionItem.Kind and in the
// CLI/Web/macOS fixtures generated from contracts/health-vocabulary.md's
// derivation table.
const (
	AttentionKindSignInRequired  = "sign_in_required"
	AttentionKindMissingSecret   = "missing_secret"
	AttentionKindConfigError     = "config_error"
	AttentionKindServerError     = "server_error"
	AttentionKindServerReview    = "server_review"
	AttentionKindToolReview      = "tool_review"
	AttentionKindClientNeverSeen = "client_never_seen"
)

// Attention ranks (contracts/rest-api.md#attention). Ascending: lower rank
// sorts first. tool_review has two ranks depending on which approval state
// drove the item (a server with both states yields two items).
const (
	AttentionRankSignInRequired    = 10
	AttentionRankMissingSecret     = 20
	AttentionRankConfigError       = 30
	AttentionRankServerError       = 40
	AttentionRankServerReview      = 50
	AttentionRankToolReviewChanged = 60
	AttentionRankToolReviewPending = 61
	AttentionRankClientNeverSeen   = 70
)

// Attention fix verbs. Most are the health.Action* verbs re-used verbatim
// (config_error and sign_in_required fixes run the same action the health
// vocabulary already names); review/reload_hint are attention-only.
const (
	AttentionFixLogin      = "login"
	AttentionFixSetSecret  = "set_secret"
	AttentionFixRestart    = "restart"
	AttentionFixReview     = "review"
	AttentionFixReloadHint = "reload_hint"
)

// Time-based thresholds (FR-002, research D2).
const (
	// AttentionServerErrorThreshold is how long a server must stay in
	// "error" or "connecting" (measured from AttentionServer.StateSince)
	// before it becomes a server_error item.
	AttentionServerErrorThreshold = 60 * time.Second
	// AttentionClientNeverSeenThreshold is how long a client must have been
	// connected (AttentionClient.ConnectedAt) with no session observed
	// since, before it becomes a client_never_seen item.
	AttentionClientNeverSeenThreshold = 5 * time.Minute
	// AttentionTimerCap bounds the threshold-recompute timer the subscriber
	// arms after every recompute (never longer than this, so a clock jump
	// cannot stall a time-based item past its threshold indefinitely).
	AttentionTimerCap = 30 * time.Second
)
