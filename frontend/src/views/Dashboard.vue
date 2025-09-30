<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Dashboard</h1>
        <p class="text-base-content/70 mt-1">MCPProxy Control Panel Overview</p>
      </div>
      <div class="flex items-center space-x-2">
        <div
          :class="[
            'badge',
            systemStore.isRunning ? 'badge-success' : 'badge-error'
          ]"
        >
          {{ systemStore.isRunning ? 'Running' : 'Stopped' }}
        </div>
        <span class="text-sm">{{ systemStore.listenAddr || 'Not running' }}</span>
      </div>
    </div>

    <!-- Stats Cards -->
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
      <!-- Servers Stats -->
      <div class="stats shadow bg-base-100">
        <div class="stat">
          <div class="stat-figure text-primary">
            <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01" />
            </svg>
          </div>
          <div class="stat-title">Total Servers</div>
          <div class="stat-value">{{ serversStore.serverCount.total }}</div>
          <div class="stat-desc">{{ serversStore.serverCount.connected }} connected</div>
        </div>
      </div>

      <!-- Tools Stats -->
      <div class="stats shadow bg-base-100">
        <div class="stat">
          <div class="stat-figure text-secondary">
            <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
            </svg>
          </div>
          <div class="stat-title">Available Tools</div>
          <div class="stat-value">{{ serversStore.totalTools }}</div>
          <div class="stat-desc">across all servers</div>
        </div>
      </div>

      <!-- Enabled Servers -->
      <div class="stats shadow bg-base-100">
        <div class="stat">
          <div class="stat-figure text-success">
            <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          </div>
          <div class="stat-title">Enabled</div>
          <div class="stat-value">{{ serversStore.serverCount.enabled }}</div>
          <div class="stat-desc">servers active</div>
        </div>
      </div>

      <!-- Quarantined Servers -->
      <div class="stats shadow bg-base-100">
        <div class="stat">
          <div class="stat-figure text-warning">
            <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.732-.833-2.5 0L3.732 16.5c-.77.833.192 2.5 1.732 2.5z" />
            </svg>
          </div>
          <div class="stat-title">Quarantined</div>
          <div class="stat-value">{{ serversStore.serverCount.quarantined }}</div>
          <div class="stat-desc">security review needed</div>
        </div>
      </div>
    </div>

    <!-- Token Savings Card -->
    <div v-if="tokenSavingsData" class="card bg-base-100 shadow-md">
      <div class="card-body">
        <h2 class="card-title">Token Savings</h2>
        <p class="text-sm text-base-content/70">MCPProxy reduces token usage through intelligent BM25 search</p>

        <div class="stats stats-horizontal shadow mt-4">
          <div class="stat">
            <div class="stat-figure text-success">
              <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4M7.835 4.697a3.42 3.42 0 001.946-.806 3.42 3.42 0 014.438 0 3.42 3.42 0 001.946.806 3.42 3.42 0 013.138 3.138 3.42 3.42 0 00.806 1.946 3.42 3.42 0 010 4.438 3.42 3.42 0 00-.806 1.946 3.42 3.42 0 01-3.138 3.138 3.42 3.42 0 00-1.946.806 3.42 3.42 0 01-4.438 0 3.42 3.42 0 00-1.946-.806 3.42 3.42 0 01-3.138-3.138 3.42 3.42 0 00-.806-1.946 3.42 3.42 0 010-4.438 3.42 3.42 0 00.806-1.946 3.42 3.42 0 013.138-3.138z" />
              </svg>
            </div>
            <div class="stat-title">Tokens Saved</div>
            <div class="stat-value text-success">{{ tokenSavingsData.saved_tokens.toLocaleString() }}</div>
            <div class="stat-desc">{{ tokenSavingsData.saved_tokens_percentage.toFixed(1) }}% reduction</div>
          </div>

          <div class="stat">
            <div class="stat-title">Full Tool List Size</div>
            <div class="stat-value text-sm">{{ tokenSavingsData.total_server_tool_list_size.toLocaleString() }}</div>
            <div class="stat-desc">All upstream server tools</div>
          </div>

          <div class="stat">
            <div class="stat-title">Typical Query Result</div>
            <div class="stat-value text-sm">{{ tokenSavingsData.average_query_result_size.toLocaleString() }}</div>
            <div class="stat-desc">BM25 search result size</div>
          </div>
        </div>

        <div class="text-xs text-base-content/60 mt-4">
          <details class="collapse collapse-arrow bg-base-200">
            <summary class="collapse-title text-sm font-medium">Per-Server Token Breakdown</summary>
            <div class="collapse-content">
              <div class="overflow-x-auto mt-2">
                <table class="table table-xs">
                  <thead>
                    <tr>
                      <th>Server</th>
                      <th class="text-right">Tool List Size (tokens)</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="(size, serverName) in tokenSavingsData.per_server_tool_list_sizes" :key="serverName">
                      <td>{{ serverName }}</td>
                      <td class="text-right font-mono">{{ size.toLocaleString() }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>
          </details>
        </div>
      </div>
    </div>

    <!-- Token Usage Stats (Recent Calls) -->
    <div v-if="tokenStats.totalTokens > 0" class="card bg-base-100 shadow-md">
      <div class="card-body">
        <h2 class="card-title">Recent Token Usage</h2>
        <p class="text-sm text-base-content/70">Token consumption from the last {{ recentToolCalls.length }} tool calls</p>

        <div class="stats stats-horizontal shadow mt-4">
          <div class="stat">
            <div class="stat-figure text-primary">
              <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />
              </svg>
            </div>
            <div class="stat-title">Total Tokens</div>
            <div class="stat-value text-primary">{{ tokenStats.totalTokens.toLocaleString() }}</div>
            <div class="stat-desc">{{ tokenStats.callsWithMetrics }} of {{ recentToolCalls.length }} calls tracked</div>
          </div>

          <div class="stat">
            <div class="stat-title">Input Tokens</div>
            <div class="stat-value text-sm">{{ tokenStats.inputTokens.toLocaleString() }}</div>
            <div class="stat-desc">Request data</div>
          </div>

          <div class="stat">
            <div class="stat-title">Output Tokens</div>
            <div class="stat-value text-sm">{{ tokenStats.outputTokens.toLocaleString() }}</div>
            <div class="stat-desc">Response data</div>
          </div>

          <div class="stat">
            <div class="stat-title">Avg per Call</div>
            <div class="stat-value text-sm">{{ tokenStats.avgTokensPerCall.toLocaleString() }}</div>
            <div class="stat-desc">{{ tokenStats.mostUsedModel || 'Mixed models' }}</div>
          </div>
        </div>

        <div class="text-xs text-base-content/60 mt-2">
          <router-link to="/tool-calls" class="link">View detailed token usage →</router-link>
        </div>
      </div>
    </div>

    <!-- Diagnostics Panel -->
    <div class="space-y-6">
      <!-- Main Diagnostics Card -->
      <div class="card bg-base-100 shadow-md">
        <div class="card-body">
          <div class="flex items-center justify-between mb-4">
            <h2 class="card-title text-xl">System Diagnostics</h2>
            <div class="flex items-center space-x-2">
              <div class="badge badge-sm" :class="diagnosticsBadgeClass">
                {{ totalDiagnosticsCount }} {{ totalDiagnosticsCount === 1 ? 'issue' : 'issues' }}
              </div>
              <button
                v-if="dismissedDiagnostics.size > 0"
                @click="restoreAllDismissed"
                class="btn btn-xs btn-ghost"
                title="Restore dismissed issues"
              >
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                </svg>
              </button>
            </div>
          </div>

          <!-- Upstream Errors -->
          <div v-if="upstreamErrors.length > 0" class="collapse collapse-arrow border border-error mb-4">
            <input type="checkbox" class="peer" :checked="!collapsedSections.upstreamErrors" @change="toggleSection('upstreamErrors')" />
            <div class="collapse-title bg-error/10 text-error font-medium flex items-center justify-between">
              <div class="flex items-center space-x-2">
                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
                <span>Upstream Errors ({{ upstreamErrors.length }})</span>
              </div>
            </div>
            <div class="collapse-content bg-error/5">
              <div class="pt-4 space-y-3">
                <div
                  v-for="error in upstreamErrors"
                  :key="error.server"
                  class="flex items-start justify-between p-3 bg-base-100 rounded-lg border border-error/20"
                >
                  <div class="flex-1">
                    <div class="font-medium text-error">{{ error.server }}</div>
                    <div class="text-sm text-base-content/70 mt-1">{{ error.message }}</div>
                    <div class="text-xs text-base-content/50 mt-1">{{ error.timestamp }}</div>
                  </div>
                  <div class="flex items-center space-x-2 ml-4">
                    <router-link :to="`/servers/${error.server}`" class="btn btn-xs btn-outline btn-error">
                      Fix
                    </router-link>
                    <button @click="dismissError(error)" class="btn btn-xs btn-ghost" title="Dismiss">
                      <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- OAuth Required -->
          <div v-if="oauthRequired.length > 0" class="collapse collapse-arrow border border-warning mb-4">
            <input type="checkbox" class="peer" :checked="!collapsedSections.oauthRequired" @change="toggleSection('oauthRequired')" />
            <div class="collapse-title bg-warning/10 text-warning font-medium flex items-center justify-between">
              <div class="flex items-center space-x-2">
                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.029 5.912c-.563-.097-1.159.026-1.563.43L10.5 17.25H8v2.25H5.5v2.25H3v-2.818c0-.597.237-1.17.659-1.591l6.499-6.499c.404-.404.527-1 .43-1.563A6 6 0 1121 9z" />
                </svg>
                <span>Authentication Required ({{ oauthRequired.length }})</span>
              </div>
            </div>
            <div class="collapse-content bg-warning/5">
              <div class="pt-4 space-y-3">
                <div
                  v-for="server in oauthRequired"
                  :key="server"
                  class="flex items-center justify-between p-3 bg-base-100 rounded-lg border border-warning/20"
                >
                  <div class="flex items-center space-x-3">
                    <svg class="w-5 h-5 text-warning" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.029 5.912c-.563-.097-1.159.026-1.563.43L10.5 17.25H8v2.25H5.5v2.25H3v-2.818c0-.597.237-1.17.659-1.591l6.499-6.499c.404-.404.527-1 .43-1.563A6 6 0 1121 9z" />
                    </svg>
                    <div>
                      <div class="font-medium">{{ server }}</div>
                      <div class="text-sm text-base-content/70">OAuth authentication needed</div>
                    </div>
                  </div>
                  <div class="flex items-center space-x-2">
                    <button @click="triggerOAuthLogin(server)" class="btn btn-xs btn-warning">
                      Login
                    </button>
                    <button @click="dismissOAuth(server)" class="btn btn-xs btn-ghost" title="Dismiss">
                      <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- Missing Secrets -->
          <div v-if="missingSecrets.length > 0" class="collapse collapse-arrow border border-warning mb-4">
            <input type="checkbox" class="peer" :checked="!collapsedSections.missingSecrets" @change="toggleSection('missingSecrets')" />
            <div class="collapse-title bg-warning/10 text-warning font-medium flex items-center justify-between">
              <div class="flex items-center space-x-2">
                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
                </svg>
                <span>Missing Secrets ({{ missingSecrets.length }})</span>
              </div>
            </div>
            <div class="collapse-content bg-warning/5">
              <div class="pt-4 space-y-3">
                <div
                  v-for="secret in missingSecrets"
                  :key="secret.name"
                  class="flex items-center justify-between p-3 bg-base-100 rounded-lg border border-warning/20"
                >
                  <div class="flex items-center space-x-3">
                    <svg class="w-5 h-5 text-warning" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
                    </svg>
                    <div>
                      <div class="font-medium">{{ secret.name }}</div>
                      <div class="text-sm text-base-content/70 font-mono">{{ secret.reference }}</div>
                    </div>
                  </div>
                  <div class="flex items-center space-x-2">
                    <router-link to="/secrets" class="btn btn-xs btn-warning">
                      Set Value
                    </router-link>
                    <button @click="dismissSecret(secret)" class="btn btn-xs btn-ghost" title="Dismiss">
                      <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                      </svg>
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- Runtime Warnings -->
          <div v-if="runtimeWarnings.length > 0" class="collapse collapse-arrow border border-info mb-4">
            <input type="checkbox" class="peer" :checked="!collapsedSections.runtimeWarnings" @change="toggleSection('runtimeWarnings')" />
            <div class="collapse-title bg-info/10 text-info font-medium flex items-center justify-between">
              <div class="flex items-center space-x-2">
                <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
                <span>Runtime Warnings ({{ runtimeWarnings.length }})</span>
              </div>
            </div>
            <div class="collapse-content bg-info/5">
              <div class="pt-4 space-y-3">
                <div
                  v-for="warning in runtimeWarnings"
                  :key="warning.id"
                  class="flex items-start justify-between p-3 bg-base-100 rounded-lg border border-info/20"
                >
                  <div class="flex-1">
                    <div class="font-medium text-info">{{ warning.category }}</div>
                    <div class="text-sm text-base-content/70 mt-1">{{ warning.message }}</div>
                    <div class="text-xs text-base-content/50 mt-1">{{ warning.timestamp }}</div>
                  </div>
                  <button @click="dismissWarning(warning)" class="btn btn-xs btn-ghost ml-4" title="Dismiss">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                    </svg>
                  </button>
                </div>
              </div>
            </div>
          </div>

          <!-- No Issues State -->
          <div v-if="totalDiagnosticsCount === 0" class="text-center py-12">
            <svg class="w-16 h-16 mx-auto mb-4 text-success opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <h3 class="text-lg font-medium text-success mb-2">All Systems Operational</h3>
            <p class="text-base-content/60">No issues detected with your server configuration</p>
            <router-link to="/servers" class="btn btn-sm btn-outline btn-success mt-4">
              View Servers
            </router-link>
          </div>
        </div>
      </div>

      <!-- Hints Panel -->
      <HintsPanel :hints="dashboardHints" />

      <!-- Tool Call History Widget -->
      <div class="card bg-base-100 shadow-md">
        <div class="card-body">
          <div class="flex items-center justify-between mb-4">
            <h2 class="card-title text-xl">Recent Tool Calls</h2>
            <router-link to="/tool-calls" class="btn btn-sm btn-ghost">
              View All
              <svg class="w-4 h-4 ml-1" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
              </svg>
            </router-link>
          </div>

          <div v-if="toolCallsLoading" class="flex justify-center py-8">
            <span class="loading loading-spinner loading-md"></span>
          </div>

          <div v-else-if="toolCallsError" class="alert alert-error">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <span>{{ toolCallsError }}</span>
          </div>

          <div v-else-if="recentToolCalls.length === 0" class="text-center py-8 text-base-content/60">
            <svg class="w-12 h-12 mx-auto mb-3 opacity-30" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2" />
            </svg>
            <p>No tool calls yet</p>
            <p class="text-sm mt-1">Tool calls will appear here once servers start executing tools</p>
          </div>

          <div v-else class="overflow-x-auto">
            <table class="table table-sm">
              <thead>
                <tr>
                  <th>Time</th>
                  <th>Server</th>
                  <th>Tool</th>
                  <th>Status</th>
                  <th>Duration</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="call in recentToolCalls" :key="call.id" class="hover">
                  <td>
                    <span class="text-xs" :title="call.timestamp">
                      {{ formatRelativeTime(call.timestamp) }}
                    </span>
                  </td>
                  <td>
                    <router-link
                      :to="`/servers/${call.server_name}`"
                      class="link link-hover text-sm"
                    >
                      {{ call.server_name }}
                    </router-link>
                  </td>
                  <td>
                    <code class="text-xs">{{ call.tool_name }}</code>
                  </td>
                  <td>
                    <div
                      class="badge badge-sm"
                      :class="call.error ? 'badge-error' : 'badge-success'"
                    >
                      {{ call.error ? 'Error' : 'Success' }}
                    </div>
                  </td>
                  <td>
                    <span class="text-xs text-base-content/70">
                      {{ formatDuration(call.duration) }}
                    </span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, reactive, onMounted, onUnmounted } from 'vue'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import api from '@/services/api'
