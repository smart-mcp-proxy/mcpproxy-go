<template>
  <div class="secrets-page">
    <div class="page-header">
      <h1 class="page-title">Secrets & Environment Variables</h1>
      <p class="page-description">
        Manage secrets stored in your system's secure keyring and environment variables referenced in your configuration.
      </p>
    </div>

    <!-- Loading State -->
    <div v-if="loading" class="loading-state">
      <div class="spinner"></div>
      <p>Loading secrets...</p>
    </div>

    <!-- Error State -->
    <div v-else-if="error" class="error-state">
      <div class="error-icon">⚠️</div>
      <h3>Error Loading Secrets</h3>
      <p>{{ error }}</p>
      <button @click="loadSecrets" class="retry-button">Retry</button>
    </div>

    <!-- Main Content -->
    <div v-else class="secrets-content">
      <!-- Statistics -->
      <div class="stats-section">
        <div class="stat-card">
          <div class="stat-number">{{ configSecrets?.total_secrets || 0 }}</div>
          <div class="stat-label">Keyring Secrets</div>
        </div>
        <div class="stat-card">
          <div class="stat-number">{{ configSecrets?.total_env_vars || 0 }}</div>
          <div class="stat-label">Environment Variables</div>
        </div>
        <div class="stat-card">
          <div class="stat-number">{{ missingEnvVars }}</div>
          <div class="stat-label">Missing Env Vars</div>
        </div>
        <div class="stat-card">
          <div class="stat-number">{{ migrationCandidates.length }}</div>
          <div class="stat-label">Migration Candidates</div>
        </div>
      </div>

      <!-- Actions Bar -->
      <div class="actions-bar">
        <button @click="loadConfigSecrets" class="action-button secondary">
          🔄 Refresh
        </button>
        <button @click="runMigrationAnalysis" class="action-button secondary" :disabled="analysisLoading">
          🔍 {{ analysisLoading ? 'Analyzing...' : 'Analyze Configuration' }}
        </button>
        <button @click="showAddSecretForm = !showAddSecretForm" class="action-button">
          {{ showAddSecretForm ? '✕ Cancel' : '➕ Add Secret' }}
        </button>
      </div>

      <!-- Add Secret Form -->
      <div v-if="showAddSecretForm" class="add-secret-form">
        <h3>Add New Secret</h3>
        <form @submit.prevent="addSecret">
          <div class="form-row">
            <div class="form-group">
              <label for="secret-name">Secret Name</label>
              <input
                id="secret-name"
                v-model="newSecret.name"
                type="text"
                placeholder="e.g. my-api-key"
                required
                class="form-input"
              />
              <small class="form-hint">Use only letters, numbers, and hyphens</small>
            </div>
            <div class="form-group">
              <label for="secret-value">Secret Value</label>
              <input
                id="secret-value"
                v-model="newSecret.value"
                type="password"
                placeholder="Enter secret value"
                required
                class="form-input"
              />
            </div>
          </div>
          <div class="form-actions">
            <button
              type="submit"
              class="action-button"
              :disabled="addingSecret || !newSecret.name || !newSecret.value"
            >
              {{ addingSecret ? 'Adding...' : 'Add Secret' }}
            </button>
            <button type="button" @click="cancelAddSecret" class="action-button secondary">
              Cancel
            </button>
          </div>
          <div class="form-preview" v-if="newSecret.name">
            <strong>Configuration reference:</strong>
            <code>${keyring:{{ newSecret.name }}}</code>
          </div>
        </form>
      </div>

      <!-- Tabs -->
      <div class="tabs-container">
        <div class="tabs-header">
          <button
            @click="activeTab = 'secrets'"
            :class="['tab-button', { active: activeTab === 'secrets' }]"
          >
            Secrets ({{ configSecrets?.total_secrets || 0 }})
          </button>
          <button
            @click="activeTab = 'envs'"
            :class="['tab-button', { active: activeTab === 'envs' }]"
          >
            Environment Variables ({{ configSecrets?.total_env_vars || 0 }})
          </button>
        </div>

        <div class="tabs-content">
          <!-- Secrets Tab -->
          <div v-if="activeTab === 'secrets'" class="tab-panel">
            <div class="section">
              <h2 class="section-title">Keyring Secrets Referenced in Configuration</h2>
              <div v-if="!configSecrets?.secrets || configSecrets.secrets.length === 0" class="empty-state">
                <div class="empty-icon">🔐</div>
                <h3>No Keyring Secrets Referenced</h3>
                <p>No keyring secret references are currently used in your configuration.</p>
                <p>Use the form below to store secrets or use the CLI: <code>mcpproxy secrets set &lt;name&gt;</code></p>
                <p>Then reference them in config: <code>${keyring:name}</code></p>
              </div>
              <div v-else class="secrets-list">
                <div v-for="ref in configSecrets.secrets" :key="ref.name" class="secret-item">
                  <div class="secret-info">
                    <div class="secret-name">{{ ref.name }}</div>
                    <div class="secret-type">{{ ref.type }}</div>
                    <div class="secret-ref">{{ ref.original }}</div>
                  </div>
                  <div class="secret-actions">
                    <button @click="testSecret(ref)" class="action-button small">Test</button>
                    <button @click="deleteSecret(ref)" class="action-button small danger">Delete</button>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- Environment Variables Tab -->
          <div v-if="activeTab === 'envs'" class="tab-panel">
            <div class="section">
              <h2 class="section-title">Environment Variables Referenced in Configuration</h2>
              <div v-if="!configSecrets?.environment_vars?.length" class="empty-state">
                <div class="empty-icon">🌍</div>
                <h3>No Environment Variables Referenced</h3>
                <p>No environment variables are currently referenced in your configuration.</p>
                <p>Reference them in config: <code>${env:VARIABLE_NAME}</code></p>
              </div>
              <div v-else class="env-vars-list">
                <div v-for="envVar in configSecrets.environment_vars" :key="envVar.secret_ref.name" class="env-var-item" :class="{ 'missing': !envVar.is_set }">
                  <div class="env-var-info">
                    <div class="env-var-name">{{ envVar.secret_ref.name }}</div>
                    <div class="env-var-status">
                      <span v-if="envVar.is_set" class="status-badge set">✅ Set</span>
                      <span v-else class="status-badge missing">❌ Missing</span>
                    </div>
                    <div class="env-var-ref">{{ envVar.secret_ref.original }}</div>
                  </div>
                  <div class="env-var-actions">
                    <button @click="testEnvVar(envVar)" class="action-button small">Test</button>
                    <button v-if="!envVar.is_set" @click="setEnvVarHelp(envVar)" class="action-button small warning">Help</button>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Migration Candidates -->
      <div v-if="migrationCandidates.length > 0" class="section">
        <h2 class="section-title">
          Migration Candidates
          <span class="confidence-legend">
            <span class="confidence-item">
              <span class="confidence-dot high"></span>High Confidence
            </span>
            <span class="confidence-item">
              <span class="confidence-dot medium"></span>Medium Confidence
            </span>
            <span class="confidence-item">
              <span class="confidence-dot low"></span>Low Confidence
            </span>
          </span>
        </h2>
        <p class="section-description">
          These configuration values appear to be secrets that could be migrated to secure storage.
        </p>
        <div class="migration-list">
          <div
            v-for="(candidate, index) in migrationCandidates"
            :key="index"
            class="migration-item"
            :class="getConfidenceClass(candidate.confidence)"
          >
            <div class="migration-info">
              <div class="migration-field">{{ candidate.field }}</div>
              <div class="migration-value">{{ candidate.value }}</div>
              <div class="migration-suggestion">
                Suggested: <code>{{ candidate.suggested }}</code>
              </div>
              <div class="migration-confidence">
                Confidence: {{ Math.round(candidate.confidence * 100) }}%
              </div>
            </div>
            <div class="migration-actions">
              <button
                @click="migrateSecret(candidate)"
                class="action-button small"
                :disabled="candidate.migrating"
              >
                {{ candidate.migrating ? 'Migrating...' : 'Store in Keychain' }}
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- Help Section -->
      <div class="section help-section">
        <h2 class="section-title">How to Use Secrets</h2>
        <div class="help-content">
          <div class="help-item">
            <h4>Store a Secret</h4>
            <p>Use the CLI to store secrets securely:</p>
            <code>mcpproxy secrets set my-api-key</code>
          </div>
          <div class="help-item">
            <h4>Use in Configuration</h4>
            <p>Reference secrets in your config using the format:</p>
            <code>${keyring:my-api-key}</code>
          </div>
          <div class="help-item">
            <h4>Environment Variables</h4>
            <p>You can also reference environment variables:</p>
            <code>${env:MY_API_KEY}</code>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import apiClient from '@/services/api'
