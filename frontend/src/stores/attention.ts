import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { AttentionItem, LoadingState } from '@/types'
import api from '@/services/api'

// Spec 109 FR-001/FR-003: the one needs-attention list, read verbatim from
// GET /api/v1/attention and kept live over SSE attention.changed. Every
// surface (Home list, header pill, sidebar Home badge) reads this store —
// none of them re-derives its own predicate (FR-003 removes
// Dashboard.vue's local `serversNeedingAttention`).
export const useAttentionStore = defineStore('attention', () => {
  const items = ref<AttentionItem[]>([])
  const loading = ref<LoadingState>({ loading: false, error: null })

  // True once the list has been fetched successfully at least once, so a
  // consumer can tell "no items" from "we don't know yet" (mirrors
  // servers.ts `loaded`).
  const loaded = ref(false)

  const count = computed(() => items.value.length)

  // Three independent, uncancelled callers issue a fetch: SidebarNav.vue and
  // TopHeader.vue each fetch onMounted, AttentionList.vue fetches onMounted
  // too, and `attention.changed` over SSE triggers a silent refetch. None of
  // them cancel the others, so a response already in flight when a newer one
  // lands must not be able to overwrite it — the same ticket guard
  // servers.ts uses for its own multi-writer list (see servers.ts
  // issueSeq/appliedSeq).
  let issueSeq = 0
  let appliedSeq = 0

  // A request settles when it produces an outcome — a list OR a failure, both
  // advance the mark, otherwise an older request's failure could still land
  // after a newer success and get accepted as if it were news.
  function claimTicket(ticket: number): boolean {
    if (ticket < appliedSeq) return false
    appliedSeq = ticket
    return true
  }

  async function fetchAttention(silent = false) {
    if (!silent) {
      loading.value = { loading: true, error: null }
    }
    const ticket = ++issueSeq
    try {
      const response = await api.getAttention()
      if (response.success && response.data) {
        if (!claimTicket(ticket)) return
        items.value = response.data.items ?? []
        loaded.value = true
        loading.value = { loading: false, error: null }
      } else {
        throw new Error(response.error || 'Failed to load attention list')
      }
    } catch (error) {
      console.error('Failed to fetch attention list:', error)
      if (!claimTicket(ticket)) return
      if (!silent) {
        loading.value = { loading: false, error: error instanceof Error ? error.message : 'Unknown error' }
      }
    }
  }

  function handleAttentionChanged(_event: Event) {
    // The SSE payload is already narrowed to {count, ids} per caller
    // (FR-006) — refetch the full item shape (summaries, fixes) rather than
    // reconstructing it from ids. Silent so a background change never flashes
    // a loading state over the list a user is reading.
    fetchAttention(true)
  }

  function setupEventListeners() {
    window.addEventListener('mcpproxy:attention-changed', handleAttentionChanged)
  }

  function cleanupEventListeners() {
    window.removeEventListener('mcpproxy:attention-changed', handleAttentionChanged)
  }

  setupEventListeners()

  return {
    // State
    items,
    loading,
    loaded,

    // Computed
    count,

    // Actions
    fetchAttention,
    cleanupEventListeners,
  }
})
