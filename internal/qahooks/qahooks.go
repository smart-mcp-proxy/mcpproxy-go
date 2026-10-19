// Package qahooks holds live-QA park points (Spec 116 PR3, quickstart L3).
// Every hook is nil unless the binary is built with -tags qa_hooks, so a
// release build carries no park point at all.
package qahooks

// ParkWorker, when set, blocks a post-commit connect or disconnect worker for
// server until the operator releases it.
var ParkWorker func(server string)

// ParkSupervisorDisconnect, when set, blocks a supervisor ActionDisconnect for
// server until the operator releases it.
var ParkSupervisorDisconnect func(server string)

// ParkDial, when set, blocks an upstream instance's connect for server after
// the caller's last registry lookup and before it launches or dials anything
// (Spec 116 PR4, quickstart L4).
var ParkDial func(server string)

// ParkOAuth, when set, blocks an upstream instance's OAuth token refresh for
// server before the refresh request is sent (Spec 116 PR4, quickstart L4).
var ParkOAuth func(server string)

// ParkLogin, when set, blocks a manual OAuth login (StartManualOAuthQuick,
// StartManualOAuthWithInfo) for server after it bound the login to the
// instance's gate and before it sends anything (Spec 116 PR4 review round 3).
var ParkLogin func(server string)

// ParkOAuthPersist, when set, blocks an upstream instance's OAuth token save
// (a login's code exchange or a refresh) for server after the network
// response and before the result is persisted (Spec 116 PR2b, #1607).
var ParkOAuthPersist func(server string)
