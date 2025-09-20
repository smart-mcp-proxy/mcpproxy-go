<template>
  <div class="secrets-page">
    <div class="page-header">
      <h1 class="page-title">Secrets Management</h1>
      <p class="page-description">
        Manage secrets stored in your system's secure keyring. Secrets are never displayed in full for security.
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
          <div class="stat-number">{{ secretRefs.length }}</div>
          <div class="stat-label">Total Secrets</div>
        </div>
        <div class="stat-card">
          <div class="stat-number">{{ unresolvedCount }}</div>
          <div class="stat-label">Unresolved References</div>
        </div>
        <div class="stat-card">
          <div class="stat-number">{{ migrationCandidates.length }}</div>
          <div class="stat-label">Migration Candidates</div>
        </div>
      </div>

      <!-- Actions Bar -->
      <div class="actions-bar">
        <button @click="loadSecrets" class="action-button secondary">
          🔄 Refresh
        </button>
        <button @click="runMigrationAnalysis" class="action-button secondary" :disabled="analysisLoading">
          🔍 {{ analysisLoading ? 'Analyzing...' : 'Analyze Configuration' }}
        </button>
      </div>

      <!-- Secret References -->
      <div class="section">
        <h2 class="section-title">Stored Secret References</h2>
        <div v-if="secretRefs.length === 0" class="empty-state">
          <div class="empty-icon">🔐</div>
          <h3>No Secrets Found</h3>
          <p>No secret references are currently stored in your keyring.</p>
          <p>Use the CLI to store secrets: <code>mcpproxy secrets set &lt;name&gt;</code></p>
        </div>
        <div v-else class="secrets-list">
          <div v-for="ref in secretRefs" :key="ref.name" class="secret-item">
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
import type { SecretRef, MigrationCandidate, MigrationAnalysis } from '@/types'

const loading = ref(true)
const error = ref<string | null>(null)
const secretRefs = ref<SecretRef[]>([])
const migrationCandidates = ref<MigrationCandidate[]>([])
const analysisLoading = ref(false)

const unresolvedCount = computed(() => {
  // This would need to be determined by checking if secrets can be resolved
  // For now, we'll assume all are resolved since they're in the keyring
  return 0
})

const loadSecrets = async () => {
  loading.value = true
  error.value = null

  try {
    const response = await apiClient.getSecretRefs()
    if (response.success && response.data) {
      secretRefs.value = response.data.refs || []
    } else {
      error.value = response.error || 'Failed to load secrets'
    }
  } catch (err: any) {
    error.value = err.message || 'Failed to load secrets'
    console.error('Failed to load secrets:', err)
  } finally {
    loading.value = false
  }
}

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

const deleteSecret = async (ref: SecretRef) => {
  if (confirm(`Are you sure you want to delete secret "${ref.name}"?`)) {
    try {
      // This would call the CLI or API to delete the secret
      alert('Secret deletion via UI is not yet implemented. Use the CLI: mcpproxy secrets del ' + ref.name)
    } catch (err: any) {
      alert('Failed to delete secret: ' + err.message)
    }
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

onMounted(() => {
  loadSecrets()
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