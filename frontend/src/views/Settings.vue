<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Configuration</h1>
        <p class="text-base-content/70 mt-1">Edit your MCPProxy configuration directly. Changes require restart for some settings.</p>
      </div>
    </div>

    <!-- Configuration Editor -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body">
        <div class="flex justify-between items-center mb-4">
          <div>
            <h2 class="card-title">Configuration Editor</h2>
            <p class="text-sm text-base-content/70 mt-1">
              Edit your MCPProxy configuration directly. Changes require restart for some settings.
            </p>
          </div>
          <div class="flex items-center space-x-2">
            <div v-if="configStatus" :class="['badge', configStatus.valid ? 'badge-success' : 'badge-error']">
              {{ configStatus.valid ? '✓ Valid' : '✗ Invalid' }}
            </div>
            <button
              class="btn btn-sm btn-outline"
              @click="loadConfig"
              :disabled="loadingConfig"
            >
              <span v-if="loadingConfig" class="loading loading-spinner loading-xs"></span>
              <span v-else>Reload</span>
            </button>
          </div>
        </div>

        <!-- Monaco Editor -->
        <div class="border border-base-300 rounded-lg overflow-hidden" style="height: 600px;">
          <vue-monaco-editor
            v-model:value="configJson"
            language="json"
            theme="vs-dark"
            :options="editorOptions"
            @mount="handleEditorMount"
            @change="handleConfigChange"
          />
        </div>

        <!-- Validation Errors -->
        <div v-if="configErrors.length > 0" class="alert alert-error mt-4">
          <svg xmlns="http://www.w3.org/2000/svg" class="stroke-current shrink-0 h-6 w-6" fill="none" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <div>
            <h3 class="font-bold">Validation Errors</h3>
            <ul class="list-disc list-inside text-sm">
              <li v-for="(error, index) in configErrors" :key="index">
                <span class="font-mono">{{ error.field }}</span>: {{ error.message }}
              </li>
            </ul>
          </div>
        </div>

        <!-- Apply Configuration -->
        <div class="flex justify-between items-center mt-4">
          <div class="text-sm text-base-content/70">
            <span v-if="applyResult && applyResult.requires_restart" class="text-warning">
              ⚠️ {{ applyResult.restart_reason }}
            </span>
            <span v-else-if="applyResult && applyResult.applied_immediately" class="text-success">
              ✓ Configuration applied successfully
            </span>
          </div>
          <div class="flex items-center space-x-2">
            <button
              class="btn btn-outline"
              @click="validateConfig"
              :disabled="validatingConfig || !configJson"
            >
              <span v-if="validatingConfig" class="loading loading-spinner loading-sm"></span>
              Validate
            </button>
            <button
              class="btn btn-primary"
              @click="applyConfig"
              :disabled="applyingConfig || configErrors.length > 0 || !configJson"
            >
              <span v-if="applyingConfig" class="loading loading-spinner loading-sm"></span>
              Apply Configuration
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Configuration Info -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body">
        <h3 class="card-title text-sm">Configuration Tips</h3>
        <div class="text-sm text-base-content/70 space-y-2">
          <p>• Use <kbd class="kbd kbd-xs">Ctrl+Space</kbd> for autocomplete</p>
          <p>• Use <kbd class="kbd kbd-xs">Ctrl+F</kbd> to search in the configuration</p>
          <p>• Invalid JSON will be highlighted with red squiggles</p>
          <p>• <span class="font-semibold">Hot-reloadable</span>: server changes, limits, logging</p>
          <p>• <span class="font-semibold">Requires restart</span>: listen address, data directory, API key, TLS</p>
        </div>
      </div>
    </div>

    <!-- Hints Panel (Bottom of Page) -->
    <CollapsibleHintsPanel :hints="settingsHints" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { VueMonacoEditor } from '@guolao/vue-monaco-editor'
import { useServersStore } from '@/stores/servers'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import type { Hint } from '@/components/CollapsibleHintsPanel.vue'
import api from '@/services/api'

// Store references
const serversStore = useServersStore()

// Configuration editor state
const configJson = ref('')
const loadingConfig = ref(false)
const validatingConfig = ref(false)
const applyingConfig = ref(false)
const configStatus = ref<{ valid: boolean } | null>(null)
const configErrors = ref<Array<{ field: string; message: string }>>([])
const applyResult = ref<{
  success: boolean
  applied_immediately: boolean
  requires_restart: boolean
  restart_reason?: string
  changed_fields?: string[]
} | null>(null)
const editorInstance = ref<any>(null)

// Monaco editor options
const editorOptions = {
  automaticLayout: true,
  formatOnType: true,
  formatOnPaste: true,
  minimap: { enabled: false },
  scrollBeyondLastLine: false,
  fontSize: 14,
  tabSize: 2,
  wordWrap: 'on' as 'on',
  lineNumbers: 'on' as 'on',
  glyphMargin: true,
  folding: true,
  lineDecorationsWidth: 10,
  lineNumbersMinChars: 3
}

