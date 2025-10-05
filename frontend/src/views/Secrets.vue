<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Secrets & Environment Variables</h1>
        <p class="text-base-content/70 mt-1">Manage secrets stored in your system's secure keyring and environment variables</p>
      </div>
      <div class="flex items-center space-x-2">
        <button
          @click="refreshSecrets"
          :disabled="loading"
          class="btn btn-outline"
        >
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
          <span v-if="loading" class="loading loading-spinner loading-sm"></span>
          {{ loading ? 'Refreshing...' : 'Refresh' }}
        </button>
        <button @click="showAddModal = true" class="btn btn-primary">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
          </svg>
          Add Secret
        </button>
      </div>
    </div>

    <!-- Summary Stats -->
    <div class="stats shadow bg-base-100 w-full">
      <div class="stat">
        <div class="stat-title">Keyring Secrets</div>
        <div class="stat-value">{{ configSecrets?.total_secrets || 0 }}</div>
        <div class="stat-desc">Stored in system keyring</div>
      </div>

      <div class="stat">
        <div class="stat-title">Environment Variables</div>
        <div class="stat-value text-info">{{ configSecrets?.total_env_vars || 0 }}</div>
        <div class="stat-desc">Referenced in config</div>
      </div>

      <div class="stat">
        <div class="stat-title">Missing Env Vars</div>
        <div class="stat-value text-warning">{{ missingEnvVars }}</div>
        <div class="stat-desc">Need to be set</div>
      </div>

      <div class="stat">
        <div class="stat-title">Migration Candidates</div>
        <div class="stat-value text-error">{{ migrationCandidates.length }}</div>
        <div class="stat-desc">Potential secrets to secure</div>
      </div>
    </div>

    <!-- Filters -->
    <div class="flex flex-wrap gap-4 items-center justify-between">
      <div class="flex flex-wrap gap-2">
        <button
          @click="filter = 'all'"
          :class="['btn btn-sm', filter === 'all' ? 'btn-primary' : 'btn-outline']"
        >
          All ({{ totalItems }})
        </button>
        <button
          @click="filter = 'secrets'"
          :class="['btn btn-sm', filter === 'secrets' ? 'btn-primary' : 'btn-outline']"
        >
          Keyring Secrets ({{ configSecrets?.total_secrets || 0 }})
        </button>
        <button
          @click="filter = 'envs'"
          :class="['btn btn-sm', filter === 'envs' ? 'btn-primary' : 'btn-outline']"
        >
          Environment Variables ({{ configSecrets?.total_env_vars || 0 }})
        </button>
        <button
          @click="filter = 'missing'"
          :class="['btn btn-sm', filter === 'missing' ? 'btn-primary' : 'btn-outline']"
        >
          Missing ({{ missingEnvVars }})
        </button>
      </div>

      <div class="form-control">
        <input
          v-model="searchQuery"
          type="text"
          placeholder="Search secrets..."
          class="input input-bordered input-sm w-64"
        />
      </div>
    </div>

    <!-- Loading State -->
    <div v-if="loading" class="text-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
      <p class="mt-4">Loading secrets...</p>
    </div>

    <!-- Error State -->
    <div v-else-if="error" class="alert alert-error">
      <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <div>
        <h3 class="font-bold">Failed to load secrets</h3>
        <div class="text-sm">{{ error }}</div>
      </div>
      <button @click="refreshSecrets" class="btn btn-sm">
        Try Again
      </button>
    </div>

    <!-- Empty State -->
    <div v-else-if="filteredItems.length === 0" class="text-center py-12">
      <svg class="w-24 h-24 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
      </svg>
      <h3 class="text-xl font-semibold mb-2">No secrets found</h3>
      <p class="text-base-content/70 mb-4">
        {{ searchQuery ? 'No secrets match your search criteria' : `No ${filter === 'all' ? '' : filter} secrets available`.replace(/\s+/g, ' ').trim() }}
      </p>
      <button v-if="searchQuery" @click="searchQuery = ''" class="btn btn-outline">
        Clear Search
      </button>
    </div>

    <!-- Secrets List -->
    <div v-else class="space-y-4">
      <!-- Keyring Secrets -->
      <div v-for="secret in filteredSecrets" :key="secret.name" class="card bg-base-100 shadow">
        <div class="card-body">
          <div class="flex justify-between items-start">
            <div class="flex-1">
              <h3 class="card-title text-lg">{{ secret.name }}</h3>
              <div class="flex items-center gap-2 mt-2">
                <span class="badge badge-primary">Keyring</span>
                <code class="text-sm bg-base-200 px-2 py-1 rounded">{{ secret.original }}</code>
              </div>
            </div>
            <div class="flex gap-2">
              <button @click="testSecret(secret)" class="btn btn-sm btn-outline">
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
                Test
              </button>
              <button @click="deleteSecret(secret)" class="btn btn-sm btn-error btn-outline">
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                </svg>
                Delete
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- Environment Variables -->
      <div v-for="envVar in filteredEnvVars" :key="envVar.secret_ref.name" class="card bg-base-100 shadow" :class="{ 'border-l-4 border-error': !envVar.is_set }">
        <div class="card-body">
          <div class="flex justify-between items-start">
            <div class="flex-1">
              <h3 class="card-title text-lg">{{ envVar.secret_ref.name }}</h3>
              <div class="flex items-center gap-2 mt-2">
                <span class="badge badge-info">Environment Variable</span>
                <span v-if="envVar.is_set" class="badge badge-success">✓ Set</span>
                <span v-else class="badge badge-error">✗ Missing</span>
                <code class="text-sm bg-base-200 px-2 py-1 rounded">{{ envVar.secret_ref.original }}</code>
              </div>
            </div>
            <div class="flex gap-2">
              <button @click="testEnvVar(envVar)" class="btn btn-sm btn-outline">
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
                Test
              </button>
              <button v-if="!envVar.is_set" @click="setEnvVarHelp(envVar)" class="btn btn-sm btn-warning btn-outline">
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8.228 9c.549-1.165 2.03-2 3.772-2 2.21 0 4 1.343 4 3 0 1.4-1.278 2.575-3.006 2.907-.542.104-.994.54-.994 1.093m0 3h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
                Help
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Migration Candidates Section -->
    <div v-if="migrationCandidates.length > 0" class="card bg-base-100 shadow">
      <div class="card-body">
        <div class="flex justify-between items-center mb-4">
          <h2 class="card-title">Migration Candidates</h2>
          <button @click="runMigrationAnalysis" class="btn btn-sm btn-outline" :disabled="analysisLoading">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
            </svg>
            {{ analysisLoading ? 'Analyzing...' : 'Re-analyze' }}
          </button>
        </div>
        <p class="text-sm text-base-content/70 mb-4">
          These configuration values appear to be secrets that could be migrated to secure storage.
        </p>
        <div class="space-y-3">
          <div
            v-for="(candidate, index) in migrationCandidates"
            :key="index"
            class="alert"
            :class="{
              'alert-success': candidate.confidence >= 0.8,
              'alert-warning': candidate.confidence >= 0.6 && candidate.confidence < 0.8,
              'alert-error': candidate.confidence < 0.6
            }"
          >
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
            </svg>
            <div class="flex-1">
              <div class="font-bold">{{ candidate.field }}</div>
              <div class="text-sm opacity-70">{{ candidate.value }}</div>
              <div class="text-sm mt-1">
                Suggested: <code class="bg-base-200 px-2 py-1 rounded">{{ candidate.suggested }}</code>
                <span class="ml-2 opacity-60">({{ Math.round(candidate.confidence * 100) }}% confidence)</span>
              </div>
            </div>
            <button
              @click="migrateSecret(candidate)"
              class="btn btn-sm btn-primary"
              :disabled="candidate.migrating"
            >
              {{ candidate.migrating ? 'Migrating...' : 'Store in Keychain' }}
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Hints Panel (Bottom of Page) -->
    <CollapsibleHintsPanel :hints="secretsHints" />

    <!-- Add Secret Modal -->
    <AddSecretModal
      :show="showAddModal"
      @close="showAddModal = false"
      @added="handleSecretAdded"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import apiClient from '@/services/api'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import AddSecretModal from '@/components/AddSecretModal.vue'
