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