// Configuration editor methods
function handleEditorMount(editor: any) {
  editorInstance.value = editor
}

function handleConfigChange() {
  // Reset validation state on change
  configErrors.value = []
  configStatus.value = null
  applyResult.value = null

  // Try to parse JSON to check syntax
  try {
    JSON.parse(configJson.value)
    configStatus.value = { valid: true }
  } catch (e) {
    configStatus.value = { valid: false }
  }
}

async function loadConfig() {
  loadingConfig.value = true
  configErrors.value = []
  applyResult.value = null

  try {
    const response = await api.getConfig()
    if (response.success && response.data) {
      configJson.value = JSON.stringify(response.data.config, null, 2)
      configStatus.value = { valid: true }
    } else {
      configErrors.value = [{ field: 'general', message: response.error || 'Failed to load configuration' }]
    }
  } catch (error: any) {
    console.error('Failed to load config:', error)
    configErrors.value = [{ field: 'general', message: error.message || 'Failed to load configuration' }]
  } finally {
    loadingConfig.value = false
  }
}

async function validateConfig() {
  validatingConfig.value = true
  configErrors.value = []

  try {
    // Parse JSON first
    const config = JSON.parse(configJson.value)

    // Call validation endpoint
    const response = await api.validateConfig(config)
    if (response.success && response.data) {
      configErrors.value = response.data.errors || []
      configStatus.value = { valid: response.data.valid }
      if (response.data.valid) {
        console.log('Configuration validated successfully')
      }
    } else {
      configErrors.value = [{ field: 'general', message: response.error || 'Validation failed' }]
      configStatus.value = { valid: false }
    }
  } catch (error: any) {
    configErrors.value = [{ field: 'json', message: error.message || 'Invalid JSON syntax' }]
    configStatus.value = { valid: false }
  } finally {
    validatingConfig.value = false
  }
}

async function applyConfig() {
  applyingConfig.value = true
  configErrors.value = []
  applyResult.value = null

  try {
    // Parse JSON first
    const config = JSON.parse(configJson.value)

    // Call apply configuration endpoint
    const response = await api.applyConfig(config)
    if (response.success && response.data) {
      applyResult.value = response.data
      if (response.data.applied_immediately) {
        // Refresh UI data to reflect changes
        await serversStore.fetchServers()
      }
      console.log('Configuration applied successfully:', response.data)
    } else {
      configErrors.value = [{ field: 'apply', message: response.error || 'Failed to apply configuration' }]
    }
  } catch (error: any) {
    configErrors.value = [{ field: 'apply', message: error.message || 'Failed to apply configuration' }]
  } finally {
    applyingConfig.value = false
  }
}

// Settings hints
const settingsHints = computed<Hint[]>(() => {
  return [
    {
      icon: '⚙️',
      title: 'Configuration Management',
      description: 'Edit MCPProxy configuration with JSON editor',
      sections: [
        {
          title: 'Hot-Reloadable Settings',
          text: 'These settings are applied immediately without restarting:',
          list: [
            'Server enable/disable status',
            'Tool limits and search parameters',
            'Log levels and output settings',
            'Cache and timeout settings'
          ]
        },
        {
          title: 'Restart Required',
          text: 'These settings require mcpproxy restart to take effect:',
          list: [
            'Listen address (network binding)',
            'Data directory path',
            'API key authentication',
            'TLS/HTTPS configuration'
          ]
        }
      ]
    },
    {
      icon: '🔧',
      title: 'CLI Configuration Tools',
      description: 'Manage configuration from the command line',
      sections: [
        {
          title: 'View current configuration',
          codeBlock: {
            language: 'bash',
            code: `# View configuration location\nmcpproxy config path\n\n# Dump current config\ncat ~/.mcpproxy/mcp_config.json`
          }
        },
        {
          title: 'Backup configuration',
          codeBlock: {
            language: 'bash',
            code: `# Create backup\ncp ~/.mcpproxy/mcp_config.json ~/.mcpproxy/mcp_config.backup.json`
          }
        }
      ]
    },
    {
      icon: '💡',
      title: 'Configuration Tips',
      description: 'Best practices for managing MCPProxy config',
      sections: [
        {
          title: 'Editor features',
          list: [
            'Use Ctrl+Space for autocomplete suggestions',
            'Use Ctrl+F to search within the configuration',
            'Invalid JSON is highlighted with red squiggles',
            'Format with Ctrl+Shift+F (or Cmd+Shift+F on Mac)'
          ]
        },
        {
          title: 'Version control',
          text: 'Consider tracking your configuration in git (excluding secrets):',
          codeBlock: {
            language: 'bash',
            code: `# Initialize git repo for configs\ncd ~/.mcpproxy\ngit init\necho "*.db" >> .gitignore\necho "*.bleve/" >> .gitignore\ngit add mcp_config.json\ngit commit -m "Initial MCPProxy configuration"`
          }
        }
      ]
    }
  ]
})

// Initialize component
onMounted(() => {
  loadConfig() // Load configuration when component mounts
})
</script>
