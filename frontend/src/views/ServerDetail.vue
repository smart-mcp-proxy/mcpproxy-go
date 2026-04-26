<template>
  <div class="space-y-6">
    <!-- Loading State -->
    <div v-if="loading" class="text-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
      <p class="mt-4">Loading server details...</p>
    </div>

    <!-- Error State -->
    <div v-else-if="error" class="alert alert-error">
      <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <div>
        <h3 class="font-bold">Failed to load server details</h3>
        <div class="text-sm">{{ error }}</div>
      </div>
      <button @click="loadServerDetails" class="btn btn-sm">
        Try Again
      </button>
    </div>

    <!-- Server Not Found -->
    <div v-else-if="!server" class="text-center py-12">
      <svg class="w-16 h-16 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2" />
      </svg>
      <h3 class="text-xl font-semibold mb-2">Server not found</h3>
      <p class="text-base-content/70 mb-4">
        The server "{{ serverName }}" was not found.
      </p>
      <router-link to="/servers" class="btn btn-primary">
        Back to Servers
      </router-link>
    </div>

    <!-- Server Details -->
    <div v-else>
      <!-- Header -->
      <div class="flex flex-col lg:flex-row lg:justify-between lg:items-start gap-4">
        <div>
          <div class="breadcrumbs text-sm mb-2">
            <ul>
              <li><router-link to="/servers">Servers</router-link></li>
              <li>{{ server.name }}</li>
            </ul>
          </div>
          <h1 class="text-3xl font-bold">{{ server.name }}</h1>
          <p class="text-base-content/70 mt-1">{{ server.protocol }} • {{ server.url || server.command || 'No endpoint' }}</p>
        </div>

        <div class="flex items-center space-x-2">
          <div
            :class="[
              'badge badge-lg',
              server.connected ? 'badge-success' :
              server.connecting ? 'badge-warning' :
              'badge-error'
            ]"
          >
            {{ server.connected ? 'Connected' : server.connecting ? 'Connecting' : 'Disconnected' }}
          </div>
          <div class="dropdown dropdown-end">
            <div tabindex="0" role="button" class="btn btn-outline">
              Actions
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
              </svg>
            </div>
            <ul tabindex="0" class="dropdown-content menu bg-base-100 rounded-box z-[1] w-52 p-2 shadow">
              <li>
                <button @click="toggleEnabled" :disabled="actionLoading">
                  <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
                  {{ server.enabled ? 'Disable' : 'Enable' }}
                </button>
              </li>
              <li v-if="server.enabled">
                <button @click="restartServer" :disabled="actionLoading">
                  <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
                  {{ isHttpProtocol ? 'Reconnect' : 'Restart' }}
                </button>
              </li>
              <li v-if="healthAction === 'login'">
                <button @click="triggerOAuth" :disabled="actionLoading">
                  <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
                  Login
                </button>
              </li>
              <li v-if="server.enabled && server.connected">
                <button @click="discoverTools" :disabled="actionLoading">
                  <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
                  Discover Tools
                </button>
              </li>
              <li>
                <button @click="server.quarantined ? handleApproveClick() : quarantineServer()" :disabled="actionLoading">
                  <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
                  {{ server.quarantined ? 'Approve' : 'Quarantine' }}
                </button>
              </li>
              <li>
                <button @click="refreshData" :disabled="actionLoading">
                  <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
                  Refresh
                </button>
              </li>
            </ul>
          </div>
        </div>
      </div>

      <!-- Status Cards -->
      <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
        <div class="stats shadow bg-base-100">
          <div class="stat">
            <div class="stat-figure text-primary">
              <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
              </svg>
            </div>
            <div class="stat-title">Tools</div>
            <div class="stat-value">{{ serverTools.length }}</div>
            <div class="stat-desc">available tools</div>
          </div>
        </div>

        <div class="stats shadow bg-base-100">
          <div class="stat">
            <div class="stat-figure text-secondary">
              <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
              </svg>
            </div>
            <div class="stat-title">Status</div>
            <div class="stat-value text-sm">{{ server.enabled ? 'Enabled' : 'Disabled' }}</div>
            <div class="stat-desc">{{ server.quarantined ? 'Quarantined' : 'Active' }}</div>
          </div>
        </div>

        <div class="stats shadow bg-base-100">
          <div class="stat">
            <div class="stat-figure text-info">
              <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z" />
              </svg>
            </div>
            <div class="stat-title">Protocol</div>
            <div class="stat-value text-sm">{{ server.protocol }}</div>
            <div class="stat-desc">communication type</div>
          </div>
        </div>

        <div class="stats shadow bg-base-100">
          <div class="stat">
            <div class="stat-figure text-warning">
              <svg class="w-8 h-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
              </svg>
            </div>
            <div class="stat-title">Connection</div>
            <div class="stat-value text-sm">
              {{ server.connected ? 'Online' : server.connecting ? 'Connecting' : 'Offline' }}
            </div>
            <div class="stat-desc">current state</div>
          </div>
        </div>
      </div>

      <!-- Alerts -->
      <div class="space-y-4">
        <!-- Spec 044 — structured diagnostic panel (shown when a diagnostic
             with warn/error severity is attached). Replaces the generic
             last_error alert for those cases. -->
        <ErrorPanel
          v-if="showDiagnosticPanel"
          :diagnostic="server.diagnostic"
          :server-name="server.name"
          @fixed="handleDiagnosticFixed"
        />

        <div v-else-if="server.last_error" class="alert alert-error">
          <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <div>
            <h3 class="font-bold">Server Error</h3>
            <div class="text-sm">{{ server.last_error }}</div>
          </div>
        </div>

        <div v-if="server.quarantined" class="alert alert-warning">
          <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.732-.833-2.5 0L3.732 16.5c-.77.833.192 2.5 1.732 2.5z" />
          </svg>
          <div>
            <h3 class="font-bold">Security Quarantine</h3>
            <div class="text-sm">This server is quarantined and requires manual approval before tools can be executed.</div>
          </div>
          <button @click="handleApproveClick" :disabled="actionLoading" class="btn btn-sm btn-warning">
            <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
            Approve
          </button>
        </div>
      </div>

      <!-- Approve Confirmation Modal (F-04: security scanner gated) -->
      <div v-if="showApproveConfirmation" class="modal modal-open">
        <div class="modal-box">
          <h3 class="font-bold text-lg mb-4">
            {{ approveDialogMode === 'no_scan' ? 'No Security Scan Run' : 'Critical Findings Detected' }}
          </h3>
          <p v-if="approveDialogMode === 'critical'" class="mb-4">
            <strong>{{ server.name }}</strong> has
            <span class="text-error font-semibold">{{ criticalFindingCount }} critical finding{{ criticalFindingCount === 1 ? '' : 's' }}</span>
            in its most recent security scan. Approving will allow this server to run despite these warnings.
          </p>
          <p v-else class="mb-4">
            No security scan has been run for <strong>{{ server.name }}</strong>. We strongly recommend running a scan first.
          </p>
          <p class="text-sm text-base-content/70 mb-6">
            The security scanner is an experimental heuristic. Force-approving bypasses the scanner gate.
          </p>
          <div class="modal-action">
            <button
              @click="showApproveConfirmation = false"
              :disabled="actionLoading"
              class="btn btn-outline"
            >
              Cancel
            </button>
            <button
              v-if="approveDialogMode === 'no_scan'"
              @click="scanFirstFromDialog"
              :disabled="actionLoading"
              class="btn btn-primary"
            >
              Scan First
            </button>
            <button
              @click="confirmForceApprove"
              :disabled="actionLoading"
              class="btn btn-error"
            >
              <span v-if="actionLoading" class="loading loading-spinner loading-xs"></span>
              Force Approve
            </button>
          </div>
        </div>
      </div>

      <!-- Tabs -->
      <div class="tabs tabs-bordered">
        <button
          :class="['tab tab-lg', activeTab === 'tools' ? 'tab-active' : '']"
          @click="activeTab = 'tools'"
        >
          Tools ({{ serverTools.length }})
        </button>
        <button
          :class="['tab tab-lg', activeTab === 'logs' ? 'tab-active' : '']"
          @click="activeTab = 'logs'"
        >
          Logs
        </button>
        <button
          :class="['tab tab-lg', activeTab === 'config' ? 'tab-active' : '']"
          @click="activeTab = 'config'"
        >
          Configuration
        </button>
        <button
          v-if="hasEnabledScanners()"
          :class="['tab tab-lg', activeTab === 'security' ? 'tab-active' : '']"
          @click="activeTab = 'security'; loadScannerNames(); loadScanReport()"
        >
          <span class="flex items-center gap-2">
            <span
              v-if="securityScanStatus === 'scanning'"
              class="loading loading-spinner loading-xs"
            ></span>
            <span
              v-else
              class="inline-block w-2.5 h-2.5 rounded-full"
              :class="securityDotClass"
            ></span>
            Security{{ securityTabSuffix }}
          </span>
        </button>
      </div>

      <!-- Tab Content -->
      <div class="mt-6">
        <!-- Tools Tab -->
        <div v-if="activeTab === 'tools'">
          <div v-if="toolsLoading" class="text-center py-8">
            <span class="loading loading-spinner loading-lg"></span>
            <p class="mt-2">Loading tools...</p>
          </div>

          <div v-else-if="toolsError" class="alert alert-error">
            <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <span>{{ toolsError }}</span>
            <button @click="loadTools" class="btn btn-sm">Retry</button>
          </div>

          <div v-else-if="serverTools.length === 0" class="text-center py-8">
            <svg class="w-16 h-16 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
            </svg>
            <h3 class="text-xl font-semibold mb-2">No tools available</h3>
            <p class="text-base-content/70">
              {{ server.connected ? 'This server has no tools available.' : 'Server must be connected to view tools.' }}
            </p>
          </div>

          <div v-else class="space-y-4">
            <!-- Tool Quarantine Panel (Spec 032) -->
            <div v-if="quarantinedTools.length > 0" class="alert alert-warning shadow-lg mb-4">
              <svg class="w-6 h-6 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
              </svg>
              <div class="flex-1">
                <h3 class="font-bold">Tool Quarantine</h3>
                <div class="text-sm">
                  {{ quarantinedTools.length }} tool(s) require approval before they can be used by AI agents.
                </div>
              </div>
              <button
                @click="approveAllTools"
                :disabled="approvalLoading"
                class="btn btn-sm btn-warning"
              >
                <span v-if="approvalLoading" class="loading loading-spinner loading-xs"></span>
                Approve All
              </button>
            </div>

            <!-- Quarantined Tools List -->
            <div v-if="quarantinedTools.length > 0" class="space-y-3 mb-6">
              <div
                v-for="tool in quarantinedTools"
                :key="'q-' + tool.tool_name"
                class="card bg-base-200 border-l-4"
                :class="tool.status === 'changed' ? 'border-error' : 'border-warning'"
              >
                <div class="card-body py-3 px-4">
                  <div class="flex items-center justify-between">
                    <div class="flex-1">
                      <div class="flex items-center gap-2">
                        <h4 class="font-semibold">{{ tool.tool_name }}</h4>
                        <span
                          class="badge badge-sm"
                          :class="tool.status === 'changed' ? 'badge-error' : 'badge-warning'"
                        >
                          {{ tool.status }}
                        </span>
                      </div>
                      <p
                        v-if="tool.status !== 'changed' || !tool.previous_description"
                        class="text-sm text-base-content/70 mt-1"
                      >{{ tool.description }}</p>
                      <!-- Show before/after diff for changed tools -->
                      <div v-if="tool.status === 'changed' && tool.previous_description" class="mt-2 space-y-2 text-xs">
                        <div>
                          <div class="text-[10px] font-semibold uppercase tracking-wide text-base-content/60 mb-1">Before (approved)</div>
                          <div class="bg-error/5 border border-error/20 px-2 py-1.5 rounded font-mono leading-relaxed">
                            <template v-for="(part, i) in computeWordDiff(tool.previous_description, tool.current_description || tool.description)" :key="'b'+i">
                              <span v-if="part.type === 'removed'" class="bg-error/20 text-error font-semibold px-0.5 rounded">{{ part.text }}</span>
                              <span v-else-if="part.type === 'same'">{{ part.text }}</span>
                            </template>
                          </div>
                        </div>
                        <div>
                          <div class="text-[10px] font-semibold uppercase tracking-wide text-base-content/60 mb-1">After (current)</div>
                          <div class="bg-success/5 border border-success/20 px-2 py-1.5 rounded font-mono leading-relaxed">
                            <template v-for="(part, i) in computeWordDiff(tool.previous_description, tool.current_description || tool.description)" :key="'a'+i">
                              <span v-if="part.type === 'added'" class="bg-success/20 text-success font-semibold px-0.5 rounded">{{ part.text }}</span>
                              <span v-else-if="part.type === 'same'">{{ part.text }}</span>
                            </template>
                          </div>
                        </div>
                      </div>
                    </div>
                    <button
                      @click="approveTool(tool.tool_name)"
                      :disabled="approvalLoading"
                      class="btn btn-sm btn-outline ml-4"
                    >
                      Approve
                    </button>
                  </div>
                </div>
              </div>
            </div>

            <div class="flex justify-between items-center">
              <div>
                <h3 class="text-lg font-semibold">Available Tools</h3>
                <p class="text-base-content/70">Tools provided by {{ server.name }}</p>
              </div>
              <div class="form-control">
                <input
                  v-model="toolSearch"
                  type="text"
                  placeholder="Search tools..."
                  class="input input-bordered input-sm w-64"
                />
              </div>
            </div>

            <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
              <div
                v-for="tool in filteredTools"
                :key="tool.name"
                class="card bg-base-100 shadow-md"
              >
                <div class="card-body">
                  <div class="flex items-center gap-2">
                    <h4 class="card-title text-lg">{{ tool.name }}</h4>
                    <span
                      v-if="getToolApprovalStatus(tool.name) === 'pending'"
                      class="badge badge-info badge-sm"
                    >new</span>
                    <span
                      v-else-if="getToolApprovalStatus(tool.name) === 'changed'"
                      class="badge badge-warning badge-sm"
                    >changed</span>
                  </div>
                  <p class="text-sm text-base-content/70">
                    {{ tool.description || 'No description available' }}
                  </p>
                  <AnnotationBadges
                    v-if="tool.annotations"
                    :annotations="tool.annotations"
                    class="mt-2"
                  />
                  <div v-if="tool.input_schema" class="card-actions justify-end mt-4">
                    <button
                      class="btn btn-sm btn-outline"
                      @click="viewToolSchema(tool)"
                    >
                      View Schema
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- Logs Tab -->
        <div v-if="activeTab === 'logs'">
          <div class="flex justify-between items-center mb-4">
            <div>
              <h3 class="text-lg font-semibold">Server Logs</h3>
              <p class="text-base-content/70">Recent log entries for {{ server.name }}</p>
            </div>
            <div class="flex items-center space-x-2">
              <select v-model="logTail" class="select select-bordered select-sm">
                <option :value="50">Last 50 lines</option>
                <option :value="100">Last 100 lines</option>
                <option :value="200">Last 200 lines</option>
                <option :value="500">Last 500 lines</option>
              </select>
              <button @click="loadLogs" class="btn btn-sm btn-outline" :disabled="logsLoading">
                <span v-if="logsLoading" class="loading loading-spinner loading-xs"></span>
                Refresh
              </button>
            </div>
          </div>

          <div v-if="logsLoading" class="text-center py-8">
            <span class="loading loading-spinner loading-lg"></span>
            <p class="mt-2">Loading logs...</p>
          </div>

          <div v-else-if="logsError" class="alert alert-error">
            <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <span>{{ logsError }}</span>
            <button @click="loadLogs" class="btn btn-sm">Retry</button>
          </div>

          <div v-else-if="serverLogs.length === 0" class="text-center py-8">
            <svg class="w-16 h-16 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
            </svg>
            <h3 class="text-xl font-semibold mb-2">No logs available</h3>
            <p class="text-base-content/70">No log entries found for this server.</p>
          </div>

          <div v-else class="mockup-code max-h-96 overflow-y-auto">
            <pre v-for="(line, index) in serverLogs" :key="index" class="text-xs"><code>{{ line }}</code></pre>
          </div>
        </div>

        <!-- Configuration Tab -->
        <div v-if="activeTab === 'config'">
          <div class="space-y-6">
            <div>
              <h3 class="text-lg font-semibold mb-4">Server Configuration</h3>
              <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
                <div class="space-y-4">
                  <div>
                    <label class="label">
                      <span class="label-text font-medium">Name</span>
                    </label>
                    <input :value="server.name" readonly class="input input-bordered w-full" />
                  </div>
                  <div>
                    <label class="label">
                      <span class="label-text font-medium">Protocol</span>
                    </label>
                    <input :value="server.protocol" readonly class="input input-bordered w-full" />
                  </div>
                  <div v-if="server.url">
                    <label class="label">
                      <span class="label-text font-medium">URL</span>
                    </label>
                    <input :value="server.url" readonly class="input input-bordered w-full" />
                  </div>
                  <div v-if="server.command">
                    <label class="label">
                      <span class="label-text font-medium">Command</span>
                    </label>
                    <input :value="server.command" readonly class="input input-bordered w-full" />
                  </div>
                </div>
                <div class="space-y-4">
                  <div class="form-control">
                    <label class="label">
                      <span class="label-text font-medium">Enabled</span>
                    </label>
                    <input
                      type="checkbox"
                      :checked="server.enabled"
                      @change="toggleEnabled"
                      class="toggle"
                      :disabled="actionLoading"
                    />
                  </div>
                  <div class="form-control">
                    <label class="label">
                      <span class="label-text font-medium">Quarantined</span>
                    </label>
                    <input
                      type="checkbox"
                      :checked="server.quarantined"
                      readonly
                      class="toggle"
                      disabled
                    />
                  </div>
                  <div>
                    <label class="label">
                      <span class="label-text font-medium">Tools Count</span>
                    </label>
                    <input :value="server.tool_count" readonly class="input input-bordered w-full" />
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- Security Tab (Spec 039) -->
        <div v-if="activeTab === 'security'">
          <div class="space-y-6">
            <!-- Header: Scan button + Risk Score -->
            <div class="flex flex-col sm:flex-row sm:justify-between sm:items-center gap-4">
              <div class="tooltip tooltip-bottom" :data-tip="!dockerAvailable ? 'Docker is required to run security scanners' : (!hasEnabledScanners() ? 'No scanners enabled — install one from Security Scanners' : '')">
                <button
                  v-if="hasEnabledScanners()"
                  @click="startSecurityScan"
                  :disabled="scanLoading || !dockerAvailable"
                  class="btn btn-primary"
                >
                  <span v-if="scanLoading" class="loading loading-spinner loading-xs"></span>
                  <svg v-else class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
                  </svg>
                  {{ scanLoading ? 'Scanning...' : 'Scan Now' }}
                </button>
              </div>
              <button
                v-if="scanLoading"
                @click="cancelSecurityScan"
                class="btn btn-error btn-outline btn-sm"
              >
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                </svg>
                Cancel
              </button>

              <div v-if="(scanReport || server.security_scan) && scanReport?.scan_complete !== false && !scanLoading" class="flex items-center gap-3">
                <div class="text-right">
                  <div class="text-sm text-base-content/70">Risk Score</div>
                  <div class="text-2xl font-bold" :class="riskScoreClass">
                    {{ currentRiskScore }}<span class="text-sm font-normal text-base-content/50">/100</span>
                  </div>
                </div>
                <div
                  class="radial-progress text-sm"
                  :class="riskScoreClass"
                  :style="`--value:${currentRiskScore}; --size:3.5rem; --thickness:4px;`"
                  role="progressbar"
                >
                  {{ currentRiskScore }}
                </div>
              </div>
              <div v-else-if="scanReport?.scan_complete === false && !scanLoading" class="flex items-center gap-2">
                <svg class="w-5 h-5 text-error" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
                <span v-if="scanReport?.empty_scan" class="text-sm text-warning font-medium">No Files Scanned</span>
                <span v-else class="text-sm text-error font-medium">Scan Failed</span>
              </div>
            </div>

            <!-- Scan Progress (visible during active scan) -->
            <div v-if="scanLoading" class="space-y-3">
              <template v-if="scanProgress && scanProgress.total > 0">
                <div class="flex items-center justify-between text-sm">
                  <span class="font-medium">Scanning with {{ scanProgress.total }} scanner{{ scanProgress.total !== 1 ? 's' : '' }}...</span>
                  <span class="text-base-content/60">{{ scanProgress.completed }}/{{ scanProgress.total }} complete</span>
                </div>
                <progress
                  class="progress progress-primary w-full"
                  :value="scanProgress.completed"
                  :max="scanProgress.total"
                ></progress>
                <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-2">
                  <div
                    v-for="ss in scanProgress.scanners"
                    :key="ss.scanner_id"
                    class="flex items-center gap-2 px-3 py-2 rounded-lg bg-base-200"
                  >
                    <span v-if="ss.status === 'running'" class="loading loading-spinner loading-xs text-primary"></span>
                    <span v-else-if="ss.status === 'completed'" class="text-success">&#10003;</span>
                    <span v-else-if="ss.status === 'failed'" class="text-error">&#10007;</span>
                    <span v-else class="text-base-content/30">&#9679;</span>
                    <span class="text-sm truncate flex-1">{{ scannerDisplayName(ss.scanner_id) }}</span>
                    <span v-if="ss.findings_count > 0" class="badge badge-xs badge-error">{{ ss.findings_count }}</span>
                  </div>
                </div>
              </template>
              <template v-else>
                <div class="flex items-center gap-3 text-sm">
                  <span class="loading loading-spinner loading-sm text-primary"></span>
                  <span class="font-medium">Initializing security scan...</span>
                </div>
                <progress class="progress progress-primary w-full"></progress>
              </template>
            </div>

            <!-- Scan Context Banner -->
            <div v-if="scanContext" class="mt-2">
              <!-- No Docker Isolation (local process) -->
              <div v-if="!scanContext.docker_isolation && !isUrlSourceMethod && scanContext.source_method !== 'none' && scanContext.source_method !== 'tool_definitions_only'" class="flex items-start gap-3 p-4 rounded-lg bg-base-200/60 border border-base-300">
                <svg class="w-5 h-5 shrink-0 mt-0.5 text-base-content/60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />
                </svg>
                <div class="min-w-0 flex-1">
                  <h3 class="font-semibold text-sm">Local Process</h3>
                  <p class="text-sm text-base-content/70">Running directly on the host, without Docker isolation.</p>
                  <p class="text-sm text-base-content/70">
                    Source: <code class="bg-base-300 px-1 rounded text-xs">{{ scanContext.source_path }}</code>
                    <span v-if="scanContext.total_files"> ({{ scanContext.total_files }} files, {{ formatFileSize(scanContext.total_size_bytes) }})</span>
                  </p>
                  <p class="text-sm text-base-content/60">
                    Protocol: {{ scanContext.server_protocol }}
                    <span v-if="scanContext.server_command"> &bull; Command: {{ scanContext.server_command }}</span>
                  </p>
                </div>
              </div>

              <!-- Docker Isolated -->
              <div v-else-if="scanContext.docker_isolation" class="flex items-start gap-3 p-4 rounded-lg bg-base-200/60 border border-base-300">
                <svg class="w-5 h-5 shrink-0 mt-0.5 text-base-content/60" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2" />
                </svg>
                <div class="min-w-0 flex-1">
                  <h3 class="font-semibold text-sm">Docker Isolated</h3>
                  <p class="text-sm text-base-content/70">
                    Source extracted from container<span v-if="scanContext.container_id">: <code class="bg-base-300 px-1 rounded text-xs">{{ scanContext.container_id.substring(0, 12) }}...</code></span>
                  </p>
                  <p class="text-sm text-base-content/70">
                    Source: <code class="bg-base-300 px-1 rounded text-xs">{{ scanContext.source_path }}</code>
                    <span v-if="scanContext.total_files"> ({{ scanContext.total_files }} files, {{ formatFileSize(scanContext.total_size_bytes) }})</span>
                  </p>
                  <p class="text-sm text-base-content/60">
                    Protocol: {{ scanContext.server_protocol }}
                    <span v-if="scanContext.server_command"> &bull; Command: {{ scanContext.server_command }}</span>
                  </p>
                </div>
              </div>

              <!-- HTTP Server (url, url_full, or tool_definitions_only for http protocol) -->
              <div v-else-if="isUrlSourceMethod || scanContext.source_method === 'tool_definitions_only'" class="flex items-start gap-3 p-4 rounded-lg bg-base-200/60 border border-base-300">
                <svg class="w-5 h-5 shrink-0 mt-0.5 text-base-content/60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 12a9 9 0 01-9 9m9-9a9 9 0 00-9-9m9 9H3m9 9a9 9 0 01-9-9m9 9c1.657 0 3-4.03 3-9s-1.343-9-3-9m0 18c-1.657 0-3-4.03-3-9s1.343-9 3-9m-9 9a9 9 0 019-9" />
                </svg>
                <div class="min-w-0 flex-1">
                  <h3 class="font-semibold text-sm">{{ isUrlSourceMethod ? 'HTTP Server' : 'Tool Definitions Only' }}</h3>
                  <p class="text-sm text-base-content/70">{{ isUrlSourceMethod ? 'Tool description scanning only (no filesystem to scan)' : 'Scanning tool descriptions for poisoning and injection attacks' }}</p>
                  <p v-if="isUrlSourceMethod && scanContext.source_path" class="text-sm text-base-content/70">
                    URL: <code class="bg-base-300 px-1 rounded text-xs">{{ scanContext.source_path }}</code>
                  </p>
                  <p v-if="scanContext.tools_exported" class="text-sm text-base-content/60">
                    {{ scanContext.tools_exported }} tool definitions exported for analysis
                  </p>
                </div>
              </div>

              <!-- No Source Available -->
              <div v-else-if="scanContext.source_method === 'none'" class="alert alert-error">
                <svg class="w-6 h-6 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
                <div>
                  <h3 class="font-bold">No Source Available</h3>
                  <p class="text-sm">Could not resolve source files for scanning.</p>
                  <p class="text-sm text-base-content/70">Server may be disconnected or not running in Docker.</p>
                </div>
              </div>
            </div>

            <!-- Scan error -->
            <div v-if="scanError" class="alert alert-error">
              <svg class="w-5 h-5 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
              </svg>
              <span>{{ scanError }}</span>
              <button @click="scanError = null; startSecurityScan()" class="btn btn-sm btn-ghost">Retry</button>
            </div>

            <!-- Loading state for report -->
            <div v-if="scanReportLoading && !scanLoading" class="text-center py-8">
              <span class="loading loading-spinner loading-lg"></span>
              <p class="mt-2">Loading scan report...</p>
            </div>

            <!-- Not scanned yet -->
            <div v-else-if="!scanReport && !scanLoading && securityScanStatus === 'not_scanned'" class="text-center py-12">
              <svg class="w-16 h-16 mx-auto mb-4 opacity-40" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
              </svg>
              <h3 class="text-xl font-semibold mb-2">No Security Scan</h3>
              <p class="text-base-content/70 mb-4">
                This server has not been scanned yet. Click "Scan Now" to check for security issues.
              </p>
            </div>

            <!-- Scan results summary (hidden during active scan) -->
            <template v-else-if="scanReport && !scanLoading">
              <!-- Risk Score + Summary -->
              <div class="flex items-center gap-6 mb-4">
                <div class="text-center">
                  <div class="text-3xl font-bold" :class="scanReport.risk_score >= 70 ? 'text-error' : scanReport.risk_score >= 40 ? 'text-warning' : 'text-success'">
                    {{ scanReport.empty_scan ? 'N/A' : scanReport.risk_score + '/100' }}
                  </div>
                  <div class="text-xs text-base-content/50">Risk Score</div>
                </div>
                <div class="flex gap-4 text-sm">
                  <span v-if="scanReport.summary?.dangerous" class="text-error font-semibold">{{ scanReport.summary.dangerous }} dangerous</span>
                  <span v-if="scanReport.summary?.warnings" class="text-warning font-semibold">{{ scanReport.summary.warnings }} warnings</span>
                  <span v-if="scanReport.summary?.info_level" class="text-info">{{ scanReport.summary.info_level }} info</span>
                  <span v-if="scanReport.summary?.total === 0" class="text-success font-semibold">No findings</span>
                </div>
              </div>

              <!-- Scan metadata -->
              <div class="text-sm text-base-content/60 mb-4">
                <span v-if="scanReport.job_id">Scan ID: <code class="bg-base-200 px-1 rounded text-xs">{{ scanReport.job_id.substring(0, 8) }}</code></span>
                <span v-if="scanReport.scanned_at" class="ml-4">{{ new Date(scanReport.scanned_at).toLocaleString() }}</span>
                <span v-if="scanReport.pass2_running" class="ml-4 badge badge-sm badge-info">Pass 2 running...</span>
                <span v-else-if="scanReport.pass2_complete" class="ml-4 badge badge-sm badge-success">Pass 2 complete</span>
              </div>

              <!-- Action buttons -->
              <div class="flex gap-3">
                <router-link v-if="scanReport.job_id" :to="`/security/scans/${scanReport.job_id}`" class="btn btn-primary btn-sm">
                  View Full Report &rarr;
                </router-link>
              </div>
            </template>
          </div>
        </div>
      </div>
    </div>

    <!-- Tool Schema Modal -->
    <div v-if="selectedToolSchema" class="modal modal-open">
      <div class="modal-box max-w-4xl">
        <h3 class="font-bold text-lg mb-4">{{ selectedToolSchema.name }} - Input Schema</h3>
        <div class="mockup-code">
          <pre><code>{{ JSON.stringify(selectedToolSchema.input_schema, null, 2) }}</code></pre>
        </div>
        <div class="modal-action">
          <button class="btn" @click="selectedToolSchema = null">Close</button>
        </div>
      </div>
    </div>

    <!-- Hints Panel (Bottom of Page) -->
    <CollapsibleHintsPanel :hints="serverDetailHints" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import AnnotationBadges from '@/components/AnnotationBadges.vue'