import { useSystemStore } from '@/stores/system'
import type { Hint } from '@/components/CollapsibleHintsPanel.vue'
import type { SecretRef, MigrationCandidate, ConfigSecretsResponse, EnvVarStatus } from '@/types'

const systemStore = useSystemStore()

const loading = ref(true)
const error = ref<string | null>(null)
const configSecrets = ref<ConfigSecretsResponse | null>(null)
const migrationCandidates = ref<MigrationCandidate[]>([])
const analysisLoading = ref(false)
const filter = ref<'all' | 'secrets' | 'envs' | 'missing'>('all')
const searchQuery = ref('')
const showAddModal = ref(false)

const missingEnvVars = computed(() => {
  return configSecrets.value?.environment_vars?.filter(env => !env.is_set).length || 0
})

const totalItems = computed(() => {
  return (configSecrets.value?.total_secrets || 0) + (configSecrets.value?.total_env_vars || 0)
})

const filteredSecrets = computed(() => {
  if (filter.value === 'envs' || filter.value === 'missing') return []

  let secrets = configSecrets.value?.secrets || []

  if (searchQuery.value) {
    const query = searchQuery.value.toLowerCase()
    secrets = secrets.filter(s =>
      s.name.toLowerCase().includes(query) ||
      s.original.toLowerCase().includes(query)
    )
  }

  return secrets
})

