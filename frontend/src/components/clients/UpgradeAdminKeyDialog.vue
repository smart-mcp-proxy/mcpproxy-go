<template>
  <BaseDialog :open="open" title="Upgrade admin-key clients" wide test-id="upgrade-admin-key-dialog" @close="emit('close')">
    <!-- Step 1: choose the binding every upgraded client receives. -->
    <form v-if="step === 'choose'" class="space-y-3" @submit.prevent="preview">
      <p class="text-sm opacity-70">
        These clients hold the admin API key in their config. Each gets its own client credential instead. Nothing is written until you review the change and apply it.
      </p>
      <div class="form-control">
        <label class="label" for="upgrade-profile"><span class="label-text font-medium">Profile</span></label>
        <select id="upgrade-profile" v-model="profile" class="select select-bordered select-sm w-full" data-test="upgrade-profile">
          <option value="">All servers</option>
          <option v-for="item in profiles.profiles" :key="item.name" :value="item.name">{{ item.title || item.name }}</option>
        </select>
      </div>
      <fieldset class="form-control">
        <legend class="label-text font-medium mb-1">Mode</legend>
        <label class="label cursor-pointer justify-start gap-2"><input v-model="mode" type="radio" class="radio radio-sm" value="" data-test="upgrade-mode-default" /><span class="label-text">Keep default</span></label>
        <label class="label cursor-pointer justify-start gap-2"><input v-model="mode" type="radio" class="radio radio-sm" value="locked" :disabled="!profile" data-test="upgrade-mode-locked" /><span class="label-text">Locked</span></label>
        <label class="label cursor-pointer justify-start gap-2"><input v-model="mode" type="radio" class="radio radio-sm" value="switchable" data-test="upgrade-mode-switchable" /><span class="label-text">Switchable</span></label>
      </fieldset>
      <div v-if="error" role="alert" class="alert alert-error text-sm" data-test="upgrade-error">{{ error }}</div>
      <div class="modal-action">
        <button type="button" class="btn btn-ghost btn-sm" @click="emit('close')">Cancel</button>
        <button type="submit" class="btn btn-primary btn-sm" :disabled="busy" data-test="upgrade-preview">
          <span v-if="busy" class="loading loading-spinner loading-xs" />
          Preview
        </button>
      </div>
    </form>

    <!-- Step 2: the combined preview. -->
    <div v-else-if="step === 'preview' && previewData" class="space-y-3" data-test="upgrade-preview-step">
      <p v-if="stale" role="alert" class="alert alert-warning text-sm" data-test="upgrade-stale">Client configs changed since the preview. Preview again before applying.</p>
      <GuardRefusal v-if="previewData.guard" :refusal="{ error: guardText, bindings: previewData.guard.bindings, fixes: previewData.guard.fixes }" @navigate="emit('close')" />
      <div class="overflow-x-auto">
        <table class="table table-sm" data-test="upgrade-preview-table">
          <thead><tr><th scope="col">Client</th><th scope="col">Config path</th><th scope="col">Credential</th><th scope="col">Profile</th><th scope="col">Mode</th></tr></thead>
          <tbody>
            <template v-for="row in previewData.preview" :key="row.client_id">
              <tr :data-test="`upgrade-row-${row.client_id}`">
                <td class="font-medium">{{ row.display_name || row.client_id }}</td>
                <td><code class="text-xs break-all">{{ row.display_path || '—' }}</code></td>
                <td><code class="text-xs">{{ row.credential }}</code></td>
                <td>{{ row.profile ? profiles.titleFor(row.profile) : 'All servers' }}</td>
                <td>{{ row.mode }}</td>
              </tr>
              <tr v-if="row.error"><td colspan="5" class="text-error text-xs">{{ row.error }}</td></tr>
              <tr>
                <td colspan="5" class="pt-0">
                  <details class="text-xs"><summary class="cursor-pointer opacity-70">Show the change</summary><pre class="mt-1 whitespace-pre-wrap break-all rounded bg-base-200 p-2">{{ JSON.stringify(row.diff, null, 2) }}</pre></details>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
      <div v-if="error" role="alert" class="alert alert-error text-sm" data-test="upgrade-error">{{ error }}</div>
      <div class="modal-action">
        <button type="button" class="btn btn-ghost btn-sm" @click="step = 'choose'">Back</button>
        <button type="button" class="btn btn-primary btn-sm" :disabled="busy || !!previewData.guard || stale" data-test="upgrade-apply" @click="apply">
          <span v-if="busy" class="loading loading-spinner loading-xs" />
          Apply to {{ previewData.preview.length }} client{{ previewData.preview.length === 1 ? '' : 's' }}
        </button>
        <button v-if="stale" type="button" class="btn btn-outline btn-sm" data-test="upgrade-preview-again" @click="preview">Preview again</button>
      </div>
    </div>

    <!-- Step 3: what happened, and the step the dialog never does itself. -->
    <div v-else-if="step === 'done'" class="space-y-3" data-test="upgrade-done-step" role="status" aria-live="polite">
      <p v-if="applied" class="text-sm"><strong>{{ applied.upgraded.length }}</strong> upgraded<template v-if="applied.upgraded.length">: {{ applied.upgraded.join(', ') }}</template>.</p>
      <p v-else class="text-sm">No client holds the admin key.</p>
      <ul v-if="applied?.failed.length" class="text-sm text-error list-disc list-inside" data-test="upgrade-failed">
        <li v-for="item in applied.failed" :key="item.client_id">{{ item.client_id }}: {{ item.error }}</li>
      </ul>
      <section class="rounded-box border border-warning/50 p-3 space-y-2" data-test="rotate-admin-key-panel">
        <h4 class="font-semibold">Rotate the admin API key</h4>
        <p class="text-sm">Upgraded clients no longer need the admin key, but every other copy of it still works until you replace it.</p>
        <ol class="list-decimal list-inside text-sm space-y-1">
          <li>Set a new <code>api_key</code> in the MCPProxy config.</li>
          <li>Restart MCPProxy. Every copy of the old key stops working, including in unmanaged clients.</li>
        </ol>
        <a class="link link-primary text-sm" :href="docsHref" target="_blank" rel="noopener noreferrer" data-test="rotate-admin-key-docs">How to rotate the admin API key</a>
      </section>
      <div class="modal-action"><button type="button" class="btn btn-primary btn-sm" data-test="upgrade-close" @click="emit('close')">Close</button></div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import BaseDialog from '@/components/BaseDialog.vue'
