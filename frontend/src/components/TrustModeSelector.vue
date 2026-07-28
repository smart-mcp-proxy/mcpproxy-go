<template>
  <div class="space-y-2" data-test="trust-mode-selector">
    <div
      v-for="m in TRUST_MODES"
      :key="m.mode"
      :data-test="`trust-mode-option-${m.mode}`"
      :class="[
        'rounded-lg border transition-colors',
        displayedMode === m.mode
          ? 'border-primary bg-primary/5'
          : 'border-base-300 hover:border-base-content/30',
      ]"
    >
      <label class="flex items-start gap-3 p-3 cursor-pointer">
        <input
          type="radio"
          class="radio radio-primary radio-sm mt-0.5 shrink-0"
          :name="groupName"
          :value="m.mode"
          :checked="displayedMode === m.mode"
          @change="onSelect(m.mode)"
        />
        <span class="flex-1 min-w-0">
          <span class="flex flex-wrap items-center gap-2">
            <span class="font-semibold">{{ m.label }}</span>
            <!-- FR-001: no value configured — manual is presented as the default. -->
            <span
              v-if="state.isDefault && state.effective === m.mode"
              data-test="trust-mode-default-note"
              class="badge badge-ghost badge-xs"
            >
              current default
            </span>
            <span v-if="m.warnsOnSelect" class="badge badge-warning badge-xs">least safe</span>
          </span>
          <!-- FR-002: both behaviors each mode governs — tool changes… -->
          <span class="block mt-1 text-sm opacity-80 leading-relaxed">{{ m.description }}</span>
          <!-- …and new-server admission. -->
          <span class="block mt-1 text-xs opacity-60 leading-relaxed">{{ m.admissionNote }}</span>
        </span>
      </label>

      <!-- FR-003: the least-safe mode is applied only after the operator
           acknowledges BOTH risks (unscanned tool changes + unscanned admission). -->
      <div
        v-if="pendingWarnMode === m.mode"
        data-test="trust-mode-auto-confirm"
        class="border-t border-warning/40 bg-warning/10 px-3 py-3 space-y-2 rounded-b-lg"
      >
        <p class="text-sm font-semibold text-warning">Apply “{{ m.label }}” trust mode?</p>
        <p class="text-xs leading-relaxed">{{ m.description }}</p>
        <p class="text-xs leading-relaxed">{{ m.admissionNote }}</p>
        <div class="flex items-center gap-2 pt-1">
          <button
            type="button"
            data-test="trust-mode-auto-confirm-accept"
            class="btn btn-warning btn-xs"
            @click="confirmPending"
          >
            I understand — use {{ m.label }}
          </button>
          <button
            type="button"
            data-test="trust-mode-auto-confirm-cancel"
            class="btn btn-ghost btn-xs"
            @click="cancelPending"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>

    <!-- FR-001 / US1 scenario 4: an unrecognized configured value is shown as-is
         next to the fail-closed mode actually in effect — never hidden, never
         silently rewritten. -->
    <p
      v-if="state.isInvalid"
      data-test="trust-mode-invalid-note"
      class="text-xs text-warning leading-relaxed"
    >
      Configured value <code class="font-mono">{{ state.raw }}</code> is not a recognized trust
      mode, so this server behaves as <strong>manual</strong> (fail closed). Choosing a mode above
      replaces the unrecognized value.
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, getCurrentInstance, ref, watch } from 'vue'
import { TRUST_MODES, deriveTrustModeState, type TrustMode } from '@/utils/trustMode'

// Spec 088 US1 (FR-001/FR-002/FR-003/FR-005): tri-mode trust selector replacing
// the legacy binary auto-approve toggle. Presentational only — it emits the
// chosen mode string; persistence (PATCH `{trust_mode}` + restart-required
// notice, FR-004) belongs to the parent view.
const props = defineProps<{
  /** Raw `Server.trust_mode` as delivered — may be absent, migrated or invalid. */
  modelValue?: string
  /** Radio-group name; defaults to a per-instance unique name. */
  name?: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', mode: TrustMode): void
}>()

const uid = getCurrentInstance()?.uid ?? 0
const groupName = computed(() => props.name || `trust-mode-${uid}`)

/** Fail-closed derivation mirroring the backend accessor (display only). */
const state = computed(() => deriveTrustModeState(props.modelValue))

/** A warning-gated mode awaiting acknowledgement (not yet emitted). */
const pendingWarnMode = ref<TrustMode | null>(null)

/** Tentative selection while a warning is open; otherwise the effective mode. */
const displayedMode = computed<TrustMode>(() => pendingWarnMode.value ?? state.value.effective)

// A value arriving from the parent (save, re-load, SSE refresh) supersedes any
// selection still awaiting acknowledgement.
watch(
  () => props.modelValue,
  () => {
    pendingWarnMode.value = null
  }
)

function onSelect(mode: TrustMode) {
  if (mode === state.value.effective) {
    // Already in effect — nothing to warn about and nothing to save.
    pendingWarnMode.value = null
    return
  }
  const meta = TRUST_MODES.find((m) => m.mode === mode)
  if (meta?.warnsOnSelect) {
    pendingWarnMode.value = mode
    return
  }
  pendingWarnMode.value = null
  emit('update:modelValue', mode)
}

function confirmPending() {
  const mode = pendingWarnMode.value
  pendingWarnMode.value = null
  if (mode) emit('update:modelValue', mode)
}

function cancelPending() {
  pendingWarnMode.value = null
}
</script>