import HintsPanel from '@/components/HintsPanel.vue'
import type { Hint } from '@/components/HintsPanel.vue'

const serversStore = useServersStore()
const systemStore = useSystemStore()

// Collapsed sections state
const collapsedSections = reactive({
  upstreamErrors: false,
  oauthRequired: false,
  missingSecrets: false,
  runtimeWarnings: false
})

// Dismissed diagnostics
const dismissedDiagnostics = ref(new Set<string>())

// Load dismissed items from localStorage
const STORAGE_KEY = 'mcpproxy-dismissed-diagnostics'
const loadDismissedDiagnostics = () => {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored) {
      const items = JSON.parse(stored) as string[]
      dismissedDiagnostics.value = new Set(items)
    }
  } catch (error) {
    console.warn('Failed to load dismissed diagnostics from localStorage:', error)
  }
}

// Save dismissed items to localStorage
const saveDismissedDiagnostics = () => {
  try {
    const items = Array.from(dismissedDiagnostics.value)
    localStorage.setItem(STORAGE_KEY, JSON.stringify(items))
  } catch (error) {
    console.warn('Failed to save dismissed diagnostics to localStorage:', error)
  }
}

// Load dismissed diagnostics on init
loadDismissedDiagnostics()

// Diagnostics data
const diagnosticsData = ref<any>(null)
const diagnosticsLoading = ref(false)
const diagnosticsError = ref<string | null>(null)

