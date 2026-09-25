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
// completed calls, internal/runtime/activity_service.go). ok is false before
// any sized retrieve_tools call has completed.
func (r *Runtime) realRetrieveToolsAvgRespBytes() (avgBytes int64, ok bool) {
	snap := r.UsageSnapshot()
	if snap == nil {
		return 0, false
	}
	tu, exists := snap.Tools[toolKey("", "retrieve_tools")]
	if !exists || tu == nil {
		return 0, false
	}
	return tu.AvgRespBytes()
}
