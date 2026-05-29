<template>
  <div class="space-y-3" :data-test="`settings-section-${sectionId}`">
    <SettingField
      v-for="f in fields"
      :key="f.key"
      :field="f"
      :model-value="getPath(working, f.key)"
      @update:model-value="onChange(f, $event)"
    />

    <!-- per-section action bar -->
    <div class="flex items-center justify-between pt-2">
      <div class="text-sm min-h-[1.25rem]">
        <span v-if="lastResult?.requires_restart" class="text-warning" :data-test="`settings-restart-${sectionId}`">
          ⚠️ Saved — restart required{{ lastResult.restart_reason ? `: ${lastResult.restart_reason}` : '' }}
        </span>
        <span v-else-if="lastResult" class="text-success" :data-test="`settings-saved-${sectionId}`">
          ✓ Saved
        </span>
        <span v-else-if="error" class="text-error">{{ error }}</span>
        <span v-else-if="dirtyKeys.length" class="text-base-content/60">
          {{ dirtyKeys.length }} unsaved change{{ dirtyKeys.length > 1 ? 's' : '' }}
        </span>
      </div>
      <button
        class="btn btn-primary btn-sm"
        :disabled="!dirtyKeys.length || saving || hasInvalid"
        :data-test="`settings-apply-${sectionId}`"
        @click="attemptSave"
      >
        <span v-if="saving" class="loading loading-spinner loading-xs"></span>
        Save changes
      </button>
    </div>

    <!-- danger confirm -->
    <dialog ref="confirmEl" class="modal" :data-test="`settings-confirm-${sectionId}`">
      <div class="modal-box">
        <h3 class="font-bold text-lg text-error">Confirm sensitive change</h3>
        <ul class="list-disc list-inside text-sm mt-3 space-y-2">
          <li v-for="(m, i) in pendingMessages" :key="i">{{ m }}</li>
        </ul>
        <div class="modal-action">
          <button class="btn btn-sm" @click="cancelConfirm" data-test="settings-confirm-cancel">Cancel</button>
          <button class="btn btn-sm btn-error" @click="proceedConfirm" data-test="settings-confirm-proceed">
            Apply anyway
          </button>
        </div>
      </div>
      <form method="dialog" class="modal-backdrop"><button @click="cancelConfirm">close</button></form>
    </dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, getCurrentInstance } from 'vue'
import SettingField from './SettingField.vue'
import { getPath, setPath, buildPartial, type SettingField as Field } from '@/views/settings/fields'
import { useSystemStore } from '@/stores/system'
import api from '@/services/api'

const props = defineProps<{
  sectionId: string
  fields: Field[]
  working: any // reactive working copy of the full config
  original: any // snapshot of last-saved values
}>()
const emit = defineEmits<{ (e: 'saved', changed: string[]): void }>()

const systemStore = useSystemStore()
const saving = ref(false)
const error = ref('')
const lastResult = ref<any>(null)
const dirty = ref<Record<string, true>>({})
const confirmEl = ref<HTMLDialogElement | null>(null)
const pendingMessages = ref<string[]>([])

const dirtyKeys = computed(() => Object.keys(dirty.value))

// crude validity gate: any number field out of range blocks save
const hasInvalid = computed(() =>
  props.fields.some((f) => {
    if (f.control !== 'number') return false
    const v = getPath(props.working, f.key)
    if (v === '' || v == null) return true
    const n = Number(v)
    if (Number.isNaN(n)) return true
    if (f.min != null && n < f.min) return true
    if (f.max != null && n > f.max) return true
    return false
  })
)

function eq(a: any, b: any): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}

function onChange(f: Field, val: any) {
  setPath(props.working, f.key, val)
  if (eq(getPath(props.working, f.key), getPath(props.original, f.key))) {
    delete dirty.value[f.key]
  } else {
    dirty.value[f.key] = true
  }
  dirty.value = { ...dirty.value } // trigger reactivity
  lastResult.value = null
  error.value = ''
}

function isLoopback(addr: any): boolean {
  const s = String(addr ?? '')
  return /^(127\.|localhost|\[::1\]|::1)/.test(s.replace(/^.*@/, '')) || s.startsWith('127.0.0.1')
}

function dangerMessages(): string[] {
  const msgs: string[] = []
  for (const key of dirtyKeys.value) {
    const f = props.fields.find((x) => x.key === key)
    if (!f?.danger) continue
    const val = getPath(props.working, key)
    if ('confirmValue' in f.danger) {
      if (eq(val, f.danger.confirmValue)) msgs.push(f.danger.message)
    } else if (f.key === 'listen') {
      if (!isLoopback(val)) msgs.push(f.danger.message)
    } else {
      msgs.push(f.danger.message)
    }
  }
  return msgs
}

function attemptSave() {
  const msgs = dangerMessages()
  if (msgs.length) {
    pendingMessages.value = msgs
    confirmEl.value?.showModal()
    return
  }
  void doSave()
}

function cancelConfirm() {
  confirmEl.value?.close()
  pendingMessages.value = []
}

function proceedConfirm() {
  confirmEl.value?.close()
  pendingMessages.value = []
  void doSave()
}

async function doSave() {
  saving.value = true
  error.value = ''
  lastResult.value = null
  try {
    const keys = dirtyKeys.value
    const partial = buildPartial(props.working, keys)
    const resp = await api.patchConfig(partial)
    if (resp.success && resp.data) {
      lastResult.value = resp.data
      if (resp.data.validation_errors && resp.data.validation_errors.length) {
        error.value = resp.data.validation_errors.map((e: any) => `${e.field}: ${e.message}`).join('; ')
        lastResult.value = null
      } else {
        // commit saved values into the original snapshot, clear dirty
        for (const k of keys) setPath(props.original, k, getPath(props.working, k))
        dirty.value = {}
        systemStore.addToast({
          type: resp.data.requires_restart ? 'warning' : 'success',
          title: resp.data.requires_restart ? 'Saved — restart required' : 'Settings saved',
          message: (resp.data.changed_fields || keys).join(', '),
        })
        emit('saved', keys)
      }
    } else {
      error.value = resp.error || 'Failed to save'
    }
  } catch (e: any) {
    error.value = e?.message || 'Failed to save'
  } finally {
    saving.value = false
  }
}

// keep getCurrentInstance import used (avoids tree-shake lint in some setups)
void getCurrentInstance
</script>
