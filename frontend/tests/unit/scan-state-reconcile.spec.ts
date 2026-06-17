import { describe, it, expect } from 'vitest'
import { isTerminalScanStatus, decideScanReconcile } from '@/utils/scanState'

// MCP-2740: A finished security scan stayed stuck on "Scanning… 5/5 complete"
// with the Report button disabled because terminal state was only derived from a
// live poll tick whose job id matched `activeScanJobId`. A fast scan can finalize
// (and be replaced by a Pass-2 job, or just have its id drift) before the first
// 2s tick, so the matched-job branch never fires and `scanLoading` stays true
// forever. The reconciliation below derives terminal state from the authoritative
// backend status on EVERY status fetch, regardless of job-id / scan_pass bookkeeping.

describe('isTerminalScanStatus (MCP-2740)', () => {
  it('treats every backend terminal status as terminal', () => {
    for (const s of ['completed', 'complete', 'failed', 'error', 'cancelled', 'canceled']) {
      expect(isTerminalScanStatus(s)).toBe(true)
    }
  })

  it('treats live/unknown statuses as non-terminal', () => {
    for (const s of ['running', 'pending', '', undefined, null]) {
      expect(isTerminalScanStatus(s as any)).toBe(false)
    }
  })
})

describe('decideScanReconcile (MCP-2740)', () => {
  const loadingState = { scanLoading: true, activeScanJobId: 'job-A' }

  it('finalizes a completed job whose id matches the active job', () => {
    const d = decideScanReconcile({ status: 'completed', jobId: 'job-A', scanPass: 1 }, loadingState)
    expect(d.finalize).toBe(true)
    expect(d.isError).toBe(false)
    expect(d.resumePolling).toBe(false)
  })

  it('finalizes a completed job whose id DIFFERS from the active job (the stuck-UI bug)', () => {
    // Previously: jobId !== activeScanJobId && scan_pass !== 2 → early return, never finalized.
    const d = decideScanReconcile({ status: 'completed', jobId: 'job-B', scanPass: 1 }, loadingState)
    expect(d.finalize).toBe(true)
    expect(d.isError).toBe(false)
  })

  it('finalizes a completed job even when scan_pass is absent', () => {
    const d = decideScanReconcile({ status: 'complete', jobId: 'job-B' }, loadingState)
    expect(d.finalize).toBe(true)
  })

  it('finalizes and flags an error for failed/error status', () => {
    expect(decideScanReconcile({ status: 'failed', jobId: 'job-A' }, loadingState)).toMatchObject({
      finalize: true,
      isError: true,
    })
    expect(decideScanReconcile({ status: 'error', jobId: 'job-A' }, loadingState)).toMatchObject({
      finalize: true,
      isError: true,
    })
  })

  it('finalizes a cancelled job', () => {
    expect(decideScanReconcile({ status: 'cancelled', jobId: 'job-A' }, loadingState).finalize).toBe(true)
  })

  it('finalizes when a newer Pass-2 job is running (Pass 1 is done)', () => {
    const d = decideScanReconcile({ status: 'running', jobId: 'job-B', scanPass: 2 }, loadingState)
    expect(d.finalize).toBe(true)
    expect(d.isError).toBe(false)
  })

  it('keeps polling for a still-running active job', () => {
    const d = decideScanReconcile({ status: 'running', jobId: 'job-A', scanPass: 1 }, loadingState)
    expect(d.finalize).toBe(false)
    expect(d.resumePolling).toBe(true)
  })

  it('keeps polling for a pending job', () => {
    const d = decideScanReconcile({ status: 'pending', jobId: 'job-A' }, loadingState)
    expect(d.finalize).toBe(false)
    expect(d.resumePolling).toBe(true)
  })

  // Codex P2 (PR #698): after Pass-1 finalizes, loadScanReport(true) is called to
  // refresh the report. At that point scanLoading=false and activeScanJobId=null.
  // If the backend is already running Pass-2, decideScanReconcile correctly returns
  // resumePolling=true — but the caller must pass skipPolling=true so it does NOT
  // re-enable scanLoading and hide the just-completed Pass-1 report.
  // This test documents that invariant: the cleared state + running Pass-2 → resumePolling.
  it('returns resumePolling for a running Pass-2 seen after scan state was cleared (post-finalize)', () => {
    const clearedState = { scanLoading: false, activeScanJobId: null }
    const d = decideScanReconcile({ status: 'running', jobId: 'job-B', scanPass: 2 }, clearedState)
    expect(d.finalize).toBe(false)
    expect(d.resumePolling).toBe(true)
    // Callers that invoke loadScanReport after clearScanRunState MUST pass
    // skipPolling=true to suppress this branch and preserve the Pass-1 report.
  })
})
