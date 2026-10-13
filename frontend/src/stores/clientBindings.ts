import { defineStore } from 'pinia'
import { reactive, ref } from 'vue'
import api from '@/services/api'
import { useClientsStore } from '@/stores/clients'
import type { ClientView } from '@/types/api'

// Spec 108-i: the client credential actions behind the Clients page. The rows
// themselves stay in stores/clients.ts; this store owns what an action leaves
// behind - the per-row refusal to show under the row, and which rows a request
// is in flight for.
//
// It never holds a credential secret. The one-time `mcp_cli_...` value a custom
// client or a rotation returns is handed straight to the caller, which keeps it
// in component state for the life of one dialog and clears it on close.
export const useClientBindingsStore = defineStore('clientBindings', () => {
  const clients = useClientsStore()
  const rowErrors = reactive<Record<string, unknown>>({})
  const busy = reactive<Record<string, boolean>>({})
  const lastError = ref<unknown>(null)

  function clearRowError(id: string) { delete rowErrors[id] }

  // Moves one client to a profile (PUT binding). `mode` is omitted unless the
  // caller means to change it: omitted keeps the credential's current mode.
  async function setBinding(id: string, body: { profile: string; mode?: 'locked' | 'switchable' }): Promise<ClientView | null> {
    busy[id] = true
    clearRowError(id)
    try {
      const result = await api.setClientBinding(id, body)
      clients.replaceRow(result.client)
      void clients.refreshPresence()
      return result.client
    } catch (err) {
      rowErrors[id] = err
      lastError.value = err
      return null
    } finally {
      busy[id] = false
    }
  }

  async function finalizeRotation(id: string): Promise<boolean> {
    busy[id] = true
    clearRowError(id)
    try {
      const result = await api.finalizeClientRotation(id)
      clients.replaceRow(result.client)
      return true
    } catch (err) {
      rowErrors[id] = err
      return false
    } finally {
      busy[id] = false
    }
  }

  return { rowErrors, busy, lastError, clearRowError, setBinding, finalizeRotation }
})