import ErrorPanel from '@/components/diagnostics/ErrorPanel.vue'
import type { Hint } from '@/components/CollapsibleHintsPanel.vue'
import type { Server, Tool, ToolApproval, SecurityScanReport } from '@/types'
import api from '@/services/api'
import { useSecurityScannerStatus } from '@/composables/useSecurityScannerStatus'

interface Props {
  serverName: string
}

const props = defineProps<Props>()
const route = useRoute()

const serversStore = useServersStore()
const systemStore = useSystemStore()

// State
const loading = ref(true)
const error = ref<string | null>(null)
const server = ref<Server | null>(null)
const activeTab = ref<'tools' | 'logs' | 'config' | 'security'>('tools')
const actionLoading = ref(false)

// Tools
const serverTools = ref<Tool[]>([])
const toolsLoading = ref(false)
const toolsError = ref<string | null>(null)
const toolSearch = ref('')
const selectedToolSchema = ref<Tool | null>(null)

// Tool quarantine (Spec 032)
const toolApprovals = ref<ToolApproval[]>([])
const approvalLoading = ref(false)

const quarantinedTools = computed(() => {
  return toolApprovals.value.filter(t => t.status === 'pending' || t.status === 'changed')
})

// Security scan (Spec 039)
const dockerAvailable = ref(true) // optimistic default until overview loads
const { hasEnabledScanners } = useSecurityScannerStatus()
const scanReport = ref<SecurityScanReport | null>(null)
const scanStatus = ref<any>(null)
const scanLoading = ref(false)
const scanReportLoading = ref(false)
const scanError = ref<string | null>(null)
const activeScanJobId = ref<string | null>(null) // Track the active scan job ID
const scannerNameMap = ref<Record<string, string>>({}) // scanner_id → human-readable name
let scanPollTimer: ReturnType<typeof setInterval> | null = null

