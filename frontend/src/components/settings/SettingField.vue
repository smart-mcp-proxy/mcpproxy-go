<template>
  <div
    class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2 py-3 px-2 -mx-2 border-b border-base-200 last:border-0 rounded transition-colors"
    :class="dirty ? 'bg-warning/5 border-l-2 border-l-warning' : 'border-l-2 border-l-transparent'"
    :data-test="`setting-row-${field.key}`"
  >
    <!-- Label + help + badges -->
    <div class="sm:max-w-md">
      <div class="flex items-center gap-2 flex-wrap">
        <span class="font-medium">{{ field.label }}</span>
        <span v-if="dirty" class="badge badge-warning badge-xs" title="Unsaved change">●</span>
        <a
          v-if="docsHref"
          :href="docsHref"
          target="_blank"
          rel="noopener noreferrer"
          class="link link-primary text-xs font-normal"
          :data-test="`setting-docs-${field.key}`"
          title="Open documentation"
          @click.stop
        >docs ↗</a>
        <span v-if="field.restart" class="badge badge-warning badge-xs gap-1" title="Requires restart">
          restart
        </span>
        <span v-if="field.danger" class="badge badge-error badge-xs" title="Sensitive change">
          sensitive
        </span>
      </div>
      <p v-if="field.help" class="text-xs text-base-content/60 mt-0.5">{{ field.help }}</p>
    </div>

    <!-- Control -->
    <div class="shrink-0">
      <!-- toggle -->
      <input
        v-if="field.control === 'toggle'"
        type="checkbox"
        class="toggle toggle-primary"
        :checked="!!modelValue"
        :data-test="`setting-toggle-${field.key}`"
        @change="emitVal(($event.target as HTMLInputElement).checked)"
      />

      <!-- select -->
      <select
        v-else-if="field.control === 'select'"
        class="select select-bordered select-sm min-w-[12rem]"
        :value="modelValue ?? ''"
        :data-test="`setting-select-${field.key}`"
        @change="emitVal(($event.target as HTMLSelectElement).value)"
      >
        <option v-for="opt in field.options" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
      </select>

      <!-- number -->
      <input
        v-else-if="field.control === 'number'"
        type="number"
        class="input input-bordered input-sm w-32"
        :class="{ 'input-error': numberError }"
        :value="modelValue"
        :min="field.min"
        :max="field.max"
        :step="field.step"
        :data-test="`setting-number-${field.key}`"
        @input="onNumber(($event.target as HTMLInputElement).value)"
      />

      <!-- secret -->
      <div v-else-if="field.control === 'secret'" class="join">
        <input
          :type="showSecret ? 'text' : 'password'"
          class="input input-bordered input-sm join-item w-56 font-mono"
          :value="modelValue ?? ''"
          :placeholder="field.placeholder"
          :data-test="`setting-secret-${field.key}`"
          @input="emitVal(($event.target as HTMLInputElement).value)"
        />
        <button type="button" class="btn btn-sm join-item" :title="showSecret ? 'Hide' : 'Show'" @click="showSecret = !showSecret">
          {{ showSecret ? '🙈' : '👁' }}
        </button>
        <button
          type="button"
          class="btn btn-sm join-item"
          :data-test="`setting-regenerate-${field.key}`"
          title="Generate a new random key"
          @click="regenerate"
        >
          ↻
        </button>
      </div>

      <!-- text / duration -->
      <input
        v-else-if="field.control === 'text' || field.control === 'duration'"
        type="text"
        class="input input-bordered input-sm w-56 font-mono"
        :value="modelValue ?? ''"
        :placeholder="field.placeholder"
        :data-test="`setting-text-${field.key}`"
        @input="emitVal(($event.target as HTMLInputElement).value)"
      />

      <!-- multiselect (checkbox group) -->
      <div v-else-if="field.control === 'multiselect'" class="flex flex-wrap gap-3 justify-end max-w-xs">
        <label v-for="opt in field.options" :key="opt.value" class="label cursor-pointer gap-1 p-0">
          <input
            type="checkbox"
            class="checkbox checkbox-xs"
            :checked="Array.isArray(modelValue) && modelValue.includes(opt.value)"
            :data-test="`setting-multi-${field.key}-${opt.value}`"
            @change="toggleMulti(opt.value, ($event.target as HTMLInputElement).checked)"
          />
          <span class="text-xs">{{ opt.label }}</span>
        </label>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { docsUrl, type SettingField } from '@/views/settings/fields'

const props = defineProps<{ field: SettingField; modelValue: any; dirty?: boolean }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: any): void }>()

const showSecret = ref(false)
const docsHref = computed(() => docsUrl(props.field.docs))

const numberError = computed(() => {
  if (props.field.control !== 'number' || props.modelValue == null || props.modelValue === '') return false
  const n = Number(props.modelValue)
  if (Number.isNaN(n)) return true
  if (props.field.min != null && n < props.field.min) return true
  if (props.field.max != null && n > props.field.max) return true
  return false
})

function emitVal(v: any) {
  emit('update:modelValue', v)
}

function onNumber(raw: string) {
  if (raw === '') {
    emitVal('')
    return
  }
  emitVal(Number(raw))
}

function toggleMulti(value: string, checked: boolean) {
  const cur = Array.isArray(props.modelValue) ? [...props.modelValue] : []
  const idx = cur.indexOf(value)
  if (checked && idx === -1) cur.push(value)
  if (!checked && idx !== -1) cur.splice(idx, 1)
  emitVal(cur)
}

function regenerate() {
  // 32-byte random hex key — generated client-side; user must save + restart.
  const bytes = new Uint8Array(32)
  crypto.getRandomValues(bytes)
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
  showSecret.value = true
  emitVal(hex)
}

defineExpose({ numberError })
</script>
