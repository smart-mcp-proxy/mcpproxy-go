<template>
  <dialog :open="show" class="modal">
    <div class="modal-box max-w-2xl">
      <form @submit.prevent="handleSubmit">
        <h3 class="font-bold text-lg mb-4">Add New Server</h3>

        <!-- Server Type Selection -->
        <div class="form-control mb-4">
          <label class="label">
            <span class="label-text font-semibold">Server Type</span>
          </label>
          <div class="flex gap-4">
            <label class="flex items-center space-x-2 cursor-pointer">
              <input
                type="radio"
                name="serverType"
                value="stdio"
                v-model="formData.type"
                class="radio radio-primary"
              />
              <span>stdio (Local Command)</span>
            </label>
            <label class="flex items-center space-x-2 cursor-pointer">
              <input
                type="radio"
                name="serverType"
                value="http"
                v-model="formData.type"
                class="radio radio-primary"
              />
              <span>HTTP/HTTPS (Remote)</span>
            </label>
          </div>
        </div>

        <!-- Common Fields -->
        <div class="form-control mb-4">
          <label class="label">
            <span class="label-text font-semibold">Server Name</span>
          </label>
          <input
            type="text"
            v-model="formData.name"
            placeholder="e.g., github-server"
            class="input input-bordered"
            required
          />
        </div>

        <!-- HTTP/HTTPS Fields -->
        <div v-if="formData.type === 'http'" class="space-y-4">
          <div class="form-control">
            <label class="label">
              <span class="label-text font-semibold">URL</span>
            </label>
            <input
              type="url"
              v-model="formData.url"
              placeholder="https://api.example.com/mcp"
              class="input input-bordered"
              required
            />
          </div>
        </div>

        <!-- stdio Fields -->
        <div v-if="formData.type === 'stdio'" class="space-y-4">
          <div class="form-control">
            <label class="label">
              <span class="label-text font-semibold">Command</span>
            </label>
            <select v-model="formData.command" class="select select-bordered" required>
              <option value="">Select command</option>
              <option value="npx">npx (Node.js)</option>
              <option value="uvx">uvx (Python)</option>
              <option value="node">node</option>
              <option value="python">python</option>
              <option value="custom">Custom command</option>
            </select>
          </div>

          <div v-if="formData.command === 'custom'" class="form-control">
            <label class="label">
              <span class="label-text font-semibold">Custom Command Path</span>
            </label>
            <input
              type="text"
              v-model="formData.customCommand"
              placeholder="/usr/local/bin/my-mcp-server"
              class="input input-bordered"
              required
            />
          </div>

          <div class="form-control">
            <label class="label">
              <span class="label-text font-semibold">Arguments</span>
              <span class="label-text-alt">One per line</span>
            </label>
            <textarea
              v-model="formData.argsText"
              placeholder="@modelcontextprotocol/server-filesystem"
              class="textarea textarea-bordered h-24"
              rows="3"
            ></textarea>
          </div>

          <div class="form-control">
            <label class="label">
              <span class="label-text font-semibold">Environment Variables</span>
              <span class="label-text-alt">KEY=value format, one per line</span>
            </label>
            <textarea
              v-model="formData.envText"
              placeholder="API_KEY=your-key&#10;DEBUG=true"
              class="textarea textarea-bordered h-24"
              rows="3"
            ></textarea>
          </div>

          <div class="form-control">
            <label class="label">
              <span class="label-text font-semibold">Working Directory (Optional)</span>
            </label>
            <input
              type="text"
              v-model="formData.workingDir"
              placeholder="/path/to/project"
              class="input input-bordered"
            />
          </div>
        </div>

        <!-- Toggles Section -->
        <div class="divider mt-6">Options</div>

        <div class="space-y-3">
          <!-- Enabled -->
          <div class="form-control">
            <label class="label cursor-pointer justify-start space-x-3">
              <input
                type="checkbox"
                v-model="formData.enabled"
                class="toggle toggle-primary"
              />
              <span class="label-text font-semibold">Enabled</span>
              <div class="tooltip tooltip-right" data-tip="Start this server immediately after adding">
                <svg class="w-4 h-4 opacity-60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
              </div>
            </label>
          </div>

          <!-- Quarantined -->
          <div class="form-control">
            <label class="label cursor-pointer justify-start space-x-3">
              <input
                type="checkbox"
                v-model="formData.quarantined"
                class="toggle toggle-warning"
              />
              <span class="label-text font-semibold">Quarantined</span>
              <div class="tooltip tooltip-right" data-tip="Prevent tool execution until security review is complete. Recommended for new servers.">
                <svg class="w-4 h-4 opacity-60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
              </div>
            </label>
          </div>

          <!-- Isolated (Docker) -->
          <div class="form-control">
            <label class="label cursor-pointer justify-start space-x-3">
              <input
                type="checkbox"
                v-model="formData.isolated"
                class="toggle toggle-info"
                :disabled="formData.type !== 'stdio'"
              />
              <span class="label-text font-semibold">Docker Isolation</span>
              <div class="tooltip tooltip-right" data-tip="Run stdio server in isolated Docker container for enhanced security (stdio only)">
                <svg class="w-4 h-4 opacity-60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
              </div>
            </label>
          </div>

          <!-- Idle on Inactivity -->
          <div class="form-control">
            <label class="label cursor-pointer justify-start space-x-3">
              <input
                type="checkbox"
                v-model="formData.idleOnInactivity"
                class="toggle toggle-success"
                disabled
              />
              <span class="label-text font-semibold opacity-50">Idle on Inactivity</span>
              <div class="tooltip tooltip-right" data-tip="Future feature: Automatically stop server after period of inactivity to save resources">
                <svg class="w-4 h-4 opacity-60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
              </div>
            </label>
            <span class="text-xs opacity-50 ml-12">Coming soon</span>
          </div>
        </div>

        <!-- Error Display -->
        <div v-if="error" class="alert alert-error mt-4">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <span>{{ error }}</span>
        </div>

        <!-- Actions -->
        <div class="modal-action">
          <button type="button" @click="handleClose" class="btn btn-ghost">Cancel</button>
          <button type="submit" class="btn btn-primary" :disabled="loading">
            <span v-if="loading" class="loading loading-spinner loading-sm"></span>
            {{ loading ? 'Adding...' : 'Add Server' }}
          </button>
        </div>
      </form>
    </div>
    <form method="dialog" class="modal-backdrop" @click="handleClose">
      <button>close</button>
    </form>
  </dialog>