// Scan context & files
const scanFiles = ref<Array<{ path: string; suspicious: boolean; findings?: string[] }>>([])
const scanFilesLoading = ref(false)
const scanFilesLoaded = ref(false)
const scanFilesPass = ref(1) // 1 = security scan (source), 2 = supply chain (full deps)
const scanFilesMeta = ref<{ total: number; has_more: boolean; suspicious_count: number; offset: number }>({
  total: 0, has_more: false, suspicious_count: 0, offset: 0
})

const scanContext = computed(() => {
  return scanStatus.value?.scan_context || null
})

// Whether the scan source method indicates a URL-based server (HTTP/SSE)
const isUrlSourceMethod = computed(() => {
  const method = scanContext.value?.source_method || ''
  return method === 'url' || method === 'url_full'
})

// Logs
const serverLogs = ref<string[]>([])
const logsLoading = ref(false)
const logsError = ref<string | null>(null)
const logTail = ref(100)

// Computed
const isHttpProtocol = computed(() => {
  return server.value?.protocol === 'http' || server.value?.protocol === 'streamable-http'
})

// Suggested action from unified health status
const healthAction = computed(() => {
  return server.value?.health?.action || ''
})

// Spec 044 — render the structured diagnostic panel whenever a warn/error
// diagnostic is attached. Info-level diagnostics are ignored (shown only in
// verbose/admin views, per spec).
const showDiagnosticPanel = computed(() => {
  const d = server.value?.diagnostic
  if (!d || !d.code) return false
  return d.severity === 'warn' || d.severity === 'error'
})

