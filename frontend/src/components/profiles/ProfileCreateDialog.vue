<template>
  <BaseDialog :open="open" title="Create profile" test-id="profile-create-dialog" @close="emit('close')">
    <form class="space-y-3" @submit.prevent="submit">
      <div class="form-control">
        <label class="label" for="profile-name"><span class="label-text font-medium">Name</span></label>
        <input id="profile-name" v-model.trim="form.name" class="input input-bordered input-sm w-full font-mono" :class="fieldError('name') && 'input-error'" placeholder="work-readonly" autocomplete="off" data-test="profile-create-name" />
        <span v-if="fieldError('name')" class="text-error text-xs mt-1" data-test="profile-create-name-error">{{ fieldError('name') }}</span>
        <span v-else class="text-xs opacity-60 mt-1">A short slug: lower-case letters, digits, '-' and '_'.</span>
      </div>
      <div class="form-control">
        <label class="label" for="profile-title"><span class="label-text font-medium">Title (optional)</span></label>
        <input id="profile-title" v-model="form.title" maxlength="80" class="input input-bordered input-sm w-full" :class="fieldError('title') && 'input-error'" placeholder="Work Read-only" data-test="profile-create-title" />
        <span v-if="fieldError('title')" class="text-error text-xs mt-1">{{ fieldError('title') }}</span>
      </div>
      <fieldset class="form-control" :class="fieldError('servers') && 'border border-error rounded p-2'">
        <legend class="label-text font-medium mb-1">Servers</legend>
        <div class="max-h-40 overflow-y-auto space-y-1 border border-base-300 rounded-lg p-2" data-test="profile-create-servers">
          <p v-if="!serverNames.length" class="text-sm opacity-60 text-center py-1">No servers configured</p>
          <label v-for="name in serverNames" :key="name" class="flex items-center gap-2 cursor-pointer px-1">
            <input v-model="form.servers" type="checkbox" class="checkbox checkbox-sm" :value="name" :data-test="`profile-create-server-${name}`" />
            <span class="text-sm">{{ name }}</span>
          </label>
        </div>
        <span v-if="fieldError('servers')" class="text-error text-xs mt-1">{{ fieldError('servers') }}</span>
      </fieldset>
      <fieldset class="form-control">
        <legend class="label-text font-medium mb-1">Starting point</legend>
        <label v-for="option in STARTING_POINTS" :key="option.id" class="label cursor-pointer justify-start gap-2 py-1">
          <input v-model="form.start" type="radio" class="radio radio-sm" :value="option.id" :data-test="`profile-create-start-${option.id}`" />
          <span class="label-text">{{ option.label }}</span>
        </label>
      </fieldset>
      <GuardRefusal v-if="guard" :refusal="guard" @navigate="emit('close')" />
      <div v-else-if="otherError" role="alert" class="alert alert-error text-sm" data-test="profile-create-error">{{ otherError }}</div>
      <div class="modal-action">
        <button type="button" class="btn btn-ghost btn-sm" @click="emit('close')">Cancel</button>
        <button type="submit" class="btn btn-primary btn-sm" :disabled="busy || !form.name" data-test="profile-create-submit">
          <span v-if="busy" class="loading loading-spinner loading-xs" />
          Create profile
        </button>
      </div>
    </form>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import BaseDialog from '@/components/BaseDialog.vue'
import GuardRefusal from '@/components/GuardRefusal.vue'
import api from '@/services/api'
import { useServersStore } from '@/stores/servers'
import { describeError, isGuardRefusal } from '@/utils/profiles'
import type { ProfileConfig } from '@/types/api'
import type { ApiError } from '@/services/api'

// Spec 108-i I5 / FR-040: the goal flow "create Work Read-only with chosen
// servers, read tools only" in one dialog. The starting point sets max_tier;
// Custom sets none and the editor takes over.
const STARTING_POINTS = [
  { id: 'read', label: 'Read-only', tier: 'read' },
  { id: 'write', label: 'Read + write', tier: 'write' },
  { id: 'destructive', label: 'Everything', tier: 'destructive' },
  { id: 'custom', label: 'Custom', tier: '' },
] as const

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'created', name: string): void }>()
const serversStore = useServersStore()
const busy = ref(false)
const error = ref<ApiError | null>(null)
const form = reactive({ name: '', title: '', servers: [] as string[], start: 'read' as (typeof STARTING_POINTS)[number]['id'] })

const serverNames = computed(() => serversStore.servers.map(server => server.name).sort((a, b) => a.localeCompare(b)))

watch(() => props.open, open => {
  if (!open) return
  Object.assign(form, { name: '', title: '', servers: [], start: 'read' })
  error.value = null
  busy.value = false
  if (!serversStore.servers.length) void serversStore.fetchServers()
})

function fieldError(field: string): string { return error.value?.field === field ? error.value.message : '' }
const guard = computed(() => (error.value && isGuardRefusal(error.value) ? error.value : null))
const otherError = computed(() => (error.value && !guard.value && !['name', 'title', 'servers'].includes(error.value.field ?? '') ? describeError(error.value) : ''))

async function submit() {
  busy.value = true
  error.value = null
  try {
    const cfg: ProfileConfig = { name: form.name, servers: [...form.servers] }
    if (form.title.trim()) cfg.title = form.title.trim()
    const tier = STARTING_POINTS.find(option => option.id === form.start)?.tier
    if (tier) cfg.max_tier = tier
    const result = await api.createProfile(cfg)
    emit('created', result.profile.name)
  } catch (err) {
    error.value = err as ApiError
  } finally {
    busy.value = false
  }
}
</script>