// Auto-refresh interval
let refreshInterval: ReturnType<typeof setInterval> | null = null

// Load diagnostics from API
const loadDiagnostics = async () => {
  diagnosticsLoading.value = true
  diagnosticsError.value = null

  try {
    const response = await api.getDiagnostics()
    if (response.success && response.data) {
      diagnosticsData.value = response.data
    } else {
      diagnosticsError.value = response.error || 'Failed to load diagnostics'
    }
  } catch (error) {
    diagnosticsError.value = error instanceof Error ? error.message : 'Unknown error'
  } finally {
    diagnosticsLoading.value = false
  }
}

// Computed diagnostics with dismiss filtering
const upstreamErrors = computed(() => {
  if (!diagnosticsData.value?.upstream_errors) return []

  return diagnosticsData.value.upstream_errors.filter((error: any) => {
    const errorKey = `error_${error.server}`
    return !dismissedDiagnostics.value.has(errorKey)
  }).map((error: any) => ({
    server: error.server || 'Unknown',
    message: error.message,
    timestamp: new Date(error.timestamp).toLocaleString()
  }))
})

const oauthRequired = computed(() => {
  if (!diagnosticsData.value?.oauth_required) return []

  return diagnosticsData.value.oauth_required.filter((server: string) => {
    const oauthKey = `oauth_${server}`
    return !dismissedDiagnostics.value.has(oauthKey)
  })
})

