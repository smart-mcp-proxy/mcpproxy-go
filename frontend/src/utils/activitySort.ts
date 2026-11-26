// The Activity sort keys, in both directions (Spec 134 FR-046). The table has
// always sorted by these five; the calls view no longer shows two of them as
// columns, so the control lists them all.
export type ActivitySortColumn = 'timestamp' | 'type' | 'server_name' | 'status' | 'duration_ms'
export type ActivitySortDirection = 'asc' | 'desc'

export const ACTIVITY_SORT_OPTIONS: ReadonlyArray<{ value: string; label: string }> = [
  { value: 'timestamp:desc', label: 'Time, newest first' },
  { value: 'timestamp:asc', label: 'Time, oldest first' },
  { value: 'type:asc', label: 'Type, A to Z' },
  { value: 'type:desc', label: 'Type, Z to A' },
  { value: 'server_name:asc', label: 'Server, A to Z' },
  { value: 'server_name:desc', label: 'Server, Z to A' },
  { value: 'status:asc', label: 'Status, A to Z' },
  { value: 'status:desc', label: 'Status, Z to A' },
  { value: 'duration_ms:desc', label: 'Duration, longest first' },
  { value: 'duration_ms:asc', label: 'Duration, shortest first' },
]