function handleDiagnosticFixed(_payload: { fixerKey: string; mode: 'dry_run' | 'execute' }) {
  // Trigger a silent refresh so the diagnostic disappears once the server
  // reconnects. The SSE stream will also push an update, but an explicit
  // refresh provides a more responsive UI when the user clicks "Execute".
  void serversStore.fetchServers(true)
}

// Security scan computed properties
const securityScanStatus = computed(() => {
  if (scanLoading.value) return 'scanning'
  return server.value?.security_scan?.status || 'not_scanned'
})

// Resolve scanner ID to human-readable name
function scannerDisplayName(scannerId: string): string {
  return scannerNameMap.value[scannerId] || scannerId
}

// Per-scanner progress during active scan
const scanProgress = computed(() => {
  if (!scanStatus.value?.scanner_statuses) return null
  const statuses = scanStatus.value.scanner_statuses as Array<{
    scanner_id: string; status: string; findings_count: number; error?: string
  }>
  const total = statuses.length
  const completed = statuses.filter(s => s.status === 'completed' || s.status === 'failed').length
  return { total, completed, scanners: statuses }
})

const securityDotClass = computed(() => {
  switch (securityScanStatus.value) {
    case 'clean': return 'bg-success'
    case 'warnings': return 'bg-warning'
    case 'dangerous': return 'bg-error'
    case 'failed': return 'bg-error'
    case 'scanning': return '' // handled by spinner
    default: return 'bg-base-content/30'
  }
})