const missingSecrets = computed(() => {
  if (!diagnosticsData.value?.missing_secrets) return []

  return diagnosticsData.value.missing_secrets.filter((secret: any) => {
    const secretKey = `secret_${secret.name}`
    return !dismissedDiagnostics.value.has(secretKey)
  })
})

const runtimeWarnings = computed(() => {
  if (!diagnosticsData.value?.runtime_warnings) return []

  return diagnosticsData.value.runtime_warnings.filter((warning: any) => {
    const warningKey = `warning_${warning.title}_${warning.timestamp}`
    return !dismissedDiagnostics.value.has(warningKey)
  }).map((warning: any) => ({
    id: `${warning.title}_${warning.timestamp}`,
    category: warning.category,
    message: warning.message,
    timestamp: new Date(warning.timestamp).toLocaleString()
  }))
})

const totalDiagnosticsCount = computed(() => {
  return upstreamErrors.value.length +
         oauthRequired.value.length +
         missingSecrets.value.length +
         runtimeWarnings.value.length
})

const diagnosticsBadgeClass = computed(() => {
  if (totalDiagnosticsCount.value === 0) return 'badge-success'
  if (upstreamErrors.value.length > 0) return 'badge-error'
  if (oauthRequired.value.length > 0 || missingSecrets.value.length > 0) return 'badge-warning'
  return 'badge-info'
})

