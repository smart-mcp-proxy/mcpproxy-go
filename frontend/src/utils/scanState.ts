// Security-scan UI state reconciliation (MCP-2740).
//
// The Server Detail → Security tab previously cleared its "Scanning…" spinner
// (`scanLoading`) only inside a live poll tick whose job id matched the tracked
// `activeScanJobId`. A scan can finish in <2s — finalizing (and being replaced by
// a Pass-2 job) before the first 2s tick — so the matched-job branch never fired
// and the UI stayed stuck on "Scanning… N/N complete" with the Report button
// disabled indefinitely. The backend job, meanwhile, was already `completed`.
//
// The fix derives terminal state from the authoritative backend status on EVERY
// status fetch, independent of `activeScanJobId` / `scan_pass` bookkeeping. This
// module holds that decision as a pure, unit-testable reducer.

export const TERMINAL_SCAN_STATUSES = [
  'completed',
  'complete',
  'failed',
  'error',
  'cancelled',
  'canceled',
] as const

/** True when a backend scan status means the job is finished (no longer running). */
export function isTerminalScanStatus(status?: string | null): boolean {
  return !!status && (TERMINAL_SCAN_STATUSES as readonly string[]).includes(status)
}

export interface ScanReconcileInput {
  /** Backend job status (e.g. "running", "completed", "failed"). */
  status?: string | null
  /** Backend job id for the polled status. */
  jobId?: string | null
  /** Scan pass number (1 or 2); may be absent on older payloads. */
  scanPass?: number | null
}

export interface ScanReconcileState {
  scanLoading: boolean
  activeScanJobId: string | null
}

export interface ScanReconcileDecision {
  /** Finalize the UI: stop polling, clear the spinner, surface the report. */
  finalize: boolean
  /** The finalize is due to a failed/errored job (set error, skip success toast). */
  isError: boolean
  /** The job is still running/pending and polling should (continue to) run. */
  resumePolling: boolean
}

/**
 * Decide how the UI should reconcile against an authoritative backend scan status.
 *
 * Pure and idempotent: a terminal status always finalizes, regardless of whether
 * the polled job id matches the tracked active job or which scan pass it is. A
 * newer Pass-2 job that is merely running also finalizes, because that means the
 * tracked Pass-1 work is done.
 */
export function decideScanReconcile(
  input: ScanReconcileInput,
  state: ScanReconcileState,
): ScanReconcileDecision {
  const status = input.status ?? null

  // Authoritative terminal status — finalize regardless of job-id / scan_pass.
  if (isTerminalScanStatus(status)) {
    return {
      finalize: true,
      isError: status === 'failed' || status === 'error',
      resumePolling: false,
    }
  }

  // A different, newer Pass-2 job is running → the tracked Pass-1 job is done.
  if (
    state.activeScanJobId &&
    input.jobId &&
    input.jobId !== state.activeScanJobId &&
    input.scanPass === 2
  ) {
    return { finalize: true, isError: false, resumePolling: false }
  }

  // Still in flight.
  return {
    finalize: false,
    isError: false,
    resumePolling: status === 'running' || status === 'pending',
  }
}
