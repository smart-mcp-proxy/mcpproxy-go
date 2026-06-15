<template>
  <div class="space-y-3" :data-test="`settings-section-${sectionId}`">
    <SettingField
      v-for="f in fields"
      :key="f.key"
      :field="f"
      :model-value="getPath(working, f.key)"
      :dirty="isFieldDirty(f.key)"
      @update:model-value="onChange(f, $event)"
    />
    <p v-if="!fields.length" class="text-sm text-base-content/50 py-4">No settings match your search.</p>

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
      <div class="flex items-center gap-2">
        <button
          v-if="dirtyKeys.length"
          class="btn btn-ghost btn-sm"
          :disabled="saving"
          :data-test="`settings-discard-${sectionId}`"
          @click="discard"
        >
          Discard
        </button>
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
    </div>

    <!-- danger confirm -->
    <dialog ref="confirmEl" class="modal" :data-test="`settings-confirm-${sectionId}`">
      <div class="modal-box">
        <h3 class="font-bold text-lg" :class="pendingInfoOnly ? '' : 'text-error'">
          {{ pendingInfoOnly ? 'Are you sure?' : 'Confirm sensitive change' }}
        </h3>
        <ul class="list-disc list-inside text-sm mt-3 space-y-2">
          <li v-for="(m, i) in pendingMessages" :key="i">{{ m }}</li>
        </ul>
        <div class="modal-action">
          <button class="btn btn-sm" @click="cancelConfirm" data-test="settings-confirm-cancel">
            {{ pendingInfoOnly ? 'Keep it on' : 'Cancel' }}
          </button>
          <button
            class="btn btn-sm"
            :class="pendingInfoOnly ? 'btn-primary' : 'btn-error'"
            @click="proceedConfirm"
            data-test="settings-confirm-proceed"
          >
            {{ pendingInfoOnly ? 'Turn off anyway' : 'Apply anyway' }}
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
import { getPath, setPath, buildPartial, validateField, type SettingField as Field } from '@/views/settings/fields'
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
const pendingInfoOnly = ref(false)

// A field is dirty if the user changed it through a control (tracked in the
// `dirty` ref by onChange) OR its working value diverges from the last-saved
// original. The latter catches values written onto `state.working` from OUTSIDE
// a control — notably the instructions prefill in Settings.vue (MCP-2484).
// Without it, "Save without editing" after a prefill would PATCH nothing because
// the field was never marked dirty.
const dirtyKeys = computed(() => {
  const keys = new Set(Object.keys(dirty.value))
  for (const f of props.fields) {
    if (!eq(getPath(props.working, f.key), getPath(props.original, f.key))) keys.add(f.key)
  }
  return [...keys]
})

function isFieldDirty(key: string): boolean {
  if (key in dirty.value) return true
  const f = props.fields.find((x) => x.key === key)
  return f != null && !eq(getPath(props.working, key), getPath(props.original, key))
}

// Block Save only when a CHANGED field is invalid — a pre-existing value the
// user hasn't touched must never block saving unrelated edits.
const hasInvalid = computed(() =>
  dirtyKeys.value.some((k) => {
    const f = props.fields.find((x) => x.key === k)
    return f != null && validateField(f, getPath(props.working, k)) != null
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

function dangerMessages(): Array<{ message: string; tone: 'danger' | 'info' }> {
  const out: Array<{ message: string; tone: 'danger' | 'info' }> = []
  for (const key of dirtyKeys.value) {
    const f = props.fields.find((x) => x.key === key)
    if (!f?.danger) continue
    const val = getPath(props.working, key)
    const entry = { message: f.danger.message, tone: f.danger.tone ?? 'danger' }
    if ('confirmValue' in f.danger) {
      if (eq(val, f.danger.confirmValue)) out.push(entry)
    } else if (f.key === 'listen') {
      if (!isLoopback(val)) out.push(entry)
    } else {
      out.push(entry)
    }
  }
  return out
}

function discard() {
  for (const key of dirtyKeys.value) {
    setPath(props.working, key, getPath(props.original, key))
  }
  dirty.value = {}
  lastResult.value = null
  error.value = ''
}

function attemptSave() {
  const items = dangerMessages()
  if (items.length) {
    pendingMessages.value = items.map((i) => i.message)
    pendingInfoOnly.value = items.every((i) => i.tone === 'info')
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