const filteredEnvVars = computed(() => {
  if (filter.value === 'secrets') return []

  let envVars = configSecrets.value?.environment_vars || []

  if (filter.value === 'missing') {
    envVars = envVars.filter(e => !e.is_set)
  }

  if (searchQuery.value) {
    const query = searchQuery.value.toLowerCase()
    envVars = envVars.filter(e =>
      e.secret_ref.name.toLowerCase().includes(query) ||
      e.secret_ref.original.toLowerCase().includes(query)
    )
  }

  return envVars
})

const filteredItems = computed(() => {
  return [...filteredSecrets.value, ...filteredEnvVars.value]
})

const loadConfigSecrets = async () => {
  loading.value = true
  error.value = null

  try {
    const response = await apiClient.getConfigSecrets()
    if (response.success && response.data) {
      configSecrets.value = response.data
    } else {
      error.value = response.error || 'Failed to load config secrets'
    }
  } catch (err: any) {
    error.value = err.message || 'Failed to load config secrets'
    console.error('Failed to load config secrets:', err)
  } finally {
    loading.value = false
  }
}

const refreshSecrets = loadConfigSecrets

const runMigrationAnalysis = async () => {
  analysisLoading.value = true

  try {
    const response = await apiClient.runMigrationAnalysis()
    if (response.success && response.data) {
      migrationCandidates.value = response.data.analysis.candidates || []

      systemStore.addToast({
        type: 'success',
        title: 'Analysis Complete',
        message: `Found ${migrationCandidates.value.length} migration candidates`
      })
    } else {
      error.value = response.error || 'Failed to run migration analysis'
    }
  } catch (err: any) {
    error.value = err.message || 'Failed to run migration analysis'
    console.error('Failed to run migration analysis:', err)
  } finally {
    analysisLoading.value = false
  }
}

const testSecret = async (ref: SecretRef) => {
  systemStore.addToast({
    type: 'info',
    title: 'Secret Available',
    message: `Secret "${ref.name}" is available in ${ref.type}`
  })
}

const deleteSecret = async (ref: SecretRef) => {
  if (!confirm(`Are you sure you want to delete secret "${ref.name}"?`)) {
    return
  }

  try {
    const response = await apiClient.deleteSecret(ref.name, ref.type)
    if (response.success) {
      systemStore.addToast({
        type: 'success',
        title: 'Secret Deleted',
        message: `Secret "${ref.name}" deleted successfully`
      })

      await loadConfigSecrets()
    } else {
      systemStore.addToast({
        type: 'error',
        title: 'Delete Failed',
        message: response.error || 'Failed to delete secret'
      })
    }
  } catch (err: any) {
    systemStore.addToast({
      type: 'error',
      title: 'Delete Failed',
      message: err.message || 'Failed to delete secret'
    })
  }
}