const securityTabSuffix = computed(() => {
  const scan = server.value?.security_scan
  if (!scan?.last_scan_at) return ''
  return ` (${formatRelativeTime(scan.last_scan_at)})`
})

const currentRiskScore = computed(() => {
  if (scanReport.value) return scanReport.value.risk_score
  return server.value?.security_scan?.risk_score ?? 0
})

const riskScoreClass = computed(() => {
  const score = currentRiskScore.value
  if (score >= 70) return 'text-error'
  if (score >= 30) return 'text-warning'
  return 'text-success'
})

const filteredTools = computed(() => {
  if (!toolSearch.value) return serverTools.value

  const query = toolSearch.value.toLowerCase()
  return serverTools.value.filter(tool =>
    tool.name.toLowerCase().includes(query) ||
    tool.description?.toLowerCase().includes(query)
  )
})

// Tool approval status lookup for the main tools grid
function getToolApprovalStatus(toolName: string): string | null {
  const approval = toolApprovals.value.find(t => t.tool_name === toolName)
  if (!approval) return null
  return approval.status
}

// Word-level diff for changed tool descriptions
interface DiffPart {
  type: 'same' | 'added' | 'removed'
  text: string
}

/// Generic LCS over arrays of strings (works for word tokens or single chars).
function lcsDiff(oldElems: string[], newElems: string[]): DiffPart[] {
  const m = oldElems.length
  const n = newElems.length
  if (m === 0 && n === 0) return []
  if (m === 0) return newElems.map(t => ({ type: 'added', text: t }))
  if (n === 0) return oldElems.map(t => ({ type: 'removed', text: t }))

  const dp: number[][] = Array.from({ length: m + 1 }, () => Array(n + 1).fill(0))
  for (let i = 1; i <= m; i++) {
    for (let j = 1; j <= n; j++) {
      if (oldElems[i - 1] === newElems[j - 1]) {
        dp[i][j] = dp[i - 1][j - 1] + 1
      } else {
        dp[i][j] = Math.max(dp[i - 1][j], dp[i][j - 1])
      }
    }
  }

  const out: DiffPart[] = []
  let i = m, j = n
  while (i > 0 || j > 0) {
    if (i > 0 && j > 0 && oldElems[i - 1] === newElems[j - 1]) {
      out.push({ type: 'same', text: oldElems[i - 1] })
      i--; j--
    } else if (j > 0 && (i === 0 || dp[i][j - 1] >= dp[i - 1][j])) {
      out.push({ type: 'added', text: newElems[j - 1] })
      j--
    } else {
      out.push({ type: 'removed', text: oldElems[i - 1] })
      i--
    }
  }
  return out.reverse()
}

