package runtime

// bytesPerTokenApprox is the rough bytes-per-token ratio used to convert a
// real observed average response SIZE (bytes; the usage aggregate never
// retains the response text itself, only its size — Spec 069 A2) into a
// token count. It matches the "bytes" token_source semantics
// UsageAggregateResponse already reports for the same reason. Good enough
// for "is this materially different from the synthetic simulation", not
// precise accounting.
const bytesPerTokenApprox = 4

// resolveAverageQueryResultSize decides what CalculateTokenSavings reports as
// AverageQueryResultSize and whether it is an estimate
// (contracts.ServerTokenMetrics.Estimated):
//
//   - haveReal is false (no completed retrieve_tools call has been recorded in
//     this runtime's usage aggregate yet): the synthetic per-topK simulation
//     (simulatedSize) is used, and the result is marked estimated.
//   - haveReal is true: realAvgBytes (the real average retrieve_tools response
//     size, ToolUsage.AvgRespBytes) is converted to a token count and used
//     instead, and the result is not an estimate.
func resolveAverageQueryResultSize(simulatedSize int, realAvgBytes int64, haveReal bool) (size int, estimated bool) {
	if !haveReal {
		return simulatedSize, true
	}
	real := int(realAvgBytes / bytesPerTokenApprox)
	if real <= 0 {
		real = 1
	}
	return real, false
}

// realRetrieveToolsAvgRespBytes reads the real observed average retrieve_tools
// response size from the runtime's usage aggregate (populated from actual
// completed calls via UsageAggregate.applyRetrieveToolsSizing — a dedicated
// counter, NOT the per-tool rollup, which deliberately excludes retrieve_tools
// as an internal built-in). ok is false before any retrieve_tools call has
// completed at all.
//
// A sized, non-truncated call gives an exact real average. When every
// observed call so far was truncated (a deployment whose responses routinely
// exceed tool_response_limit — the corrected review finding), there is no
// exact delivered size to average: the logged ResponseBytes on those records
// is the pre-truncation size, larger than what the agent received
// (truncatedBuiltinOverstatesDelivery). But contracts.ServerTokenMetrics.
// Estimated documents "false once at least one real retrieve_tools call has
// ... completed" — real calls plainly have — so falling back to the
// synthetic per-topK simulation forever would contradict that promise. The
// agent's response is cut to tool_response_limit characters
// (internal/server/content_forward.go), which is the best available
// conservative stand-in for the size actually delivered in that case.
func (r *Runtime) realRetrieveToolsAvgRespBytes() (avgBytes int64, ok bool) {
	snap := r.UsageSnapshot()
	if snap == nil {
		return 0, false
	}
	if avg, ok := snap.AvgRetrieveToolsRespBytes(); ok {
		return avg, true
	}
	if snap.HasObservedRetrieveToolsCall() && r.cfg != nil && r.cfg.ToolResponseLimit > 0 {
		return int64(r.cfg.ToolResponseLimit), true
	}
	return 0, false
}