const migrateSecret = async (candidate: MigrationCandidate) => {
  candidate.migrating = true

  try {
    const match = candidate.suggested.match(/\$\{keyring:([^}]+)\}/)
    if (!match) {
      throw new Error('Invalid suggested reference format')
    }

    const secretName = match[1]

    systemStore.addToast({
      type: 'info',
      title: 'Migration Instructions',
      message: `Run: mcpproxy secrets set ${secretName}\nThen update config to use: ${candidate.suggested}`
    })
  } catch (err: any) {
    systemStore.addToast({
      type: 'error',
      title: 'Migration Failed',
      message: err.message
    })
  } finally {
    candidate.migrating = false
  }
}

const testEnvVar = async (envVar: EnvVarStatus) => {
  systemStore.addToast({
    type: envVar.is_set ? 'success' : 'warning',
    title: `Environment Variable ${envVar.is_set ? 'Set' : 'Missing'}`,
    message: `"${envVar.secret_ref.name}" is ${envVar.is_set ? 'set' : 'NOT set'}`
  })
}

const setEnvVarHelp = async (envVar: EnvVarStatus) => {
  const instructions = `To set "${envVar.secret_ref.name}":\n\nmacOS/Linux: export ${envVar.secret_ref.name}="your-value"\nWindows (PS): $env:${envVar.secret_ref.name}="your-value"\nWindows (CMD): set ${envVar.secret_ref.name}=your-value`

  systemStore.addToast({
    type: 'info',
    title: 'Set Environment Variable',
    message: instructions
  })
}

const handleSecretAdded = async () => {
  await loadConfigSecrets()
}

// Secrets hints
const secretsHints = computed<Hint[]>(() => {
  return [
    {
      icon: '🔐',
      title: 'Store Secrets Securely',
      description: 'Use the system keyring to store sensitive values',
      sections: [
        {
          title: 'Add a secret via CLI',
          codeBlock: {
            language: 'bash',
            code: `# Store a secret in system keyring\nmcpproxy secrets set my-api-key`
          }
        },
        {
          title: 'Add a secret via Web UI',
          text: 'Click the "Add Secret" button above to add secrets through the web interface'
        },
        {
          title: 'Use in configuration',
          codeBlock: {
            language: 'json',
            code: `{\n  "env": {\n    "API_KEY": "\${keyring:my-api-key}"\n  }\n}`
          }
        }
      ]
    },
    {
      icon: '🌍',
      title: 'Environment Variables',
      description: 'Reference environment variables in your configuration',
      sections: [
        {
          title: 'Use environment variables',
          codeBlock: {
            language: 'json',
            code: `{\n  "env": {\n    "API_KEY": "\${env:MY_API_KEY}"\n  }\n}`
          }
        },
        {
          title: 'Set environment variables',
          codeBlock: {
            language: 'bash',
            code: `# macOS/Linux\nexport MY_API_KEY="your-value"\n\n# Windows PowerShell\n$env:MY_API_KEY="your-value"`
          }
        }
      ]
    },
    {
      icon: '🔄',
      title: 'Migrate Existing Secrets',
      description: 'Find and migrate hardcoded secrets to secure storage',
      sections: [
        {
          title: 'Run migration analysis',
          text: 'MCPProxy can scan your configuration and identify potential secrets that should be moved to secure storage. Click the "Analyze Configuration" button to find migration candidates.'
        },
        {
          title: 'Automatic detection',
          text: 'The analyzer looks for patterns like API keys, tokens, passwords, and other sensitive values that might be hardcoded in your configuration.'
        }
      ]
    }
  ]
})

onMounted(async () => {
  // Give the API service time to initialize the API key from URL params
  await new Promise(resolve => setTimeout(resolve, 100))
  loadConfigSecrets()
})
</script>