/// Char-level diff for short strings, with a safety cap on input length
/// to keep the O(N×M) dp table bounded.
function characterLevelDiff(oldText: string, newText: string, maxChars = 1500): DiffPart[] {
  if (oldText.length > maxChars || newText.length > maxChars) {
    return [
      { type: 'removed', text: oldText },
      { type: 'added', text: newText },
    ]
  }
  return lcsDiff(Array.from(oldText), Array.from(newText))
}

function mergeSameKind(parts: DiffPart[]): DiffPart[] {
  const out: DiffPart[] = []
  for (const p of parts) {
    const last = out[out.length - 1]
    if (last && last.type === p.type) {
      last.text += p.text
    } else {
      out.push({ ...p })
    }
  }
  return out
}

/// Word-level diff with character-level refinement inside adjacent
/// (removed, added) pairs. Keeps whole-token highlights for large docstring
/// expansions while narrowing substring changes like "1 April" → "8 April"
/// down to just the differing characters.
function computeWordDiff(oldText: string, newText: string): DiffPart[] {
  const oldWords = oldText.split(/(\s+)/).filter(t => t.length > 0)
  const newWords = newText.split(/(\s+)/).filter(t => t.length > 0)
  const wordDiff = mergeSameKind(lcsDiff(oldWords, newWords))

  const refined: DiffPart[] = []
  for (let idx = 0; idx < wordDiff.length; idx++) {
    const current = wordDiff[idx]
    const next = wordDiff[idx + 1]
    if (
      next &&
      ((current.type === 'removed' && next.type === 'added') ||
        (current.type === 'added' && next.type === 'removed'))
    ) {
      const removedText = current.type === 'removed' ? current.text : next.text
      const addedText = current.type === 'added' ? current.text : next.text
      refined.push(...characterLevelDiff(removedText, addedText))
      idx++ // skip the paired part
      continue
    }
    refined.push(current)
  }
  return mergeSameKind(refined)
}

// Methods
async function loadServerDetails() {
  loading.value = true
  error.value = null

  try {
    await serversStore.fetchServers()
    server.value = serversStore.servers.find(s => s.name === props.serverName) || null

    if (!server.value) {
      error.value = `Server "${props.serverName}" not found`
      return
    }

    // Load tools, approvals, and logs in parallel
    await Promise.all([
      loadTools(),
      loadToolApprovals(),
      loadLogs()
    ])
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to load server details'
  } finally {
    loading.value = false
  }
}

async function loadTools() {
  if (!server.value) return

  toolsLoading.value = true
  toolsError.value = null

  try {
    const response = await api.getServerTools(server.value.name)
    if (response.success && response.data) {
      serverTools.value = response.data.tools || []
    } else {
      toolsError.value = response.error || 'Failed to load tools'
    }
  } catch (err) {
    toolsError.value = err instanceof Error ? err.message : 'Failed to load tools'
  } finally {
    toolsLoading.value = false
  }
}

// Tool quarantine functions (Spec 032)
async function loadToolApprovals() {
  if (!server.value) return
  try {
    const response = await api.getToolApprovals(server.value.name)
    if (response.success && response.data) {
      const approvals = response.data.tools || []

      // Fetch diffs for changed tools to populate previous_description
      const changedTools = approvals.filter(t => t.status === 'changed')
      if (changedTools.length > 0) {
        const diffPromises = changedTools.map(async (tool) => {
          try {
            const diffResp = await api.getToolDiff(server.value!.name, tool.tool_name)
            if (diffResp.success && diffResp.data) {
              tool.previous_description = diffResp.data.previous_description
              tool.current_description = diffResp.data.current_description
            }
          } catch {
            // Diff fetch failed, continue without it
          }
        })
        await Promise.all(diffPromises)
      }

      toolApprovals.value = approvals
    }
  } catch {
    // Silently fail - tool approvals are supplementary info
  }
}

async function approveTool(toolName: string) {
  if (!server.value) return
  approvalLoading.value = true
  try {
    const response = await api.approveTools(server.value.name, [toolName])
    if (response.success) {
      systemStore.addToast({
        type: 'success',
        title: 'Tool Approved',
        message: `${toolName} has been approved`,
      })
      await loadToolApprovals()
      // Refresh server data to update quarantine counts
      await serversStore.fetchServers()
      server.value = serversStore.servers.find(s => s.name === props.serverName) || null
    } else {
      systemStore.addToast({
        type: 'error',
        title: 'Approval Failed',
        message: response.error || 'Failed to approve tool',
      })
    }
  } catch (err) {
    systemStore.addToast({
      type: 'error',
      title: 'Approval Failed',
      message: err instanceof Error ? err.message : 'Failed to approve tool',
    })
  } finally {
    approvalLoading.value = false
  }
}

async function approveAllTools() {
  if (!server.value) return
  approvalLoading.value = true
  try {
    const response = await api.approveTools(server.value.name)
    if (response.success) {
      systemStore.addToast({
        type: 'success',
        title: 'Tools Approved',
        message: `All tools for ${server.value.name} have been approved`,
      })
      await loadToolApprovals()
      // Refresh server data to update quarantine counts
      await serversStore.fetchServers()
      server.value = serversStore.servers.find(s => s.name === props.serverName) || null
    } else {
      systemStore.addToast({
        type: 'error',
        title: 'Approval Failed',
        message: response.error || 'Failed to approve tools',
      })
    }
  } catch (err) {
    systemStore.addToast({
      type: 'error',
      title: 'Approval Failed',
      message: err instanceof Error ? err.message : 'Failed to approve tools',
    })
  } finally {
    approvalLoading.value = false
  }
}

async function loadLogs() {
  if (!server.value) return

  logsLoading.value = true
  logsError.value = null

  try {
    const response = await api.getServerLogs(server.value.name, logTail.value)
    if (response.success && response.data) {
      serverLogs.value = response.data.logs || []
    } else {
      logsError.value = response.error || 'Failed to load logs'
    }
  } catch (err) {
    logsError.value = err instanceof Error ? err.message : 'Failed to load logs'
  } finally {
    logsLoading.value = false
  }
}

