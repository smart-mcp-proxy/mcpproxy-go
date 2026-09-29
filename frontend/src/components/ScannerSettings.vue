<template>
  <section class="mt-6 border-t border-base-300 pt-5" data-test="settings-scanners">
    <h3 class="font-semibold">Scanners</h3>
    <p class="mb-3 text-sm text-base-content/70">Optional deep scanners run in Docker alongside the built-in offline baseline. Deep-scan and Docker isolation controls remain in the settings above.</p>
    <div v-if="loading" class="text-sm"><span class="loading loading-spinner loading-xs"></span> Loading scanners…</div>
    <div v-else class="space-y-2">
      <div v-for="scanner in scanners" :key="scanner.id" class="rounded border border-base-300 p-3">
        <div class="flex items-center justify-between gap-3">
          <div><div class="font-medium">{{ scanner.name }}</div><div class="text-xs text-base-content/60">{{ scanner.description || scanner.status }}</div><div v-if="scanner.status === 'error' && scanner.error_message" class="text-xs text-error">{{ scanner.error_message }}</div></div>
          <div class="flex gap-2">
            <button v-if="scanner.status === 'error'" :data-test="`scanner-retry-${scanner.id}`" class="btn btn-sm btn-error btn-outline" @click="retry(scanner)">Retry</button>
            <button class="btn btn-sm btn-ghost" @click="openConfig(scanner)">Configure</button>
            <button class="btn btn-sm" :class="isEnabled(scanner) ? 'btn-outline' : 'btn-primary'" @click="toggle(scanner)">{{ isEnabled(scanner) ? 'Disable' : 'Enable' }}</button>
          </div>
        </div>
      </div>
      <p v-if="!scanners.length" class="text-sm text-base-content/60">No optional scanners are installed. The offline baseline remains active.</p>
    </div>
    <dialog ref="dialog" class="modal"><div class="modal-box"><h4 class="font-bold">Configure {{ configuring?.name }}</h4><p class="mt-1 text-sm text-base-content/60">Scanner secrets are stored through MCPProxy's existing secure configuration flow.</p>
      <label v-for="env in configuredEnvs" :key="env.key" class="form-control mt-3"><span class="label-text">{{ env.label || env.key }} <span v-if="env.optional" class="badge badge-ghost badge-xs">Optional</span></span><input v-model="values[env.key]" class="input input-bordered" :type="env.secret ? 'password' : 'text'" :placeholder="env.configured ? 'Already configured' : 'Enter value'" /></label>
      <label class="form-control mt-3"><span class="label-text">Docker Image <span class="badge badge-ghost badge-xs">Optional</span></span><input v-model="dockerImage" class="input input-bordered font-mono" :placeholder="configuring?.image_override || configuring?.docker_image || 'default image'" /></label>
      <div class="divider text-xs">Add Custom Variable</div><div class="flex gap-2"><input v-model="customKey" class="input input-bordered input-sm flex-1" placeholder="VARIABLE_NAME" /><input v-model="customValue" class="input input-bordered input-sm flex-1" type="password" placeholder="Value" /><button class="btn btn-sm btn-outline" :disabled="!customKey || !customValue" @click="addCustom">Add</button></div>
      <div class="modal-action"><button class="btn" @click="dialog?.close()">Cancel</button><button class="btn btn-primary" @click="saveConfig">Save</button></div></div></dialog>
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import api from '@/services/api'
const scanners = ref<any[]>([]); const loading = ref(false); const configuring = ref<any | null>(null); const values = ref<Record<string, string>>({}); const dialog = ref<HTMLDialogElement | null>(null)
const dockerImage = ref(''); const customKey = ref(''); const customValue = ref('')
const configuredEnvs = computed(() => [
  ...(configuring.value?.required_env || []),
  ...(configuring.value?.optional_env || []).map((env: any) => ({ ...env, optional: true })),
])
const isEnabled = (scanner: any) => ['installed', 'configured', 'pulling'].includes(scanner.status)
async function load() { if (typeof api.listScanners !== 'function') return; loading.value = true; const res = await api.listScanners(); loading.value = false; if (res.success) scanners.value = res.data ?? [] }
async function toggle(scanner: any) { if (isEnabled(scanner)) await api.removeScanner(scanner.id); else await api.installScanner(scanner.id); await load() }
async function retry(scanner: any) { await api.installScanner(scanner.id); await load() }
function openConfig(scanner: any) { configuring.value = scanner; values.value = { ...(scanner.configured_env || {}) }; dockerImage.value = scanner.image_override || ''; customKey.value = ''; customValue.value = ''; dialog.value?.showModal?.() }
function addCustom() { if (!customKey.value || !customValue.value) return; values.value = { ...values.value, [customKey.value]: customValue.value }; customKey.value = ''; customValue.value = '' }
async function saveConfig() { if (!configuring.value) return; const env = Object.fromEntries(Object.entries(values.value).filter(([, value]) => value)); const res = await api.configureScanner(configuring.value.id, env, dockerImage.value || undefined); if (res.success) { dialog.value?.close(); await load() } }
function changed() { void load() }
onMounted(() => { void load(); window.addEventListener('mcpproxy:scanner-changed', changed) }); onUnmounted(() => window.removeEventListener('mcpproxy:scanner-changed', changed))
</script>
