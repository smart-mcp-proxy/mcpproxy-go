<template>
  <div
    v-if="diagnostic && diagnostic.code"
    class="alert"
    :class="severityAlertClass"
    role="alert"
    :aria-label="`Diagnostic ${diagnostic.code}`"
  >
    <svg class="w-6 h-6 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        stroke-width="2"
        d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
      />
    </svg>
    <div class="w-full">
      <div class="flex items-start justify-between gap-3">
        <div>
          <div class="flex items-center gap-2 flex-wrap">
            <h3 class="font-bold">{{ headerTitle }}</h3>
            <span class="badge badge-sm" :class="severityBadgeClass" data-testid="error-panel-severity">
              {{ diagnostic.severity }}
            </span>
            <code class="text-xs opacity-80" data-testid="error-panel-code">{{ diagnostic.code }}</code>
          </div>
          <p v-if="diagnostic.user_message" class="text-sm mt-1" data-testid="error-panel-message">
            {{ diagnostic.user_message }}
          </p>
          <p v-if="diagnostic.cause" class="text-xs opacity-70 mt-1">
            <span class="font-semibold">Cause:</span> {{ diagnostic.cause }}
          </p>
        </div>
        <button
          type="button"
          class="btn btn-xs btn-ghost"
          :aria-expanded="expanded ? 'true' : 'false'"
          :aria-label="expanded ? 'Collapse fix steps' : 'Expand fix steps'"
          @click="expanded = !expanded"
        >
          {{ expanded ? 'Hide steps' : 'Show fix steps' }}
        </button>
      </div>

      <div v-if="expanded" class="mt-3 space-y-2" data-testid="error-panel-fix-steps">
        <ol class="list-decimal list-inside space-y-2 text-sm">
          <li
            v-for="(step, idx) in (diagnostic.fix_steps || [])"
            :key="idx"
            class="flex flex-col gap-1"
          >
            <div class="flex items-center gap-2 flex-wrap">
              <span class="font-medium">{{ step.label }}</span>
              <span
                v-if="step.destructive"
                class="badge badge-xs badge-warning"
                aria-label="Destructive action"
              >destructive</span>
            </div>
            <!-- link fix step -->
            <a
              v-if="step.type === 'link' && step.url"
              :href="step.url"
              target="_blank"
              rel="noopener noreferrer"
              class="link link-primary text-xs break-all"
            >{{ step.url }}</a>

            <!-- command fix step -->
            <div
              v-else-if="step.type === 'command' && step.command"
              class="flex items-center gap-2"
            >
              <code
                class="text-xs bg-base-200 p-1 rounded break-all flex-1"
                :data-testid="`error-panel-command-${idx}`"
              >{{ step.command }}</code>
              <button
                type="button"
                class="btn btn-xs"
                :aria-label="`Copy command: ${step.label}`"
                @click="copyCommand(step.command as string)"
              >Copy</button>
            </div>

            <!-- button fix step -->
            <div v-else-if="step.type === 'button' && step.fixer_key" class="flex items-center gap-2 flex-wrap">
              <!--
                Non-destructive fixes: single primary Execute button (no dry-run UX noise).
                Destructive fixes: Preview (dry-run) + Execute (gated by window.confirm()).
                Fix: gemini P1 — previously non-destructive fixes had no Execute path.
              -->
              <button
                v-if="step.destructive"
                type="button"
                class="btn btn-xs btn-warning"
                :disabled="isFixing(step.fixer_key)"
                :data-testid="`error-panel-fix-button-${idx}`"
                @click="runFixer(step, 'dry_run')"
              >
                <span
                  v-if="isFixing(step.fixer_key)"
                  class="loading loading-spinner loading-xs"
                ></span>
                Preview (dry-run)
              </button>
              <button
                type="button"
                class="btn btn-xs"
                :class="step.destructive ? 'btn-outline btn-error' : 'btn-primary'"
                :disabled="isFixing(step.fixer_key)"
                :data-testid="`error-panel-execute-button-${idx}`"
                @click="step.destructive ? confirmAndExecute(step) : runFixer(step, 'execute')"
              >
                <span
                  v-if="!step.destructive && isFixing(step.fixer_key)"
                  class="loading loading-spinner loading-xs"
                ></span>
                Execute
              </button>
            </div>
          </li>
        </ol>

        <a
          v-if="diagnostic.docs_url"
          :href="diagnostic.docs_url"
          target="_blank"
          rel="noopener noreferrer"
          class="link link-hover text-xs mt-2 inline-block"
          data-testid="error-panel-docs-link"
        >Documentation →</a>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Diagnostic, DiagnosticFixStep } from '@/types'