async function toggleEnabled() {
  if (!server.value) return

  actionLoading.value = true
  try {
    if (server.value.enabled) {
      await serversStore.disableServer(server.value.name)
      systemStore.addToast({
        type: 'success',
        title: 'Server Disabled',
        message: `${server.value.name} has been disabled`,
      })
    } else {
      await serversStore.enableServer(server.value.name)
      systemStore.addToast({
        type: 'success',
        title: 'Server Enabled',
        message: `${server.value.name} has been enabled`,
      })
    }
    // Update local server reference
    await serversStore.fetchServers()
    server.value = serversStore.servers.find(s => s.name === props.serverName) || null
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Operation Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    actionLoading.value = false
  }
}

async function restartServer() {
  if (!server.value) return

  actionLoading.value = true
  try {
    await serversStore.restartServer(server.value.name)
    systemStore.addToast({
      type: 'success',
      title: 'Server Restarted',
      message: `${server.value.name} is restarting`,
    })
    // Refresh server data after restart
    setTimeout(async () => {
      await serversStore.fetchServers()
      server.value = serversStore.servers.find(s => s.name === props.serverName) || null
    }, 2000)
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Restart Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    actionLoading.value = false
  }
}

async function triggerOAuth() {
  if (!server.value) return

  actionLoading.value = true
  try {
    await serversStore.triggerOAuthLogin(server.value.name)
    systemStore.addToast({
      type: 'success',
      title: 'OAuth Login Triggered',
      message: `Check your browser for ${server.value.name} login`,
    })
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'OAuth Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    actionLoading.value = false
  }
}

async function quarantineServer() {
  if (!server.value) return

  actionLoading.value = true
  try {
    await serversStore.quarantineServer(server.value.name)
    systemStore.addToast({
      type: 'success',
      title: 'Server Quarantined',
      message: `${server.value.name} has been quarantined`,
    })
    // Update local server reference
    await serversStore.fetchServers()
    server.value = serversStore.servers.find(s => s.name === props.serverName) || null
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Quarantine Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    actionLoading.value = false
  }
}

async function unquarantineServer() {
  if (!server.value) return

  actionLoading.value = true
  try {
    await serversStore.unquarantineServer(server.value.name)
    systemStore.addToast({
      type: 'success',
      title: 'Server Unquarantined',
      message: `${server.value.name} has been removed from quarantine`,
    })
    // Update local server reference
    await serversStore.fetchServers()
    server.value = serversStore.servers.find(s => s.name === props.serverName) || null
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Unquarantine Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    actionLoading.value = false
  }
}

// --- Security-aware approval flow (F-04) ---
// Approve buttons go through POST /security/approve which enforces the
// scanner gate before unquarantining the server. Force is only used after
// the user explicitly confirms in the dialog.
const showApproveConfirmation = ref(false)
const approveDialogMode = ref<'no_scan' | 'critical'>('no_scan')

const criticalFindingCount = computed(() => {
  // Prefer the loaded scan report summary if available; otherwise fall back
  // to finding_counts on the server's security_scan summary (if populated).
  const rep = scanReport.value as any
  if (rep?.summary?.critical != null) return rep.summary.critical as number
  const scan = server.value?.security_scan as any
  if (scan?.finding_counts?.critical != null) return scan.finding_counts.critical as number
  return 0
})

const hasCompletedScanForApprove = computed(() => {
  if (scanReport.value) return true
  return !!server.value?.security_scan?.last_scan_at
})

function handleApproveClick() {
  if (!server.value) return
  if (!hasCompletedScanForApprove.value) {
    approveDialogMode.value = 'no_scan'
    showApproveConfirmation.value = true
    return
  }
  if (criticalFindingCount.value > 0) {
    approveDialogMode.value = 'critical'
    showApproveConfirmation.value = true
    return
  }
  void doSecurityApprove(false)
}

async function doSecurityApprove(force: boolean) {
  if (!server.value) return
  actionLoading.value = true
  try {
    await serversStore.securityApproveServer(server.value.name, force)
    systemStore.addToast({
      type: 'success',
      title: 'Server Approved',
      message: `${server.value.name} has been approved and unquarantined`,
    })
    showApproveConfirmation.value = false
    await serversStore.fetchServers()
    server.value = serversStore.servers.find(s => s.name === props.serverName) || null
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Approve Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    actionLoading.value = false
  }
}

function confirmForceApprove() {
  void doSecurityApprove(true)
}

async function scanFirstFromDialog() {
  showApproveConfirmation.value = false
  activeTab.value = 'security'
  // Kick off a scan; the Security tab will show progress. User can return to
  // approve once the scan completes.
  await startSecurityScan()
}

async function refreshData() {
  await loadServerDetails()
}

async function discoverTools() {
  if (!server.value) return

  actionLoading.value = true
  try {
    const response = await api.discoverServerTools(server.value.name)

    if (!response.success) {
      throw new Error(response.error || 'Failed to discover tools')
    }

    systemStore.addToast({
      type: 'success',
      title: 'Tool Discovery Started',
      message: `Discovering tools for ${server.value.name}...`,
    })

    // Refresh server details after a short delay to show updated tool count
    setTimeout(async () => {
      await loadServerDetails()
      systemStore.addToast({
        type: 'info',
        title: 'Tools Updated',
        message: `Tool cache refreshed for ${server.value?.name}`,
      })
    }, 2000)
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Tool Discovery Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  } finally {
    actionLoading.value = false
  }
}

function viewToolSchema(tool: Tool) {
  selectedToolSchema.value = tool
}