import type { SecretRef, MigrationCandidate, MigrationAnalysis, ConfigSecretsResponse, EnvVarStatus } from '@/types'

const loading = ref(true)
const error = ref<string | null>(null)
const configSecrets = ref<ConfigSecretsResponse | null>(null)
const migrationCandidates = ref<MigrationCandidate[]>([])
const analysisLoading = ref(false)
const activeTab = ref('secrets')
const showAddSecretForm = ref(false)
const addingSecret = ref(false)
const newSecret = ref({
  name: '',
  value: ''
})

const missingEnvVars = computed(() => {
  return configSecrets.value?.environment_vars?.filter(env => !env.is_set).length || 0
})

const loadConfigSecrets = async () => {
  loading.value = true
  error.value = null

  try {
    const response = await apiClient.getConfigSecrets()
    console.log('Config secrets response:', response) // Debug log
    if (response.success && response.data) {
      configSecrets.value = response.data
      console.log('Loaded config secrets:', configSecrets.value) // Debug log
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

const loadSecrets = loadConfigSecrets // Alias for compatibility

const runMigrationAnalysis = async () => {
  analysisLoading.value = true

  try {
    const response = await apiClient.runMigrationAnalysis()
    if (response.success && response.data) {
      migrationCandidates.value = response.data.analysis.candidates || []
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
  // For security, we don't actually retrieve the secret value
  // Just confirm it exists
  alert(`Secret "${ref.name}" is available in ${ref.type}`)
}

const addSecret = async () => {
  if (!newSecret.value.name || !newSecret.value.value) {
    return
  }

  addingSecret.value = true

  try {
    const response = await apiClient.setSecret(newSecret.value.name, newSecret.value.value)
    if (response.success) {
      // Show success message
      alert(`Secret "${newSecret.value.name}" added successfully!\nUse in config: ${response.data?.reference}`)

      // Reset form and hide it
      cancelAddSecret()

      // Reload secrets to show the new one
      await loadConfigSecrets()
    } else {
      alert('Failed to add secret: ' + (response.error || 'Unknown error'))
    }
  } catch (err: any) {
    alert('Failed to add secret: ' + err.message)
    console.error('Failed to add secret:', err)
  } finally {
    addingSecret.value = false
  }
}

const cancelAddSecret = () => {
  showAddSecretForm.value = false
  newSecret.value.name = ''
  newSecret.value.value = ''
}

const deleteSecret = async (ref: SecretRef) => {
  if (!confirm(`Are you sure you want to delete secret "${ref.name}"?`)) {
    return
  }

  try {
    const response = await apiClient.deleteSecret(ref.name, ref.type)
    if (response.success) {
      alert(`Secret "${ref.name}" deleted successfully!`)
      // Reload secrets to update the list
      await loadConfigSecrets()
    } else {
      alert('Failed to delete secret: ' + (response.error || 'Unknown error'))
    }
  } catch (err: any) {
    alert('Failed to delete secret: ' + err.message)
    console.error('Failed to delete secret:', err)
  }
}

const migrateSecret = async (candidate: MigrationCandidate) => {
  candidate.migrating = true

  try {
    // This would extract the secret name from the suggested reference
    const match = candidate.suggested.match(/\$\{keyring:([^}]+)\}/)
    if (!match) {
      throw new Error('Invalid suggested reference format')
    }

    const secretName = match[1]

    // For now, show instructions since we can't securely transfer the value via web UI
    alert(`To migrate this secret:
1. Run: mcpproxy secrets set ${secretName}
2. Enter the value when prompted
3. Update your configuration to use: ${candidate.suggested}`)

  } catch (err: any) {
    alert('Migration failed: ' + err.message)
  } finally {
    candidate.migrating = false
  }
}

const getConfidenceClass = (confidence: number): string => {
  if (confidence >= 0.8) return 'high-confidence'
  if (confidence >= 0.6) return 'medium-confidence'
  return 'low-confidence'
}

const testEnvVar = async (envVar: EnvVarStatus) => {
  if (envVar.is_set) {
    alert(`Environment variable "${envVar.secret_ref.name}" is set`)
  } else {
    alert(`Environment variable "${envVar.secret_ref.name}" is NOT set`)
  }
}

const setEnvVarHelp = async (envVar: EnvVarStatus) => {
  const instructions = `To set the environment variable "${envVar.secret_ref.name}":

On macOS/Linux:
export ${envVar.secret_ref.name}="your-value"

On Windows (PowerShell):
$env:${envVar.secret_ref.name}="your-value"

On Windows (CMD):
set ${envVar.secret_ref.name}=your-value

To make it permanent, add it to your shell profile or use your system's environment variable settings.`

  alert(instructions)
}

onMounted(async () => {
  // Give the API service time to initialize the API key from URL params
  await new Promise(resolve => setTimeout(resolve, 100))

  // Debug: Check if API key is available
  console.log('API key available on secrets mount:', apiClient.hasAPIKey(), apiClient.getAPIKeyPreview())

  loadConfigSecrets()
})
</script>

<style scoped>
.secrets-page {
  padding: 1.5rem;
  max-width: 1200px;
  margin: 0 auto;
}

.page-header {
  margin-bottom: 2rem;
}

.page-title {
  font-size: 2rem;
  font-weight: 600;
  color: var(--text-primary);
  margin: 0 0 0.5rem 0;
}

.page-description {
  color: var(--text-secondary);
  margin: 0;
}

.loading-state, .error-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 200px;
  text-align: center;
}

.spinner {
  width: 32px;
  height: 32px;
  border: 3px solid var(--border-color);
  border-top: 3px solid var(--primary-color);
  border-radius: 50%;
  animation: spin 1s linear infinite;
  margin-bottom: 1rem;
}

@keyframes spin {
  0% { transform: rotate(0deg); }
  100% { transform: rotate(360deg); }
}

.error-icon {
  font-size: 3rem;
  margin-bottom: 1rem;
}

.retry-button {
  background: var(--primary-color);
  color: white;
  border: none;
  padding: 0.5rem 1rem;
  border-radius: 4px;
  cursor: pointer;
  margin-top: 1rem;
}

.stats-section {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 1rem;
  margin-bottom: 2rem;
}

.stat-card {
  background: var(--card-background);
  border: 1px solid var(--border-color);
  border-radius: 8px;
  padding: 1.5rem;
  text-align: center;
}

.stat-number {
  font-size: 2rem;
  font-weight: 600;
  color: var(--primary-color);
  margin-bottom: 0.5rem;
}

.stat-label {
  color: var(--text-secondary);
  font-size: 0.875rem;
}

.actions-bar {
  display: flex;
  gap: 1rem;
  margin-bottom: 2rem;
}

.action-button {
  background: var(--primary-color);
  color: white;
  border: none;
  padding: 0.5rem 1rem;
  border-radius: 4px;
  cursor: pointer;
  transition: background-color 0.2s;
}

.action-button:hover {
  background: var(--primary-color-dark);
}

.action-button.secondary {
  background: var(--card-background);
  color: var(--text-primary);
  border: 1px solid var(--border-color);
}

.action-button.secondary:hover {
  background: var(--hover-color);
}

.action-button.small {
  padding: 0.25rem 0.5rem;
  font-size: 0.875rem;
}

.action-button.danger {
  background: var(--error-color);
}

.action-button:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.section {
  margin-bottom: 3rem;
}

.section-title {
  font-size: 1.25rem;
  font-weight: 600;
  color: var(--text-primary);
  margin: 0 0 0.5rem 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.section-description {
  color: var(--text-secondary);
  margin: 0 0 1rem 0;
}

.confidence-legend {
  display: flex;
  gap: 1rem;
  font-size: 0.875rem;
  font-weight: normal;
}

.confidence-item {
  display: flex;
  align-items: center;
  gap: 0.25rem;
}

.confidence-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.confidence-dot.high { background: #10b981; }
.confidence-dot.medium { background: #f59e0b; }
.confidence-dot.low { background: #ef4444; }

.empty-state {
  text-align: center;
  padding: 3rem 1rem;
  color: var(--text-secondary);
}

.empty-icon {
  font-size: 3rem;
  margin-bottom: 1rem;
}

.secrets-list, .migration-list {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.secret-item, .migration-item {
  background: var(--card-background);
  border: 1px solid var(--border-color);
  border-radius: 8px;
  padding: 1rem;
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
}

.migration-item.high-confidence {
  border-left: 4px solid #10b981;
}

.migration-item.medium-confidence {
  border-left: 4px solid #f59e0b;
}

.migration-item.low-confidence {
  border-left: 4px solid #ef4444;
}

/* Tabs */
.tabs-container {
  margin-top: 2rem;
}

.tabs-header {
  display: flex;
  border-bottom: 2px solid var(--border-color);
  margin-bottom: 1.5rem;
}

.tab-button {
  background: none;
  border: none;
  padding: 1rem 1.5rem;
  font-size: 1rem;
  color: var(--text-secondary);
  cursor: pointer;
  border-bottom: 3px solid transparent;
  transition: all 0.2s;
}

.tab-button:hover {
  color: var(--text-primary);
  background: var(--hover-color);
}

.tab-button.active {
  color: var(--primary-color);
  border-bottom-color: var(--primary-color);
  font-weight: 600;
}

.tab-panel {
  min-height: 300px;
}

/* Environment Variables */
.env-vars-list {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.env-var-item {
  background: var(--card-background);
  border: 1px solid var(--border-color);
  border-radius: 8px;
  padding: 1rem;
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
}

.env-var-item.missing {
  border-left: 4px solid #ef4444;
  background: #fef2f2;
}

.env-var-info {
  flex: 1;
}

.env-var-name {
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 0.5rem;
}

.env-var-status {
  margin-bottom: 0.5rem;
}

.status-badge {
  padding: 0.25rem 0.75rem;
  border-radius: 12px;
  font-size: 0.75rem;
  font-weight: 600;
}

.status-badge.set {
  background: #d1fae5;
  color: #065f46;
}

.status-badge.missing {
  background: #fee2e2;
  color: #991b1b;
}

.env-var-ref {
  font-family: 'Courier New', monospace;
  font-size: 0.875rem;
  color: var(--text-secondary);
}

.env-var-actions {
  display: flex;
  gap: 0.5rem;
  margin-left: 1rem;
}

.action-button.warning {
  background: #f59e0b;
  color: white;
}

.secret-info, .migration-info {
  flex: 1;
}

.secret-name, .migration-field {
  font-weight: 600;
  color: var(--text-primary);
  margin-bottom: 0.25rem;
}

.secret-type {
  background: var(--tag-background);
  color: var(--tag-text);
  padding: 0.125rem 0.5rem;
  border-radius: 12px;
  font-size: 0.75rem;
  display: inline-block;
  margin-bottom: 0.25rem;
}

.secret-ref, .migration-value, .migration-suggestion {
  font-family: 'Courier New', monospace;
  font-size: 0.875rem;
  color: var(--text-secondary);
  margin-bottom: 0.25rem;
}

.migration-confidence {
  font-size: 0.75rem;
  color: var(--text-secondary);
}

.secret-actions, .migration-actions {
  display: flex;
  gap: 0.5rem;
  margin-left: 1rem;
}

.help-section {
  background: var(--card-background);
  border: 1px solid var(--border-color);
  border-radius: 8px;
  padding: 1.5rem;
}

.help-content {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  gap: 1.5rem;
}

.help-item h4 {
  margin: 0 0 0.5rem 0;
  color: var(--text-primary);
}

.help-item p {
  margin: 0 0 0.5rem 0;
  color: var(--text-secondary);
}

.help-item code {
  background: var(--code-background);
  color: var(--code-text);
  padding: 0.25rem 0.5rem;
  border-radius: 4px;
  font-family: 'Courier New', monospace;
  font-size: 0.875rem;
}

/* Add Secret Form Styles */
.add-secret-form {
  background: var(--card-background);
  border: 1px solid var(--border-color);
  border-radius: 8px;
  padding: 1.5rem;
  margin-bottom: 2rem;
}

.add-secret-form h3 {
  margin: 0 0 1rem 0;
  color: var(--text-primary);
}

.form-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 1rem;
  margin-bottom: 1rem;
}

.form-group {
  display: flex;
  flex-direction: column;
}

.form-group label {
  margin-bottom: 0.5rem;
  color: var(--text-primary);
  font-weight: 500;
}

.form-input {
  padding: 0.75rem;
  border: 1px solid var(--border-color);
  border-radius: 4px;
  background: var(--card-background);
  color: var(--text-primary);
  font-size: 1rem;
}

.form-input:focus {
  outline: none;
  border-color: var(--primary-color);
  box-shadow: 0 0 0 2px var(--primary-color)20;
}

.form-hint {
  margin-top: 0.25rem;
  color: var(--text-secondary);
  font-size: 0.875rem;
}

.form-actions {
  display: flex;
  gap: 1rem;
  margin-bottom: 1rem;
}

.form-preview {
  padding: 0.75rem;
  background: var(--code-background);
  border-radius: 4px;
  border-left: 3px solid var(--primary-color);
}

.form-preview code {
  background: transparent;
  color: var(--primary-color);
  font-weight: 600;
}

@media (max-width: 768px) {
  .form-row {
    grid-template-columns: 1fr;
  }

  .form-actions {
    flex-direction: column;
  }
}

/* CSS Variables (these would typically be defined in your main CSS) */
:root {
  --text-primary: #1f2937;
  --text-secondary: #6b7280;
  --primary-color: #3b82f6;
  --primary-color-dark: #2563eb;
  --card-background: #ffffff;
  --border-color: #e5e7eb;
  --hover-color: #f9fafb;
  --error-color: #ef4444;
  --tag-background: #e0e7ff;
  --tag-text: #3730a3;
  --code-background: #f3f4f6;
  --code-text: #374151;
}

@media (prefers-color-scheme: dark) {
  :root {
    --text-primary: #f9fafb;
    --text-secondary: #9ca3af;
    --card-background: #1f2937;
    --border-color: #374151;
    --hover-color: #374151;
    --tag-background: #1e3a8a;
    --tag-text: #dbeafe;
    --code-background: #374151;
    --code-text: #d1d5db;
  }
}
</style>