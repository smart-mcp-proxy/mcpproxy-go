<template>
  <section class="mt-6 border-t border-base-300 pt-5" data-test="settings-scanners">
    <h3 class="font-semibold">Scanners</h3>
    <p class="text-sm text-base-content/70 mb-3">Optional deep scanners run in Docker alongside the built-in offline baseline. Deep-scan and Docker isolation controls remain in the settings above.</p>
    <div v-if="loading" class="text-sm"><span class="loading loading-spinner loading-xs"></span> Loading scanners…</div>
    <div v-else class="space-y-2">
      <div v-for="scanner in scanners" :key="scanner.id" class="flex items-center justify-between gap-3 rounded border border-base-300 p-3">
        <div><div class="font-medium">{{ scanner.name }}</div><div class="text-xs text-base-content/60">{{ scanner.description || scanner.status }}</div></div>
        <div class="flex gap-2"><button v-if="scanner.required_env?.length" class="btn btn-sm btn-ghost" @click="openConfig(scanner)">Configure</button><button class="btn btn-sm" :class="isEnabled(scanner) ? 'btn-outline' : 'btn-primary'" @click="toggle(scanner)">{{ isEnabled(scanner) ? 'Disable' : 'Enable' }}</button></div>
      </div>
      <p v-if="!scanners.length" class="text-sm text-base-content/60">No optional scanners are installed. The offline baseline remains active.</p>
    </div>
    <dialog ref="dialog" class="modal"><div class="modal-box"><h4 class="font-bold">Configure {{ configuring?.name }}</h4><p class="text-sm text-base-content/60 mt-1">Scanner secrets are stored through MCPProxy's existing secure configuration flow.</p><label v-for="env in configuring?.required_env || []" :key="env.key" class="form-control mt-3"><span class="label-text">{{ env.label || env.key }}</span><input v-model="values[env.key]" class="input input-bordered" type="password" :placeholder="env.configured ? 'Already configured' : 'Enter value'" /></label><div class="modal-action"><button class="btn" @click="dialog?.close()">Cancel</button><button class="btn btn-primary" @click="saveConfig">Save</button></div></div></dialog>
  </section>
</template>
<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import api from '@/services/api'
const scanners = ref<any[]>([]); const loading = ref(false); const configuring = ref<any | null>(null); const values = ref<Record<string, string>>({}); const dialog = ref<HTMLDialogElement | null>(null)
const isEnabled = (scanner: any) => ['installed', 'configured'].includes(scanner.status)
async function load() { if (typeof api.listScanners !== 'function') return; loading.value = true; const res = await api.listScanners(); loading.value = false; if (res.success) scanners.value = res.data ?? [] }
async function toggle(scanner: any) { if (isEnabled(scanner)) await api.removeScanner(scanner.id); else await api.installScanner(scanner.id); await load() }
function openConfig(scanner: any) { configuring.value = scanner; values.value = {}; dialog.value?.showModal() }
async function saveConfig() { if (!configuring.value) return; const env = Object.fromEntries(Object.entries(values.value).filter(([, value]) => value)); const res = await api.configureScanner(configuring.value.id, env); if (res.success) { dialog.value?.close(); await load() } }
function changed() { void load() }
onMounted(() => { void load(); window.addEventListener('mcpproxy:scanner-changed', changed) }); onUnmounted(() => window.removeEventListener('mcpproxy:scanner-changed', changed))
</script>