import api from '@/services/api'
import { useSystemStore } from '@/stores/system'

interface Props {
  diagnostic: Diagnostic | null | undefined
  serverName: string
}

const props = defineProps<Props>()
const emit = defineEmits<{
  (e: 'fixed', payload: { fixerKey: string; mode: 'dry_run' | 'execute' }): void
}>()

const systemStore = useSystemStore()

const expanded = ref(true)
const runningFixers = ref<Set<string>>(new Set())

const severityAlertClass = computed(() => {
  const sev = props.diagnostic?.severity
  if (sev === 'error') return 'alert-error'
  if (sev === 'warn') return 'alert-warning'
  return 'alert-info'
})

const severityBadgeClass = computed(() => {
  const sev = props.diagnostic?.severity
  if (sev === 'error') return 'badge-error'
  if (sev === 'warn') return 'badge-warning'
  return 'badge-info'
})

const headerTitle = computed(() => {
  const sev = props.diagnostic?.severity
  if (sev === 'error') return 'Server Error'
  if (sev === 'warn') return 'Server Warning'
  return 'Diagnostic'
})

function isFixing(key: string | undefined) {
  if (!key) return false
  return runningFixers.value.has(key)
}

async function copyCommand(cmd: string) {
  try {
    await navigator.clipboard.writeText(cmd)
    systemStore.addToast({
      type: 'success',
      title: 'Command copied',
      message: cmd.length > 60 ? cmd.slice(0, 57) + '...' : cmd,
    })
  } catch (err) {
    systemStore.addToast({
      type: 'error',
      title: 'Copy failed',
      message: err instanceof Error ? err.message : String(err),
    })
  }
}

function confirmAndExecute(step: DiagnosticFixStep) {
  if (!step.fixer_key) return
  const ok = typeof window !== 'undefined' && typeof window.confirm === 'function'
    ? window.confirm(
        `Execute destructive fix "${step.label}" on server "${props.serverName}"?\n\nThis action may mutate configuration or trigger a new login.`,
      )
    : true
  if (!ok) return
  void runFixer(step, 'execute')
}

async function runFixer(step: DiagnosticFixStep, mode: 'dry_run' | 'execute') {
  if (!step.fixer_key || !props.diagnostic?.code) return
  const key = step.fixer_key
  runningFixers.value.add(key)
  try {
    const response = await api.invokeDiagnosticFix({
      server: props.serverName,
      code: props.diagnostic.code,
      fixer_key: key,
      mode,
    })
    if (response.success && response.data) {
      const outcome = response.data.outcome
      const titleMode = mode === 'dry_run' ? 'Dry-run' : 'Executed'
      systemStore.addToast({
        type: outcome === 'success' ? 'success' : outcome === 'failed' ? 'error' : 'warning',
        title: `${titleMode}: ${step.label}`,
        message:
          response.data.preview ||
          response.data.failure_msg ||
          `Outcome: ${outcome} (${response.data.duration_ms}ms)`,
      })
      emit('fixed', { fixerKey: key, mode })
    } else {
      systemStore.addToast({
        type: 'error',
        title: `Fix failed: ${step.label}`,
        message: response.error || 'Unknown error',
      })
    }
  } catch (err) {
    systemStore.addToast({
      type: 'error',
      title: `Fix failed: ${step.label}`,
      message: err instanceof Error ? err.message : String(err),
    })
  } finally {
    runningFixers.value.delete(key)
  }
}
</script>