</template>

<script setup lang="ts">
import { ref, reactive, watch } from 'vue'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
  (e: 'added'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const serversStore = useServersStore()
const systemStore = useSystemStore()

const formData = reactive({
  type: 'stdio' as 'stdio' | 'http',
  name: '',
  url: '',
  command: '',
  customCommand: '',
  argsText: '',
  envText: '',
  workingDir: '',
  enabled: true,
  quarantined: true,
  isolated: false,
  idleOnInactivity: false
})

const loading = ref(false)
const error = ref('')

// Reset isolated when type changes
watch(() => formData.type, (newType) => {
  if (newType !== 'stdio') {
    formData.isolated = false
  }
})

function parseArgs(): string[] {
  if (!formData.argsText.trim()) return []
  return formData.argsText.split('\n').map(line => line.trim()).filter(line => line)
}

function parseEnv(): Record<string, string> {
  if (!formData.envText.trim()) return {}
  const env: Record<string, string> = {}
  formData.envText.split('\n').forEach(line => {
    const trimmed = line.trim()
    if (!trimmed) return
    const [key, ...valueParts] = trimmed.split('=')
    if (key && valueParts.length > 0) {
      env[key.trim()] = valueParts.join('=').trim()
    }
  })
  return env
}

async function handleSubmit() {
  error.value = ''
  loading.value = true

  try {
    const command = formData.command === 'custom' ? formData.customCommand : formData.command
    const args = parseArgs()
    const env = parseEnv()

    const serverData: any = {
      operation: 'add',
      name: formData.name,
      protocol: formData.type,
      enabled: formData.enabled,
      quarantined: formData.quarantined
    }

    if (formData.type === 'http') {
      serverData.url = formData.url
    } else {
      serverData.command = command
      if (args.length > 0) {
        serverData.args_json = JSON.stringify(args)
      }
      if (Object.keys(env).length > 0) {
        serverData.env_json = JSON.stringify(env)
      }
      if (formData.workingDir) {
        serverData.working_dir = formData.workingDir
      }
      if (formData.isolated) {
        serverData.isolation_json = JSON.stringify({ enabled: true })
      }
    }

    await serversStore.addServer(serverData)

    systemStore.addToast({
      type: 'success',
      title: 'Server Added',
      message: `${formData.name} has been added successfully`
    })

    emit('added')
    handleClose()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to add server'
  } finally {
    loading.value = false
  }
}

function handleClose() {
  // Reset form
  formData.type = 'stdio'
  formData.name = ''
  formData.url = ''
  formData.command = ''
  formData.customCommand = ''
  formData.argsText = ''
  formData.envText = ''
  formData.workingDir = ''
  formData.enabled = true
  formData.quarantined = true
  formData.isolated = false
  formData.idleOnInactivity = false
  error.value = ''

  emit('close')
}
</script>