import GuardRefusal from '@/components/GuardRefusal.vue'
import api, { type ApiError } from '@/services/api'
import { useProfilesStore } from '@/stores/profiles'
import { describeError, isGuardRefusal } from '@/utils/profiles'
import { docsUrl } from '@/views/settings/fields'
import type { UpgradeApplyResult, UpgradePreview } from '@/types/api'

// Spec 108-i I11 / FR-025. A previewed bulk upgrade of every client that holds
// the admin API key, then the prompt to rotate that key. The dialog never
// rotates the key itself.
const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'done'): void }>()
const profiles = useProfilesStore()
const step = ref<'choose' | 'preview' | 'done'>('choose')
const profile = ref('')
const mode = ref<'' | 'locked' | 'switchable'>('')
const busy = ref(false)
const error = ref('')
const stale = ref(false)
const previewData = ref<UpgradePreview | null>(null)
const applied = ref<UpgradeApplyResult | null>(null)
const docsHref = docsUrl('/configuration/config-file/')
const guardText = 'Binding every upgraded client to this profile would leave it reachable without authentication.'

watch(() => props.open, open => {
  if (!open) return
  step.value = 'choose'; profile.value = ''; mode.value = ''; busy.value = false; error.value = ''; stale.value = false
  previewData.value = null; applied.value = null
  if (!profiles.loaded) void profiles.fetchProfiles()
})
watch(profile, value => { if (!value && mode.value === 'locked') mode.value = '' })

function body(): { profile?: string; mode?: 'locked' | 'switchable' } {
  const out: { profile?: string; mode?: 'locked' | 'switchable' } = {}
  if (profile.value) out.profile = profile.value
  if (mode.value) out.mode = mode.value
  return out
}

async function preview() {
  busy.value = true
  error.value = ''
  stale.value = false
  try {
    const result = await api.upgradeAdminKeyHolders(body())
    previewData.value = { preview: result.preview ?? [], precondition_token: result.precondition_token, guard: result.guard, next_step: result.next_step }
    if (!previewData.value.preview.length) {
      applied.value = null
      step.value = 'done'
    } else {
      step.value = 'preview'
    }
  } catch (err) {
    error.value = describeError(err)
  } finally {
    busy.value = false
  }
}

async function apply() {
  if (!previewData.value) return
  busy.value = true
  error.value = ''
  try {
    applied.value = await api.upgradeAdminKeyHolders({ ...body(), apply: true, precondition_token: previewData.value.precondition_token })
    step.value = 'done'
    emit('done')
  } catch (err) {
    const refused = err as ApiError
    if (refused.code === 'precondition_failed') stale.value = true
    else if (isGuardRefusal(err)) previewData.value = { ...previewData.value, guard: { code: refused.code ?? '', bindings: refused.bindings ?? [], fixes: refused.fixes ?? [] } }
    else error.value = describeError(err)
  } finally {
    busy.value = false
  }
}
</script>
