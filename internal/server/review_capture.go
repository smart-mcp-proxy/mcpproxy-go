package server

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// reviewCaptureTimeout bounds one automatic definition capture. The capture
// connects the quarantined upstream under a bounded inspection exemption and
// lists its tools, so it must never be allowed to run unbounded.
const reviewCaptureTimeout = 2 * time.Minute

// maybeCaptureReviewDefinitions captures a freshly scanned, still-quarantined
// server's tool definitions for review (Spec 109 fix-review-screen, D-4), so
// the review list is not empty until someone clicks "Fetch tool definitions".
//
// It runs from the scan-settled event, after maybeAutoApproveScanSettled has
// had its chance to unquarantine the server. Eligibility is decided by
// runtime.ShouldCaptureReviewDefinitionsAfterScan: still quarantined, no
// approval records yet, and a completed baseline scan that already exported
// tools (so the upstream was started and listed automatically; this adds no new
// trust exposure). The capture itself is the existing inspection-only
// RefreshServerTools path (no indexing, no tool routing).
//
// It never blocks the event loop: the capture runs on its own goroutine with a
// bounded context, and a per-server single-flight guard collapses duplicate
// settle events into one capture. A failure is logged and left for the manual
// "Fetch tool definitions" action.
func (s *Server) maybeCaptureReviewDefinitions(serverName, status string) {
	if serverName == "" || status != "completed" || s.runtime == nil {
		return
	}
	if !s.runtime.ShouldCaptureReviewDefinitionsAfterScan(serverName) {
		return
	}
	if _, inFlight := s.reviewCaptureInFlight.LoadOrStore(serverName, struct{}{}); inFlight {
		return
	}
	capture := s.reviewCaptureFn
	if capture == nil {
		capture = s.runtime.RefreshServerTools
	}
	go func() {
		defer s.reviewCaptureInFlight.Delete(serverName)
		ctx, cancel := context.WithTimeout(context.Background(), reviewCaptureTimeout)
		defer cancel()
		if err := capture(ctx, serverName); err != nil {
			s.logger.Info("automatic tool definition capture for review did not complete; use Fetch tool definitions",
				zap.String("server", serverName), zap.Error(err))
		}
	}()
}
