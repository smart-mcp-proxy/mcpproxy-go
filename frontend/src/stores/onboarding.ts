import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { OnboardingStateResponse, OnboardingMarkRequest } from '@/types'
import api from '@/services/api'

/**
 * Adaptive onboarding wizard store (Spec 046).
 *
 * Holds the live wizard state (predicates + persisted engagement record)
 * fetched from `/api/v1/onboarding/state`, plus UI flags driving the
 * wizard's visibility.
 *
 * Usage from Dashboard.vue:
 *   const onboarding = useOnboardingStore()
 *   await onboarding.fetchState()
 *   if (onboarding.shouldShowWizard) {
 *     onboarding.openWizard()
 *   }
 */
export const useOnboardingStore = defineStore('onboarding', () => {
  // State fetched from backend
  const state = ref<OnboardingStateResponse | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)

  // UI: whether the wizard modal is currently open. Independent of the
  // server-side ShouldShowWizard predicate so the user can manually re-open
  // the wizard via the "Run setup wizard" link even when both predicates
  // are satisfied.
  const wizardOpen = ref(false)

  // Computed
  const shouldShowWizard = computed(() => state.value?.should_show_wizard ?? false)
  const hasConnectedClient = computed(() => state.value?.has_connected_client ?? false)
  const hasConfiguredServer = computed(() => state.value?.has_configured_server ?? false)
  const isEngaged = computed(() => state.value?.state.engaged ?? false)

  // The list of step IDs the wizard should render, in order. Derived
  // from the live predicates so the wizard adapts to state on each open.
  const visibleSteps = computed<Array<'connect' | 'server'>>(() => {
    const steps: Array<'connect' | 'server'> = []
    if (!hasConnectedClient.value) steps.push('connect')
    if (!hasConfiguredServer.value) steps.push('server')
    return steps
  })

  /**
   * Fetch the current state from the backend. Safe to call repeatedly;
   * reuses the existing API key handling and credentials.
   */
  async function fetchState(): Promise<OnboardingStateResponse | null> {
    loading.value = true
    error.value = null
    try {
      const res = await api.getOnboardingState()
      if (res.success && res.data) {
        state.value = res.data
        return res.data
      }
      error.value = res.error ?? 'Failed to fetch onboarding state'
      return null
    } catch (err) {
      error.value = (err as Error).message
      return null
    } finally {
      loading.value = false
    }
  }

  /**
   * Persist a mark (step status, engagement, first-shown timestamp).
   * Server returns the updated state so we refresh in place.
   */
  async function mark(payload: OnboardingMarkRequest): Promise<OnboardingStateResponse | null> {
    error.value = null
    try {
      const res = await api.markOnboardingState(payload)
      if (res.success && res.data) {
        state.value = res.data
        return res.data
      }
      error.value = res.error ?? 'Failed to update onboarding state'
      return null
    } catch (err) {
      error.value = (err as Error).message
      return null
    }
  }

  /**
   * Record that the wizard has been shown (sets first_shown_at on first
   * call, no-op on subsequent calls). Best-effort; failures are logged
   * but do not block the UI.
   */
  async function markShown(): Promise<void> {
    if (state.value?.state.first_shown_at) return
    await mark({ mark_shown: true })
  }

  /**
   * Mark the connect step as completed.
   */
  async function markConnectCompleted(): Promise<void> {
    await mark({ connect_step_status: 'completed' })
  }

  /**
   * Mark the connect step as skipped.
   */
  async function markConnectSkipped(): Promise<void> {
    await mark({ connect_step_status: 'skipped' })
  }

  /**
   * Mark the add-server step as completed.
   */
  async function markServerCompleted(): Promise<void> {
    await mark({ server_step_status: 'completed' })
  }

  /**
   * Mark the add-server step as skipped.
   */
  async function markServerSkipped(): Promise<void> {
    await mark({ server_step_status: 'skipped' })
  }

  /**
   * Mark the wizard as engaged (completed or fully skipped). Once true,
   * the wizard will not auto-show on subsequent loads, even if state
   * regresses.
   */
  async function markEngaged(): Promise<void> {
    await mark({ engaged: true })
  }

  function openWizard(): void {
    wizardOpen.value = true
    // Best-effort first-shown stamp.
    void markShown()
  }

  function closeWizard(): void {
    wizardOpen.value = false
  }

  return {
    state,
    loading,
    error,
    wizardOpen,
    shouldShowWizard,
    hasConnectedClient,
    hasConfiguredServer,
    isEngaged,
    visibleSteps,
    fetchState,
    mark,
    markShown,
    markConnectCompleted,
    markConnectSkipped,
    markServerCompleted,
    markServerSkipped,
    markEngaged,
    openWizard,
    closeWizard,
  }
})