const lastUpdateTime = computed(() => {
  if (!systemStore.status?.timestamp) return 'Never'

  const now = Date.now()
  const timestamp = systemStore.status.timestamp * 1000 // Convert to milliseconds
  const diff = now - timestamp

  if (diff < 1000) return 'Just now'
  if (diff < 60000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`

  return new Date(timestamp).toLocaleTimeString()
})

// Methods
const toggleSection = (section: keyof typeof collapsedSections) => {
  collapsedSections[section] = !collapsedSections[section]
}

const dismissError = (error: any) => {
  const key = `error_${error.server}`
  dismissedDiagnostics.value.add(key)
  saveDismissedDiagnostics()
}

const dismissOAuth = (server: string) => {
  const key = `oauth_${server}`
  dismissedDiagnostics.value.add(key)
  saveDismissedDiagnostics()
}

const dismissSecret = (secret: any) => {
  const key = `secret_${secret.name}`
  dismissedDiagnostics.value.add(key)
  saveDismissedDiagnostics()
}

const dismissWarning = (warning: any) => {
  const key = `warning_${warning.id}`
  dismissedDiagnostics.value.add(key)
  saveDismissedDiagnostics()
}

const restoreAllDismissed = () => {
  dismissedDiagnostics.value.clear()
  saveDismissedDiagnostics()
}

const triggerOAuthLogin = async (server: string) => {
  try {
    await serversStore.triggerOAuthLogin(server)
    systemStore.addToast({
      type: 'success',
      title: 'OAuth Login',
      message: `OAuth login initiated for ${server}`
    })
    // Refresh diagnostics after OAuth attempt
    setTimeout(loadDiagnostics, 2000)
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'OAuth Login Failed',
      message: `Failed to initiate OAuth login: ${error instanceof Error ? error.message : 'Unknown error'}`
    })
  }
}

// Token Savings Data
const tokenSavingsData = ref<any>(null)
const tokenSavingsLoading = ref(false)
const tokenSavingsError = ref<string | null>(null)

// Tool Calls History
const recentToolCalls = ref<any[]>([])
const toolCallsLoading = ref(false)
const toolCallsError = ref<string | null>(null)

// Load token savings data
const loadTokenSavings = async () => {
  tokenSavingsLoading.value = true
  tokenSavingsError.value = null

  try {
    const response = await api.getTokenStats()
    if (response.success && response.data) {
      tokenSavingsData.value = response.data
    } else {
      tokenSavingsError.value = response.error || 'Failed to load token savings'
    }
  } catch (error) {
    tokenSavingsError.value = error instanceof Error ? error.message : 'Unknown error'
  } finally {
    tokenSavingsLoading.value = false
  }
}

// Load recent tool calls
const loadToolCalls = async () => {
  toolCallsLoading.value = true
  toolCallsError.value = null

  try {
    const response = await api.getToolCalls({ limit: 10 })
    if (response.success && response.data) {
      recentToolCalls.value = response.data.tool_calls || []
    } else {
      toolCallsError.value = response.error || 'Failed to load tool calls'
    }
  } catch (error) {
    toolCallsError.value = error instanceof Error ? error.message : 'Unknown error'
  } finally {
    toolCallsLoading.value = false
  }
}

// Format duration from nanoseconds
const formatDuration = (nanoseconds: number): string => {
  const ms = nanoseconds / 1000000
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(2)}s`
}

// Format relative time
const formatRelativeTime = (timestamp: string): string => {
  const now = Date.now()
  const time = new Date(timestamp).getTime()
  const diff = now - time

  if (diff < 1000) return 'Just now'
  if (diff < 60000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`
  if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`
  return `${Math.floor(diff / 86400000)}d ago`
}

// Token statistics from recent tool calls
const tokenStats = computed(() => {
  let totalTokens = 0
  let inputTokens = 0
  let outputTokens = 0
  let callsWithMetrics = 0
  const modelCounts: Record<string, number> = {}

  for (const call of recentToolCalls.value) {
    if (call.metrics) {
      totalTokens += call.metrics.total_tokens || 0
      inputTokens += call.metrics.input_tokens || 0
      outputTokens += call.metrics.output_tokens || 0
      callsWithMetrics++

      const model = call.metrics.model || 'unknown'
      modelCounts[model] = (modelCounts[model] || 0) + 1
    }
  }

  // Find most used model
  let mostUsedModel = ''
  let maxCount = 0
  for (const [model, count] of Object.entries(modelCounts)) {
    if (count > maxCount) {
      maxCount = count
      mostUsedModel = model
    }
  }

  const avgTokensPerCall = callsWithMetrics > 0
    ? Math.round(totalTokens / callsWithMetrics)
    : 0

  return {
    totalTokens,
    inputTokens,
    outputTokens,
    avgTokensPerCall,
    mostUsedModel,
    callsWithMetrics
  }
})

// Dashboard hints
const dashboardHints = computed<Hint[]>(() => {
  const hints: Hint[] = []

  // Add hint if there are upstream errors
  if (upstreamErrors.value.length > 0) {
    hints.push({
      icon: '🔧',
      title: 'Fix Upstream Errors with CLI',
      description: 'Use these commands to diagnose and fix server connection issues',
      sections: [
        {
          title: 'Check server logs',
          codeBlock: {
            language: 'bash',
            code: `# View logs for specific server\ntail -f ~/.mcpproxy/logs/server-${upstreamErrors.value[0].server}.log\n\n# View main log\ntail -f ~/.mcpproxy/logs/main.log`
          }
        },
        {
          title: 'Restart server connection',
          codeBlock: {
            language: 'bash',
            code: `# Disable and re-enable server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"update","name":"${upstreamErrors.value[0].server}","enabled":false}'\n\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"update","name":"${upstreamErrors.value[0].server}","enabled":true}'`
          }
        }
      ]
    })
  }

  // Add hint if there are missing secrets
  if (missingSecrets.value.length > 0) {
    hints.push({
      icon: '🔐',
      title: 'Set Missing Secrets',
      description: 'Add secrets to your system keyring',
      sections: [
        {
          title: 'Store secret using CLI',
          codeBlock: {
            language: 'bash',
            code: `# Add secret to keyring\nmcpproxy secrets set ${missingSecrets.value[0].name}`
          }
        },
        {
          title: 'Or use environment variable',
          text: 'You can also set environment variables instead of keyring secrets:',
          codeBlock: {
            language: 'bash',
            code: `export ${missingSecrets.value[0].name}="your-secret-value"`
          }
        }
      ]
    })
  }

  // Add hint if there are OAuth required
  if (oauthRequired.value.length > 0) {
    hints.push({
      icon: '🔑',
      title: 'Authenticate OAuth Servers',
      description: 'Complete OAuth authentication for these servers',
      sections: [
        {
          title: 'Login via CLI',
          codeBlock: {
            language: 'bash',
            code: `# Authenticate with OAuth\nmcpproxy auth login --server=${oauthRequired.value[0]}`
          }
        },
        {
          title: 'Check authentication status',
          codeBlock: {
            language: 'bash',
            code: `# View authentication status\nmcpproxy auth status`
          }
        }
      ]
    })
  }

  // Always show general CLI hints
  hints.push({
    icon: '💡',
    title: 'CLI Commands for Managing MCPProxy',
    description: 'Useful commands for working with MCPProxy',
    sections: [
      {
        title: 'View all servers',
        codeBlock: {
          language: 'bash',
          code: `# List all upstream servers\nmcpproxy upstream list`
        }
      },
      {
        title: 'Search for tools',
        codeBlock: {
          language: 'bash',
          code: `# Search across all server tools\nmcpproxy tools search "your query"\n\n# List tools from specific server\nmcpproxy tools list --server=server-name`
        }
      },
      {
        title: 'Call a tool directly',
        codeBlock: {
          language: 'bash',
          code: `# Execute a tool\nmcpproxy call tool --tool-name=server:tool-name \\\n  --json_args='{"arg1":"value1"}'`
        }
      }
    ]
  })

  // LLM Agent hints
  hints.push({
    icon: '🤖',
    title: 'Use MCPProxy with LLM Agents',
    description: 'Connect Claude or other LLM agents to MCPProxy',
    sections: [
      {
        title: 'Example LLM prompts',
        list: [
          'Search for tools related to GitHub issues across all my MCP servers',
          'List all available MCP servers and their connection status',
          'Add a new MCP server from npm package @modelcontextprotocol/server-filesystem',
          'Show me statistics about which tools are being used most frequently'
        ]
      },
      {
        title: 'Configure Claude Desktop',
        text: 'Add MCPProxy to your Claude Desktop config:',
        codeBlock: {
          language: 'json',
          code: `{
  "mcpServers": {
    "mcpproxy": {
      "command": "mcpproxy",
      "args": ["serve"],
      "env": {}
    }
  }
}`
        }
      }
    ]
  })

  return hints
})

// Lifecycle
onMounted(() => {
  // Load diagnostics immediately
  loadDiagnostics()
  // Load token savings immediately
  loadTokenSavings()
  // Load tool calls immediately
  loadToolCalls()

  // Set up auto-refresh every 30 seconds
  refreshInterval = setInterval(() => {
    loadDiagnostics()
    loadTokenSavings()
    loadToolCalls()
  }, 30000)

  // Listen for SSE events to refresh diagnostics
  const handleSSEUpdate = () => {
    setTimeout(() => {
      loadDiagnostics()
      loadToolCalls()
    }, 1000) // Small delay to let backend process the change
  }

  // Listen to system store events
  systemStore.connectEventSource()

  // Refresh when servers change
  serversStore.fetchServers()
})

onUnmounted(() => {
  // Clean up interval
  if (refreshInterval) {
    clearInterval(refreshInterval)
    refreshInterval = null
  }
})
</script>