// Security scan functions (Spec 039)
function formatFileSize(bytes: number): string {
  if (!bytes || bytes === 0) return '0 B'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

async function onScannedFilesToggle(event: Event) {
  const checkbox = event.target as HTMLInputElement
  if (checkbox.checked && !scanFilesLoaded.value && server.value) {
    await loadScanFiles(0)
  }
}

async function loadScanFiles(offset: number) {
  if (!server.value) return
  scanFilesLoading.value = true
  try {
    const response = await api.getScanFiles(server.value.name, 100, offset, scanFilesPass.value)
    if (response.success && response.data) {
      if (offset === 0) {
        scanFiles.value = response.data.files || []
      } else {
        scanFiles.value = [...scanFiles.value, ...(response.data.files || [])]
      }
      scanFilesMeta.value = {
        total: response.data.total_files || 0,
        has_more: response.data.has_more || false,
        suspicious_count: response.data.suspicious_count || 0,
        offset: offset + (response.data.files?.length || 0),
      }
      scanFilesLoaded.value = true
    }
  } catch {
    // Silently fail
  } finally {
    scanFilesLoading.value = false
  }
}

async function loadMoreFiles() {
  await loadScanFiles(scanFilesMeta.value.offset)
}

async function switchFilesPass(pass: number) {
  scanFilesPass.value = pass
  scanFiles.value = []
  scanFilesLoaded.value = false
  await loadScanFiles(0)
}

function formatRelativeTime(isoString: string): string {
  const date = new Date(isoString)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffMin = Math.floor(diffMs / 60000)
  if (diffMin < 1) return 'just now'
  if (diffMin < 60) return `${diffMin}m ago`
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return `${diffHr}h ago`
  const diffDay = Math.floor(diffHr / 24)
  return `${diffDay}d ago`
}

function stopScanPolling() {
  if (scanPollTimer) {
    clearInterval(scanPollTimer)
    scanPollTimer = null
  }
}

// Load scanner name map from the scanners API (for human-readable names)
async function loadScannerNames() {
  if (Object.keys(scannerNameMap.value).length > 0) return // Already loaded
  try {
    const resp = await api.listScanners()
    if (resp.success && resp.data) {
      const map: Record<string, string> = {}
      for (const s of resp.data) {
        if (s.id && s.name) map[s.id] = s.name
      }
      scannerNameMap.value = map
    }
  } catch {
    // Not critical
  }
}

async function loadScanReport(force = false) {
  if (!server.value) return
  // Only load if we have a previous scan (skip check when force-loading after scan completion)
  if (!force && !server.value.security_scan?.last_scan_at && !scanReport.value) return

  scanReportLoading.value = true
  scanError.value = null
  try {
    // Check Docker availability for scan button
    api.getSecurityOverview().then(res => {
      if (res.success && res.data) {
        dockerAvailable.value = res.data.docker_available !== false
      }
    })

    const [reportRes, statusRes] = await Promise.all([
      api.getScanReport(server.value.name),
      api.getScanStatus(server.value.name),
    ])
    if (reportRes.success && reportRes.data) {
      scanReport.value = reportRes.data as SecurityScanReport
    }
    if (statusRes.success && statusRes.data) {
      scanStatus.value = statusRes.data
      // If scan is still running (e.g., page reload during scan), resume polling
      if (statusRes.data.status === 'running' || statusRes.data.status === 'pending') {
        activeScanJobId.value = statusRes.data.id
        scanLoading.value = true
        startScanPolling()
      }
    }
  } catch (err) {
    // Silently fail - report may not exist yet
  } finally {
    scanReportLoading.value = false
  }
}

function startScanPolling() {
  stopScanPolling()
  scanPollTimer = setInterval(async () => {
    if (!server.value) { stopScanPolling(); return }
    try {
      const statusResp = await api.getScanStatus(server.value.name)
      if (statusResp.success && statusResp.data) {
        // Update scan status for live progress display
        scanStatus.value = statusResp.data
        const jobId = statusResp.data.id
        const status = statusResp.data.status

        // Only react to the active job (Pass 1). Ignore completed Pass 2 from previous runs.
        if (activeScanJobId.value && jobId !== activeScanJobId.value) {
          // Different job — could be Pass 2 starting after Pass 1 completed.
          if (statusResp.data.scan_pass === 2) {
            // Pass 2 started or completed — Pass 1 is done. Finish polling.
            stopScanPolling()
            scanLoading.value = false
            activeScanJobId.value = null
            await loadScanReport(true)
            await serversStore.fetchServers()
            server.value = serversStore.servers.find(s => s.name === props.serverName) || null
            systemStore.addToast({ type: 'success', title: 'Scan Complete', message: `Security scan for ${server.value?.name} finished.` })
          }
          return
        }

        if (status === 'completed' || status === 'complete') {
          stopScanPolling()
          scanLoading.value = false
          activeScanJobId.value = null
          await loadScanReport(true)
          await serversStore.fetchServers()
          server.value = serversStore.servers.find(s => s.name === props.serverName) || null
          systemStore.addToast({ type: 'success', title: 'Scan Complete', message: `Security scan for ${server.value?.name} finished.` })
        } else if (status === 'failed' || status === 'error') {
          stopScanPolling()
          scanLoading.value = false
          activeScanJobId.value = null
          scanError.value = statusResp.data.error || 'Scan failed'
        }
      }
    } catch {
      // Polling error, keep trying
    }
  }, 2000) // Poll every 2s for smoother progress updates
}

async function startSecurityScan() {
  if (!server.value || scanLoading.value) return

  scanLoading.value = true
  scanError.value = null
  scanReport.value = null
  scanStatus.value = null
  scanFiles.value = []
  scanFilesLoaded.value = false

  try {
    const response = await api.startScan(server.value.name)
    if (!response.success) {
      // Check if scan is already in progress
      const errMsg = response.error || ''
      if (errMsg.includes('already in progress')) {
        // Not an error — just start polling the existing scan
        const match = errMsg.match(/\(job ([\w-]+)\)/)
        activeScanJobId.value = match ? match[1] : null
        systemStore.addToast({ type: 'info', title: 'Scan In Progress', message: 'A scan is already running for this server.' })
        startScanPolling()
        return
      }
      throw new Error(errMsg || 'Failed to start scan')
    }

    // Track the new job ID
    if (response.data?.id) {
      activeScanJobId.value = response.data.id
    }

    systemStore.addToast({
      type: 'info',
      title: 'Security Scan Started',
      message: `Scanning ${server.value.name} for security issues...`,
    })

    startScanPolling()
  } catch (err) {
    scanLoading.value = false
    scanError.value = err instanceof Error ? err.message : 'Failed to start scan'
  }
}

async function cancelSecurityScan() {
  if (!server.value) return
  try {
    await api.cancelScan(server.value.name)
    stopScanPolling()
    scanLoading.value = false
    scanError.value = null
    activeScanJobId.value = null
  } catch (err: any) {
    scanError.value = err.response?.data?.error || 'Failed to cancel scan'
  }
}


// Server detail hints
const serverDetailHints = computed<Hint[]>(() => {
  const hints: Hint[] = [
    {
      icon: '🔧',
      title: 'Server Management',
      description: 'Control and monitor this MCP server',
      sections: [
        {
          title: 'Enable/Disable server',
          codeBlock: {
            language: 'bash',
            code: `# Disable server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"update","name":"${props.serverName}","enabled":false}'\n\n# Enable server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"update","name":"${props.serverName}","enabled":true}'`
          }
        },
        {
          title: 'View server logs',
          codeBlock: {
            language: 'bash',
            code: `# Real-time logs for this server\ntail -f ~/.mcpproxy/logs/server-${props.serverName}.log`
          }
        }
      ]
    },
    {
      icon: '🛠️',
      title: 'Working with Tools',
      description: 'Use tools provided by this server',
      sections: [
        {
          title: 'List all tools',
          codeBlock: {
            language: 'bash',
            code: `# List tools from this server\nmcpproxy tools list --server=${props.serverName}`
          }
        },
        {
          title: 'Call a tool',
          text: 'Tools are prefixed with server name:',
          codeBlock: {
            language: 'bash',
            code: `# Call tool from this server\nmcpproxy call tool --tool-name=${props.serverName}:tool-name \\\n  --json_args='{"arg1":"value1"}'`
          }
        }
      ]
    },
    {
      icon: '💡',
      title: 'Troubleshooting',
      description: 'Common issues and solutions',
      sections: [
        {
          title: 'Connection issues',
          list: [
            'Check if server is enabled in configuration',
            'Review server logs for error messages',
            'Verify network connectivity for remote servers',
            'Check authentication credentials for OAuth servers'
          ]
        },
        {
          title: 'OAuth authentication',
          text: 'If server requires OAuth login:',
          codeBlock: {
            language: 'bash',
            code: `# Trigger OAuth login\nmcpproxy auth login --server=${props.serverName}`
          }
        }
      ]
    }
  ]

  return hints
})

// Watch for log tail changes
watch(logTail, () => {
  loadLogs()
})

// Load data on mount
onMounted(() => {
  // Read tab from query parameter (e.g., ?tab=security)
  const tabParam = route.query.tab as string
  if (tabParam && ['tools', 'logs', 'config', 'security'].includes(tabParam)) {
    activeTab.value = tabParam as typeof activeTab.value
  }
  loadServerDetails().then(() => {
    // Pre-load scanner names and report if opening security tab
    if (activeTab.value === 'security') {
      loadScannerNames()
      loadScanReport()
    }
  })
})

// Cleanup polling on unmount
onUnmounted(() => {
  stopScanPolling()
})
</script>
