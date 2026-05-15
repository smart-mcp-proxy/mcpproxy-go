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
            <div
              class="stat-desc"
              :class="blockedToolCount > 0 ? 'text-error' : ''"
            >
              {{ blockedToolCount > 0 ? `${blockedToolCount} disabled` : 'available tools' }}
            </div>
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
                    <template v-if="isToolConfigDenied(tool.tool_name)">
                      <span
                        class="badge badge-error badge-sm ml-4 self-center"
                        title="Tool is denied by mcp_config.json; approval has no effect while the config lock is active"
                      >locked by config</span>
                    </template>
                    <button
                      v-else
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

            <div class="flex flex-col lg:flex-row lg:justify-between lg:items-center gap-3">
              <div>
                <h3 class="text-lg font-semibold">Available Tools</h3>
                <p class="text-base-content/70">Tools provided by {{ server.name }}</p>
              </div>
              <div class="flex items-center gap-2 flex-wrap">
                <!--
                  Bulk Enable/Disable. Both buttons surface only when there's
                  something for them to do — "Enable All" appears when at
                  least one tool is currently disabled, "Disable All" when at
                  least one is currently enabled. That way the action label
                  always matches a real, observable outcome.
                -->
                <button
                  v-if="hasDisabledTool"
                  class="btn btn-sm btn-success"
                  :disabled="bulkToolToggleLoading"
                  @click="bulkToggleAllTools(true)"
                  data-test="tools-enable-all"
                >
                  <span v-if="bulkToolToggleLoading" class="loading loading-spinner loading-xs"></span>
                  Enable All
                </button>
                <button
                  v-if="hasEnabledTool"
                  class="btn btn-sm btn-warning"
                  :disabled="bulkToolToggleLoading"
                  @click="bulkToggleAllTools(false)"
                  data-test="tools-disable-all"
                >
                  <span v-if="bulkToolToggleLoading" class="loading loading-spinner loading-xs"></span>
                  Disable All
                </button>
                <div class="form-control">
                  <input
                    v-model="toolSearch"
                    type="text"
                    placeholder="Search tools..."
                    class="input input-bordered input-sm w-64"
                  />
                </div>
              </div>
            </div>

            <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
              <div
                v-for="tool in filteredTools"
                :key="tool.name"
                class="card shadow-md transition-colors"
                :class="isToolEnabled(tool.name)
                  ? 'bg-base-100'
                  : 'bg-base-200/70 border border-base-300'"
              >
                <div class="card-body">
                  <!--
                    Header row: title + status badges on the left, the
                    per-tool toggle pinned to the top-right corner so it's
                    the first thing the eye lands on. The toggle itself
                    stays in the bright base-content layer (no opacity)
                    even when the tool is disabled — only the description /
                    annotations / View Schema button below dim so the user
                    can tell at a glance the tool is off but the control to
                    bring it back is still a "live affordance".
                    Using `toggle-primary` gives the on-state a saturated
                    color so the off-state can't be confused with a
                    visually-disabled widget.

                    Hidden for pending/changed tools because the right
                    next action there is Approve, not Disable. For
                    approved or never-quarantined tools the daemon
                    synthesizes the approval record on demand, so the
                    toggle works regardless of quarantine state.
                  -->
                  <div class="flex justify-between items-start gap-3">
                    <div
                      class="flex items-center gap-2 flex-wrap min-w-0 transition-opacity"
                      :class="isToolEnabled(tool.name) ? '' : 'opacity-60'"
                    >
                      <h4 class="card-title text-lg break-all">{{ tool.name }}</h4>
                      <span
                        v-if="getToolApprovalStatus(tool.name) === 'pending'"
                        class="badge badge-info badge-sm"
                      >new</span>
                      <span
                        v-else-if="getToolApprovalStatus(tool.name) === 'changed'"
                        class="badge badge-warning badge-sm"
                      >changed</span>
                      <span
                        v-if="isToolConfigDenied(tool.name)"
                        class="badge badge-error badge-sm"
                        title="Disabled by mcp_config.json (enabled_tools / disabled_tools)"
                      >locked by config</span>
                    </div>
                    <label
                      v-if="isToolToggleAvailable(tool.name)"
                      class="flex items-center gap-2 cursor-pointer shrink-0"
                    >
                      <span class="text-xs text-base-content/70">
                        <span v-if="isToolToggleLoading(tool.name)" class="loading loading-spinner loading-xs mr-1"></span>
                        {{ isToolEnabled(tool.name) ? 'Enabled' : 'Disabled' }}
                      </span>
                      <input
                        type="checkbox"
                        class="toggle toggle-sm toggle-primary"
                        :checked="isToolEnabled(tool.name)"
                        :disabled="isToolToggleLoading(tool.name) || bulkToolToggleLoading"
                        @change="toggleToolEnabled(tool.name, ($event.target as HTMLInputElement).checked)"
                      />
                    </label>
                    <span
                      v-else-if="isToolConfigDenied(tool.name)"
                      class="text-xs text-base-content/40 shrink-0 italic"
                      title="Remove from disabled_tools or add to enabled_tools in mcp_config.json to unlock"
                    >config locked</span>
                  </div>
                  <div
                    class="transition-opacity"
                    :class="isToolEnabled(tool.name) ? '' : 'opacity-60'"
                  >
                    <p class="text-sm text-base-content/70 mt-2">
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

        <!-- Configuration Tab
             Sections mirror the macOS tray (native/macos/MCPProxy/.../ServerDetailView.swift):
             General, Connection/Process, Environment Variables, Docker Isolation
             Overrides, Status, Health. All fields come from /api/v1/servers/{id} —
             no new API surface needed. Read-only here; the existing Edit page is
             the dedicated mutation surface. -->
        <div v-if="activeTab === 'config'">
          <div class="space-y-6">
            <!-- General -->
            <div class="card bg-base-100 shadow-sm">
              <div class="card-body py-4">
                <h3 class="card-title text-base">General</h3>
                <dl class="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 mt-2 text-sm">
                  <dt class="text-base-content/60">Name</dt>
                  <dd class="font-medium">{{ server.name }}</dd>
                  <dt class="text-base-content/60">Protocol</dt>
                  <dd><code class="bg-base-200 px-1.5 py-0.5 rounded text-xs">{{ server.protocol }}</code></dd>
                  <dt class="text-base-content/60">Enabled</dt>
                  <dd class="flex items-center gap-2">
                    <input
                      type="checkbox"
                      :checked="server.enabled"
                      @change="toggleEnabled"
                      class="toggle toggle-sm"
                      :disabled="actionLoading"
                    />
                    <span class="text-base-content/70">{{ server.enabled ? 'Yes' : 'No' }}</span>
                  </dd>
                  <dt class="text-base-content/60">Quarantined</dt>
                  <dd>
                    <span :class="server.quarantined ? 'badge badge-warning badge-sm' : 'badge badge-ghost badge-sm'">
                      {{ server.quarantined ? 'Yes' : 'No' }}
                    </span>
                  </dd>
                </dl>
              </div>
            </div>

            <!-- Connection (HTTP/SSE) -->
            <div v-if="server.url" class="card bg-base-100 shadow-sm">
              <div class="card-body py-4">
                <h3 class="card-title text-base">Connection</h3>
                <dl class="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 mt-2 text-sm">
                  <dt class="text-base-content/60">URL</dt>
                  <dd><code class="bg-base-200 px-1.5 py-0.5 rounded text-xs break-all">{{ server.url }}</code></dd>
                </dl>
              </div>
            </div>

            <!-- Process (stdio) -->
            <div v-if="server.command" class="card bg-base-100 shadow-sm">
              <div class="card-body py-4">
                <h3 class="card-title text-base">Process</h3>
                <dl class="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 mt-2 text-sm">
                  <dt class="text-base-content/60">Command</dt>
                  <dd><code class="bg-base-200 px-1.5 py-0.5 rounded text-xs">{{ server.command }}</code></dd>
                  <template v-if="server.args && server.args.length">
                    <dt class="text-base-content/60">Args</dt>
                    <dd><code class="bg-base-200 px-1.5 py-0.5 rounded text-xs break-all">{{ server.args.join(' ') }}</code></dd>
                  </template>
                  <template v-if="server.working_dir">
                    <dt class="text-base-content/60">Working Dir</dt>
                    <dd><code class="bg-base-200 px-1.5 py-0.5 rounded text-xs break-all">{{ server.working_dir }}</code></dd>
                  </template>
                </dl>
              </div>
            </div>

            <!-- Headers (HTTP servers): redacted by default per backend
                 redaction policy. Click the eye icon on a row to reveal the
                 raw value, which only works if the loaded config has
                 `reveal_secret_headers: true` — otherwise the API returns
                 `***REDACTED***` and there is nothing to reveal. -->
            <div v-if="(server.url || hasHeaders) && server.protocol !== 'stdio'" class="card bg-base-100 shadow-sm">
              <div class="card-body py-4">
                <div class="flex items-center justify-between">
                  <h3 class="card-title text-base">Headers</h3>
                  <button
                    v-if="!addingHeader"
                    class="btn btn-xs btn-ghost"
                    @click="startAddingHeader"
                    :disabled="kvPatchInFlight"
                  >+ Add header</button>
                </div>
                <p class="text-xs text-base-content/50 mt-1">
                  Sent with every request to this server. Storing the value as a secret keeps the literal token out of the config file.
                </p>
                <table v-if="hasHeaders || addingHeader" class="table table-sm mt-2">
                  <tbody>
                    <tr v-for="k in headerKeys" :key="`hdr-${k}`">
                      <td class="font-mono text-xs w-1/3 align-top">{{ k }}</td>
                      <td>
                        <KVValueCell
                          scope="header"
                          :k="k"
                          :raw-value="serverHeaders[k]"
                          :is-editing="editingKey === `hdr::${k}`"
                          :busy="kvPatchInFlight"
                          @start-edit="startEdit('hdr', k)"
                          @cancel-edit="cancelEdit"
                          @save="(val) => saveEdit('header', k, val)"
                          @delete="deleteKv('header', k)"
                          @convert="openConvertModal('header', k, serverHeaders[k])"
                        />
                      </td>
                    </tr>
                    <tr v-if="addingHeader">
                      <td>
                        <input
                          v-model="newHeaderKey"
                          class="input input-bordered input-xs w-full font-mono"
                          placeholder="Header-Name"
                          @keyup.enter="commitNewHeader"
                          ref="newHeaderKeyInput"
                        />
                      </td>
                      <td>
                        <div class="flex gap-2 items-center">
                          <input
                            v-model="newHeaderValue"
                            type="text"
                            class="input input-bordered input-xs flex-1 font-mono"
                            placeholder="value (literal or ${keyring:name})"
                            @keyup.enter="commitNewHeader"
                          />
                          <button class="btn btn-xs btn-primary" @click="commitNewHeader" :disabled="!newHeaderKey || !newHeaderValue || kvPatchInFlight">Add</button>
                          <button class="btn btn-xs btn-ghost" @click="addingHeader = false" :disabled="kvPatchInFlight">Cancel</button>
                        </div>
                      </td>
                    </tr>
                  </tbody>
                </table>
                <div v-else class="text-xs text-base-content/50 mt-2">No headers configured.</div>
              </div>
            </div>

            <!-- Environment Variables: same affordances as Headers — redact /
                 reveal / inline edit / delete / convert literal values into
                 ${keyring:name} references. Visible for any server that has
                 env vars or for stdio servers (which is where most env lives). -->
            <div v-if="hasEnv || server.protocol === 'stdio'" class="card bg-base-100 shadow-sm">
              <div class="card-body py-4">
                <div class="flex items-center justify-between">
                  <h3 class="card-title text-base">Environment Variables</h3>
                  <button
                    v-if="!addingEnv"
                    class="btn btn-xs btn-ghost"
                    @click="startAddingEnv"
                    :disabled="kvPatchInFlight"
                  >+ Add variable</button>
                </div>
                <table v-if="hasEnv || addingEnv" class="table table-sm mt-2">
                  <tbody>
                    <tr v-for="k in envKeys" :key="`env-${k}`">
                      <td class="font-mono text-xs w-1/3 align-top">{{ k }}</td>
                      <td>
                        <KVValueCell
                          scope="env"
                          :k="k"
                          :raw-value="serverEnv[k]"
                          :is-editing="editingKey === `env::${k}`"
                          :busy="kvPatchInFlight"
                          @start-edit="startEdit('env', k)"
                          @cancel-edit="cancelEdit"
                          @save="(val) => saveEdit('env', k, val)"
                          @delete="deleteKv('env', k)"
                          @convert="openConvertModal('env', k, serverEnv[k])"
                        />
                      </td>
                    </tr>
                    <tr v-if="addingEnv">
                      <td>
                        <input
                          v-model="newEnvKey"
                          class="input input-bordered input-xs w-full font-mono"
                          placeholder="VAR_NAME"
                          @keyup.enter="commitNewEnv"
                          ref="newEnvKeyInput"
                        />
                      </td>
                      <td>
                        <div class="flex gap-2 items-center">
                          <input
                            v-model="newEnvValue"
                            type="text"
                            class="input input-bordered input-xs flex-1 font-mono"
                            placeholder="value (literal or ${keyring:name})"
                            @keyup.enter="commitNewEnv"
                          />
                          <button class="btn btn-xs btn-primary" @click="commitNewEnv" :disabled="!newEnvKey || !newEnvValue || kvPatchInFlight">Add</button>
                          <button class="btn btn-xs btn-ghost" @click="addingEnv = false" :disabled="kvPatchInFlight">Cancel</button>
                        </div>
                      </td>
                    </tr>
                  </tbody>
                </table>
                <div v-else class="text-xs text-base-content/50 mt-2">No environment variables configured.</div>
              </div>
            </div>

            <!-- Docker Isolation Overrides: show the per-server override when
                 set, otherwise show the resolved default ('placeholder') so
                 the user can see what's actually in effect. Mirrors the
                 macOS tray's placeholder behavior. -->
            <div v-if="hasIsolationData" class="card bg-base-100 shadow-sm">
              <div class="card-body py-4">
                <h3 class="card-title text-base">Docker Isolation Overrides</h3>
                <dl class="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 mt-2 text-sm">
                  <dt class="text-base-content/60">Image</dt>
                  <dd>
                    <code v-if="server.isolation?.image" class="bg-base-200 px-1.5 py-0.5 rounded text-xs break-all">{{ server.isolation.image }}</code>
                    <span v-else-if="server.isolation_defaults?.image" class="text-base-content/40 text-xs italic">default: {{ server.isolation_defaults.image }}</span>
                    <span v-else class="text-base-content/40 text-xs">—</span>
                  </dd>
                  <dt class="text-base-content/60">Network Mode</dt>
                  <dd>
                    <span v-if="server.isolation?.network_mode" class="badge badge-outline badge-sm">{{ server.isolation.network_mode }}</span>
                    <span v-else-if="server.isolation_defaults?.network_mode" class="text-base-content/40 text-xs italic">default: {{ server.isolation_defaults.network_mode }}</span>
                    <span v-else class="text-base-content/40 text-xs">—</span>
                  </dd>
                  <dt class="text-base-content/60">Extra Args</dt>
                  <dd>
                    <code v-if="server.isolation?.extra_args && server.isolation.extra_args.length" class="bg-base-200 px-1.5 py-0.5 rounded text-xs break-all">{{ server.isolation.extra_args.join(' ') }}</code>
                    <span v-else-if="server.isolation_defaults?.extra_args && server.isolation_defaults.extra_args.length" class="text-base-content/40 text-xs italic">default: {{ server.isolation_defaults.extra_args.join(' ') }}</span>
                    <span v-else class="text-base-content/40 text-xs">—</span>
                  </dd>
                  <dt class="text-base-content/60">Container Working Dir</dt>
                  <dd>
                    <code v-if="server.isolation?.working_dir" class="bg-base-200 px-1.5 py-0.5 rounded text-xs">{{ server.isolation.working_dir }}</code>
                    <span v-else-if="server.isolation_defaults?.working_dir" class="text-base-content/40 text-xs italic">default: {{ server.isolation_defaults.working_dir }}</span>
                    <span v-else class="text-base-content/40 text-xs">—</span>
                  </dd>
                  <template v-if="server.isolation?.memory_limit">
                    <dt class="text-base-content/60">Memory Limit</dt>
                    <dd>{{ server.isolation.memory_limit }}</dd>
                  </template>
                  <template v-if="server.isolation?.cpu_limit">
                    <dt class="text-base-content/60">CPU Limit</dt>
                    <dd>{{ server.isolation.cpu_limit }}</dd>
                  </template>
                  <template v-if="server.isolation_defaults?.runtime_type">
                    <dt class="text-base-content/60">Runtime</dt>
                    <dd><span class="badge badge-ghost badge-sm">{{ server.isolation_defaults.runtime_type }}</span></dd>
                  </template>
                </dl>
              </div>
            </div>

            <!-- Status (live runtime state) -->
            <div class="card bg-base-100 shadow-sm">
              <div class="card-body py-4">
                <h3 class="card-title text-base">Status</h3>
                <dl class="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 mt-2 text-sm">
                  <dt class="text-base-content/60">Connected</dt>
                  <dd>
                    <span :class="server.connected ? 'badge badge-success badge-sm' : 'badge badge-ghost badge-sm'">
                      {{ server.connected ? 'Yes' : 'No' }}
                    </span>
                  </dd>
                  <template v-if="server.connected_at">
                    <dt class="text-base-content/60">Connected At</dt>
                    <dd>{{ formatConfigTime(server.connected_at) }}</dd>
                  </template>
                  <template v-if="(server.reconnect_count ?? 0) > 0">
                    <dt class="text-base-content/60">Reconnect Count</dt>
                    <dd>{{ server.reconnect_count }}</dd>
                  </template>
                  <dt class="text-base-content/60">Tool Count</dt>
                  <dd>{{ server.tool_count ?? 0 }}</dd>
                  <template v-if="server.tool_list_token_size">
                    <dt class="text-base-content/60">Tool List Tokens</dt>
                    <dd>{{ server.tool_list_token_size }}</dd>
                  </template>
                  <template v-if="server.last_error">
                    <dt class="text-base-content/60">Last Error</dt>
                    <dd class="text-error/80 break-words whitespace-pre-wrap">{{ server.last_error }}</dd>
                  </template>
                </dl>
              </div>
            </div>

            <!-- Health (calculated by backend; same shape consumed by macOS tray) -->
            <div v-if="server.health" class="card bg-base-100 shadow-sm">
              <div class="card-body py-4">
                <h3 class="card-title text-base">Health</h3>
                <dl class="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 mt-2 text-sm">
                  <dt class="text-base-content/60">Level</dt>
                  <dd>
                    <span :class="healthLevelBadgeClass(server.health.level)">{{ server.health.level }}</span>
                  </dd>
                  <dt class="text-base-content/60">Admin State</dt>
                  <dd><span class="badge badge-ghost badge-sm">{{ server.health.admin_state }}</span></dd>
                  <dt class="text-base-content/60">Summary</dt>
                  <dd>{{ server.health.summary }}</dd>
                  <template v-if="server.health.detail">
                    <dt class="text-base-content/60">Detail</dt>
                    <dd class="text-base-content/70 break-words whitespace-pre-wrap">{{ server.health.detail }}</dd>
                  </template>
                  <template v-if="server.health.action">
                    <dt class="text-base-content/60">Suggested Action</dt>
                    <dd><span class="badge badge-info badge-outline badge-sm">{{ server.health.action }}</span></dd>
                  </template>
                </dl>
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

    <!-- Convert-to-secret modal: prompts for a keyring secret name, then
         calls POST /api/v1/secrets followed by a PATCH replacing the
         literal value with `${keyring:NAME}`. -->
    <div v-if="convertModal.open" class="modal modal-open">
      <div class="modal-box max-w-md">
        <h3 class="font-bold text-lg">Convert to secret</h3>
        <p class="text-sm text-base-content/70 mt-2">
          Store the value of <code class="font-mono">{{ convertModal.key }}</code> in the OS keyring and
          replace it with a <code class="font-mono">{{ '${keyring:NAME}' }}</code> reference. The
          server config will then no longer contain the literal value.
        </p>
        <div class="form-control mt-4">
          <label class="label py-1"><span class="label-text">Secret name</span></label>
          <input
            v-model="convertModal.secretName"
            class="input input-bordered input-sm font-mono"
            placeholder="my-server-token"
            @keyup.enter="commitConvert"
          />
          <label class="label py-1">
            <span class="label-text-alt text-base-content/50">Will be referenced as <code>{{ '${keyring:' + (convertModal.secretName || 'NAME') + '}' }}</code></span>
          </label>
        </div>
        <div class="modal-action">
          <button class="btn btn-ghost btn-sm" @click="closeConvertModal" :disabled="convertModal.busy">Cancel</button>
          <button class="btn btn-primary btn-sm" @click="commitConvert" :disabled="!convertModal.secretName || convertModal.busy">
            <span v-if="convertModal.busy" class="loading loading-spinner loading-xs"></span>
            Convert
          </button>
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
import KVValueCell from '@/components/KVValueCell.vue'
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
// SYSTEMATIC FIX for the "stale local snapshot" class of bugs:
//
// `server` is a *computed* derived from the Pinia store, not a manually
// reassigned ref. That means every property access (`server.value.enabled`,
// `server.value.quarantine.blocked_count`, …) reads through the store's
// reactive proxy, and every SSE-driven store mutation — whether it lands
// via the spec-047 embedded payload, the spec-048 server-merge, or the
// notify-only fallback that re-fetches /api/v1/servers — automatically
// propagates to every template binding and computed in this view.
//
// The previous shape was a snapshot ref reassigned at ~10 action-handler
// sites. Any new handler that forgot the reassignment introduced a fresh
// staleness bug (latest examples: the "N disabled" pill not clearing on
// re-enable, and the big Tools counter freezing across a server-level
// Disable/Enable cycle). The computed makes the whole class structurally
// impossible.
//
// Mutations: anywhere we previously did `server.value.X = Y` to nudge the
// UI ahead of a network round-trip (the optimistic blocked_count bump),
// we now mutate the store's server object directly via `mutateStoreServer`.
// Pinia is reactive to direct mutation, so the computed observers see
// the change immediately and the subsequent `fetchServers` reconciles
// to authoritative state.
const server = computed<Server | null>(() => {
  return serversStore.servers.find(s => s.name === props.serverName) || null
})

// mutateStoreServer applies fn to the live store object for this view's
// server. Lets optimistic updates land where the rest of the app will
// see them, without forking a parallel local copy.
function mutateStoreServer(fn: (s: Server) => void) {
  const s = serversStore.servers.find(srv => srv.name === props.serverName)
  if (s) fn(s)
}
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
const toolToggleLoading = ref<Record<string, boolean>>({})
// Single in-flight flag for the bulk Enable All / Disable All buttons so
// they're mutually exclusive with each other and with any per-tool toggle.
const bulkToolToggleLoading = ref(false)

const quarantinedTools = computed(() => {
  return toolApprovals.value.filter(t => t.status === 'pending' || t.status === 'changed')
})

const blockedToolCount = computed(() => {
  const q = server.value?.quarantine
  if (!q) return 0
  return q.blocked_count ?? 0
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

function getToolApproval(toolName: string): ToolApproval | null {
  return toolApprovals.value.find(t => t.tool_name === toolName) || null
}

function isToolConfigDenied(toolName: string): boolean {
  const tool = serverTools.value.find(t => t.name === toolName) as Tool & { config_denied?: boolean } | undefined
  return tool?.config_denied === true
}

function isToolEnabled(toolName: string): boolean {
  // GET /api/v1/servers/{id}/tools returns each tool with a top-level
  // `disabled` boolean (see contracts.Tool.Disabled in Go) when an approval
  // record exists. The approvals endpoint also exposes `enabled`/`disabled`.
  // Cross-check both so the toggle reflects reality regardless of which
  // payload the frontend already loaded.
  const tool = serverTools.value.find(t => t.name === toolName) as Tool & { disabled?: boolean; enabled?: boolean } | undefined
  if (tool) {
    if (typeof tool.disabled === 'boolean') return !tool.disabled
    if (typeof tool.enabled === 'boolean') return tool.enabled
  }
  const approval = getToolApproval(toolName)
  if (!approval) return true
  if (typeof approval.enabled === 'boolean') return approval.enabled
  if (typeof approval.disabled === 'boolean') return !approval.disabled
  return true
}

function isToolToggleLoading(toolName: string): boolean {
  return !!toolToggleLoading.value[toolName]
}

// The toggle is hidden for pending/changed tools because the right next
// action there is "Approve", not "Disable". For approved or never-quarantined
// tools the daemon synthesizes an approval record on demand, so the toggle
// works in every other case.
function isToolToggleAvailable(toolName: string): boolean {
  if (isToolConfigDenied(toolName)) return false
  const status = getToolApprovalStatus(toolName)
  return status === null || status === 'approved'
}

// Whether the bulk buttons should appear. We only render "Enable All" when
// at least one tool can actually be enabled, and "Disable All" only when at
// least one tool can be disabled — otherwise the label promises a no-op.
const hasEnabledTool = computed(() =>
  serverTools.value.some(t => isToolToggleAvailable(t.name) && isToolEnabled(t.name))
)
const hasDisabledTool = computed(() =>
  serverTools.value.some(t => isToolToggleAvailable(t.name) && !isToolEnabled(t.name))
)

// Vue Router 4 reuses the ServerDetail.vue component instance across
// /servers/foo → /servers/bar (same route, just a different param). The
// `server` computed correctly retargets via the store, but the local
// data refs (serverTools, toolApprovals, serverLogs, scan*) stay populated
// with the previous server's data until something refetches them. Without
// this watch, navigating between server detail pages briefly shows server
// B's name + stats with server A's tool list — looks like a data-corruption
// bug. Reset eagerly, then kick off a fresh load.
//
// Race protection: loadGeneration is bumped by loadServerDetails so an
// in-flight load for the previous server can't overwrite the new server's
// refs when its fetch finally resolves. See loadTools / loadToolApprovals
// / loadLogs for the gen-check pattern.
watch(
  () => props.serverName,
  (next, prev) => {
    if (next === prev) return
    serverTools.value = []
    toolsError.value = null
    selectedToolSchema.value = null
    toolApprovals.value = []
    toolToggleLoading.value = {}
    serverLogs.value = []
    logsError.value = null
    scanReport.value = null
    scanStatus.value = null
    scanError.value = null
    scanReportLoading.value = false
    activeScanJobId.value = null
    scanFiles.value = []
    scanFilesLoaded.value = false
    void loadServerDetails()
  }
)

// Reload tools (and approvals) whenever the server's runtime state
// changes between enabled/disconnected/connected. Without this, toggling
// a server enabled/disabled would leave the local serverTools list
// stuck at its previous value: enabling shows "Connected" but tool
// count stays 0 until manual refresh; disabling shows "Disconnected"
// but the prior count lingers. Both directions snap to the right state
// here by re-fetching once the store-backed server status flips. The
// store itself receives status updates via the SSE handler in
// frontend/src/stores/servers.ts, so this watch piggybacks on that
// path instead of polling.
watch(
  () => [server.value?.connected, server.value?.enabled] as const,
  ([connected, enabled], prev) => {
    if (!server.value) return
    const [prevConnected, prevEnabled] = prev ?? [undefined, undefined]
    if (connected === prevConnected && enabled === prevEnabled) return
    if (!enabled) {
      // A disabled server reports tool_count=0 immediately; reflect
      // that locally so the big "Tools" counter doesn't lag.
      serverTools.value = []
      toolApprovals.value = []
      return
    }
    if (connected) {
      void loadTools()
      void loadToolApprovals()
    }
  }
)

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
// loadGeneration is bumped by every loadServerDetails entry. The three
// per-server fetches (loadTools / loadToolApprovals / loadLogs) capture
// the generation at the start of their call and only commit their result
// if the generation hasn't advanced — which protects against the foo→bar
// navigation race where foo's response arrives AFTER bar's load already
// started. The counter is intentionally not a Vue ref: it's purely
// internal flow control, no UI reactivity needed.
let loadGeneration = 0

async function loadServerDetails() {
  const myGen = ++loadGeneration
  loading.value = true
  error.value = null

  try {
    await serversStore.fetchServers()
    if (myGen !== loadGeneration) return
    // server is a computed from the store — no manual reassignment needed.

    if (!server.value) {
      error.value = `Server "${props.serverName}" not found`
      return
    }

    // Load tools, approvals, and logs in parallel
    await Promise.all([
      _loadToolsWithGen(myGen),
      _loadToolApprovalsWithGen(myGen),
      _loadLogsWithGen(myGen)
    ])
  } catch (err) {
    if (myGen === loadGeneration) {
      error.value = err instanceof Error ? err.message : 'Failed to load server details'
    }
  } finally {
    if (myGen === loadGeneration) loading.value = false
  }
}

// loadTools is the public no-arg wrapper used by template @click handlers
// and ad-hoc reloads (e.g. the connected/enabled watch). Internal gen-gated
// impl lives in _loadToolsWithGen so loadServerDetails can pass an explicit
// generation token for navigation-race protection.
function loadTools() {
  return _loadToolsWithGen(loadGeneration)
}

async function _loadToolsWithGen(gen: number) {
  if (!server.value) return

  toolsLoading.value = true
  toolsError.value = null

  try {
    const response = await api.getServerTools(server.value.name)
    if (gen !== loadGeneration) return
    if (response.success && response.data) {
      serverTools.value = response.data.tools || []
    } else {
      toolsError.value = response.error || 'Failed to load tools'
    }
  } catch (err) {
    if (gen !== loadGeneration) return
    toolsError.value = err instanceof Error ? err.message : 'Failed to load tools'
  } finally {
    if (gen === loadGeneration) toolsLoading.value = false
  }
}

// Tool quarantine functions (Spec 032)
function loadToolApprovals() {
  return _loadToolApprovalsWithGen(loadGeneration)
}

async function _loadToolApprovalsWithGen(gen: number) {
  if (!server.value) return
  try {
    const response = await api.getToolApprovals(server.value.name)
    if (gen !== loadGeneration) return
    if (response.success && response.data) {
      const approvals = (response.data.tools || []).map((tool) => {
        const disabled = typeof tool.disabled === 'boolean'
          ? tool.disabled
          : (typeof tool.enabled === 'boolean' ? !tool.enabled : false)
        return {
          ...tool,
          disabled,
          enabled: !disabled,
        }
      })

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
        if (gen !== loadGeneration) return
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
      // server is a computed from the store — no manual reassignment needed.
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
      // server is a computed from the store — no manual reassignment needed.
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

async function toggleToolEnabled(toolName: string, enabled: boolean) {
  if (!server.value) return
  toolToggleLoading.value = { ...toolToggleLoading.value, [toolName]: true }

  // optimistic local UI update
  const idx = toolApprovals.value.findIndex(t => t.tool_name === toolName)
  const prev = idx >= 0 ? { ...toolApprovals.value[idx] } : null
  if (idx >= 0) {
    toolApprovals.value[idx] = {
      ...toolApprovals.value[idx],
      enabled,
      disabled: !enabled,
    }
  }
  // Optimistically bump the local quarantine.blocked_count so the
  // "N disabled" stat-desc pill responds the instant the user flips the
  // toggle, even before the round-trip + SSE + merge sequence completes.
  // The subsequent syncAfterToolToggle() snaps it to server truth.
  const prevQuarantine: Server['quarantine'] | undefined = server.value.quarantine
    ? { ...server.value.quarantine }
    : undefined
  bumpStoreBlockedCount(enabled ? -1 : 1)

  try {
    const response = await api.setToolEnabled(server.value.name, toolName, enabled)
    if (response.success) {
      systemStore.addToast({
        type: 'success',
        title: enabled ? 'Tool Enabled' : 'Tool Disabled',
        message: `${toolName} has been ${enabled ? 'enabled' : 'disabled'}`
      })
      // Re-fetch so blockedToolCount + serverTools.disabled reflect the new
      // state. The runtime emits servers.changed via SSE, but local server.value
      // is a snapshot of the store and doesn't auto-update — without this an
      // Enable toggle leaves the stat-desc pill stuck on "N disabled".
      await syncAfterToolToggle()
    } else {
      if (idx >= 0 && prev) toolApprovals.value[idx] = prev
      restoreStoreQuarantine(prevQuarantine)
      systemStore.addToast({
        type: 'error',
        title: 'Update Failed',
        message: response.error || 'Failed to update tool state',
      })
    }
  } catch (err) {
    if (idx >= 0 && prev) toolApprovals.value[idx] = prev
    restoreStoreQuarantine(prevQuarantine)
    systemStore.addToast({
      type: 'error',
      title: 'Update Failed',
      message: err instanceof Error ? err.message : 'Failed to update tool state',
    })
  } finally {
    const next = { ...toolToggleLoading.value }
    delete next[toolName]
    toolToggleLoading.value = next
  }
}

// bumpStoreBlockedCount adjusts the store server's quarantine.blocked_count
// so the "N disabled" stat-desc pill reflects the user's toggle action
// immediately. Mutates the store directly (Pinia is reactive to direct
// mutation) so every consumer — Server Detail, Server List, tray — sees
// the update without a round-trip. The subsequent syncAfterToolToggle()
// snaps everything back to authoritative server state.
function bumpStoreBlockedCount(delta: number) {
  mutateStoreServer(s => {
    const current = s.quarantine
    const nextBlocked = Math.max(0, (current?.blocked_count ?? 0) + delta)
    const pending = current?.pending_count ?? 0
    const changed = current?.changed_count ?? 0
    if (nextBlocked === 0 && pending === 0 && changed === 0) {
      // Match the backend's "omit when all-zero" rule so mergeServers
      // doesn't leave a stale empty Quarantine block around.
      delete (s as Server & { quarantine?: unknown }).quarantine
    } else {
      s.quarantine = {
        pending_count: pending,
        changed_count: changed,
        blocked_count: nextBlocked,
      }
    }
  })
}

function restoreStoreQuarantine(prev: Server['quarantine']) {
  mutateStoreServer(s => {
    if (prev) {
      s.quarantine = prev
    } else {
      delete (s as Server & { quarantine?: unknown }).quarantine
    }
  })
}

// syncAfterToolToggle keeps the page state consistent after any tool enable/
// disable: it refreshes the store-backed servers list (so blockedToolCount on
// the Server List view loses staleness on navigation) and the per-tool /
// approval caches (so toggle widgets pick up server-truth instead of
// optimistic state). The `server` computed automatically reflects the store
// update — no manual ref reassignment.
async function syncAfterToolToggle() {
  if (!server.value) return
  await Promise.all([
    serversStore.fetchServers(true),
    loadTools(),
    loadToolApprovals(),
  ])
}

// bulkToggleAllTools dispatches one Enable-all / Disable-all request and
// refreshes the local tool/approval state so the UI reflects the new
// disabled-flags immediately (instead of waiting for the SSE event).
async function bulkToggleAllTools(enabled: boolean) {
  if (!server.value || bulkToolToggleLoading.value) return
  bulkToolToggleLoading.value = true

  // Optimistic store mutation — same idea as the single-tool path, but
  // we drive blocked_count straight to 0 (Enable All) or the count of
  // togglable tools (Disable All) so the "N disabled" pill snaps to the
  // expected value instantly. The subsequent syncAfterToolToggle()
  // reconciles to whatever the backend actually changed.
  const prevQuarantine: Server['quarantine'] | undefined = server.value.quarantine
    ? { ...server.value.quarantine }
    : undefined
  const togglable = serverTools.value.filter(t => isToolToggleAvailable(t.name)).length
  mutateStoreServer(s => {
    const current = s.quarantine
    const pending = current?.pending_count ?? 0
    const changed = current?.changed_count ?? 0
    const nextBlocked = enabled ? 0 : togglable
    if (nextBlocked === 0 && pending === 0 && changed === 0) {
      delete (s as Server & { quarantine?: unknown }).quarantine
    } else {
      s.quarantine = {
        pending_count: pending,
        changed_count: changed,
        blocked_count: nextBlocked,
      }
    }
  })

  try {
    const response = await api.setAllToolsEnabled(server.value.name, enabled)
    if (response.success && response.data) {
      const changed = response.data.changed ?? 0
      systemStore.addToast({
        type: 'success',
        title: enabled ? 'Tools Enabled' : 'Tools Disabled',
        message: changed === 0
          ? 'No tools needed changes.'
          : `${changed} tool${changed === 1 ? '' : 's'} ${enabled ? 'enabled' : 'disabled'}.`,
      })
      // Refresh server data + tool caches so the per-tool toggle, the
      // "N disabled" pill, and the Server List both lose any staleness.
      await syncAfterToolToggle()
    } else {
      restoreStoreQuarantine(prevQuarantine)
      systemStore.addToast({
        type: 'error',
        title: 'Bulk Update Failed',
        message: response.error || 'Failed to update tools',
      })
    }
  } catch (err) {
    restoreStoreQuarantine(prevQuarantine)
    systemStore.addToast({
      type: 'error',
      title: 'Bulk Update Failed',
      message: err instanceof Error ? err.message : 'Failed to update tools',
    })
  } finally {
    bulkToolToggleLoading.value = false
  }
}

function loadLogs() {
  return _loadLogsWithGen(loadGeneration)
}

async function _loadLogsWithGen(gen: number) {
  if (!server.value) return

  logsLoading.value = true
  logsError.value = null

  try {
    const response = await api.getServerLogs(server.value.name, logTail.value)
    if (gen !== loadGeneration) return
    if (response.success && response.data) {
      serverLogs.value = response.data.logs || []
    } else {
      logsError.value = response.error || 'Failed to load logs'
    }
  } catch (err) {
    if (gen !== loadGeneration) return
    logsError.value = err instanceof Error ? err.message : 'Failed to load logs'
  } finally {
    if (gen === loadGeneration) logsLoading.value = false
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
    // server is a computed from the store — no manual reassignment needed.
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
      // server is a computed from the store — no manual reassignment needed.
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
    // server is a computed from the store — no manual reassignment needed.
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
    // server is a computed from the store — no manual reassignment needed.
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
    // server is a computed from the store — no manual reassignment needed.
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

// --- Config tab helpers ---
// Mask env var values in the Config tab so a casual viewer can see WHICH
// variables are set without exposing the secret values. Matches the
// "ALL_CAPS_KEY shown, value hidden" pattern from the macOS tray.
function maskEnvValue(value: string): string {
  if (!value) return '(empty)'
  if (value.length <= 4) return '••••'
  return '••••' + value.slice(-2) + ` (${value.length} chars)`
}

// --- Headers / Env display + edit state ---
// Convention: composite key strings prefixed with the scope ("hdr::Name" or
// "env::Name") so a single Set can drive reveal/edit state across both
// cards without collision.

const serverHeaders = computed<Record<string, string>>(() => (server.value?.headers ?? {}) as Record<string, string>)
const serverEnv = computed<Record<string, string>>(() => (server.value?.env ?? {}) as Record<string, string>)
const headerKeys = computed(() => Object.keys(serverHeaders.value).sort())
const envKeys = computed(() => Object.keys(serverEnv.value).sort())
const hasHeaders = computed(() => headerKeys.value.length > 0)
const hasEnv = computed(() => envKeys.value.length > 0)

const editingKey = ref<string | null>(null)
const kvPatchInFlight = ref(false)

const addingHeader = ref(false)
const newHeaderKey = ref('')
const newHeaderValue = ref('')
const newHeaderKeyInput = ref<HTMLInputElement | null>(null)

const addingEnv = ref(false)
const newEnvKey = ref('')
const newEnvValue = ref('')
const newEnvKeyInput = ref<HTMLInputElement | null>(null)

const convertModal = ref<{ open: boolean; scope: 'header' | 'env'; key: string; rawValue: string; secretName: string; busy: boolean }>({
  open: false,
  scope: 'header',
  key: '',
  rawValue: '',
  secretName: '',
  busy: false,
})

function startEdit(scope: 'hdr' | 'env', k: string) {
  editingKey.value = `${scope}::${k}`
}
function cancelEdit() {
  editingKey.value = null
}

async function startAddingHeader() {
  addingHeader.value = true
  newHeaderKey.value = ''
  newHeaderValue.value = ''
  await new Promise((r) => setTimeout(r, 0))
  newHeaderKeyInput.value?.focus()
}
async function startAddingEnv() {
  addingEnv.value = true
  newEnvKey.value = ''
  newEnvValue.value = ''
  await new Promise((r) => setTimeout(r, 0))
  newEnvKeyInput.value?.focus()
}

// patchServer with deep-merge semantics: the backend treats keys present
// in `headers` / `env` as upserts, keys absent as preserved, and keys
// listed in `headers_remove` / `env_remove` as deletes. This lets us send
// the minimal diff for each user action — and crucially never round-trips
// `***REDACTED***` values: any header whose redacted form was unchanged
// simply stays out of the patch, so the backend keeps the real string.
async function patchServerDiff(patch: Record<string, unknown>, action: string): Promise<boolean> {
  if (!server.value) return false
  kvPatchInFlight.value = true
  try {
    const resp = await api.patchServer(server.value.name, patch)
    if (!resp.success) {
      systemStore.addToast({ type: 'error', title: `${action} failed`, message: resp.error || 'Unknown error' })
      return false
    }
    await serversStore.fetchServers(true)
    systemStore.addToast({ type: 'success', title: action, message: '' })
    return true
  } catch (e: any) {
    systemStore.addToast({ type: 'error', title: `${action} failed`, message: e?.message || String(e) })
    return false
  } finally {
    kvPatchInFlight.value = false
  }
}

function scopeKey(scope: 'header' | 'env'): 'headers' | 'env' {
  return scope === 'header' ? 'headers' : 'env'
}

async function saveEdit(scope: 'header' | 'env', k: string, val: string) {
  const ok = await patchServerDiff({ [scopeKey(scope)]: { [k]: val } }, `Updated ${k}`)
  if (ok) editingKey.value = null
}

// Deletion uses JSON Merge Patch (RFC 7396): a null value on the key
// signals "delete this key" to the backend. JSON.stringify emits `null`
// as the literal token, so the patch body becomes `{"headers": {"X-Old":
// null}}` on the wire — symmetric with the MCP `upstream_servers patch`
// tool's `{"X-Old": null}` convention. Note the explicit `null` literal:
// passing `undefined` would be stripped by JSON.stringify (no key in the
// output) and the backend would interpret that as "preserve".
async function deleteKv(scope: 'header' | 'env', k: string) {
  if (!confirm(`Delete ${scope === 'header' ? 'header' : 'env variable'} "${k}"?`)) return
  await patchServerDiff({ [scopeKey(scope)]: { [k]: null } }, `Deleted ${k}`)
}

async function commitNewHeader() {
  if (!newHeaderKey.value || !newHeaderValue.value) return
  const ok = await patchServerDiff(
    { headers: { [newHeaderKey.value]: newHeaderValue.value } },
    `Added ${newHeaderKey.value}`
  )
  if (ok) addingHeader.value = false
}

async function commitNewEnv() {
  if (!newEnvKey.value || !newEnvValue.value) return
  const ok = await patchServerDiff(
    { env: { [newEnvKey.value]: newEnvValue.value } },
    `Added ${newEnvKey.value}`
  )
  if (ok) addingEnv.value = false
}

// Suggest a keyring secret name derived from the kv key. Keep it short,
// lowercase, alphanumeric + hyphens — the same convention as the existing
// Secrets view.
function suggestSecretName(scope: 'header' | 'env', k: string): string {
  const base = `${server.value?.name || 'server'}-${k}`
  return base
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-|-$/g, '')
    .slice(0, 64)
}

function openConvertModal(scope: 'header' | 'env', k: string, rawValue: string) {
  convertModal.value = {
    open: true,
    scope,
    key: k,
    rawValue,
    secretName: suggestSecretName(scope, k),
    busy: false,
  }
}
function closeConvertModal() {
  convertModal.value.open = false
  convertModal.value.busy = false
}

async function commitConvert() {
  const m = convertModal.value
  if (!m.secretName || !server.value) return
  m.busy = true
  try {
    // Atomic server-side conversion: the backend reads the real value
    // from the loaded config (so we don't need the plaintext on the
    // client — important when the API redacts sensitive headers on the
    // read path), stores it in keyring, and rewrites the config field
    // with the ${keyring:NAME} reference. Single round-trip, single
    // failure surface.
    const resp = await api.convertConfigToSecret(server.value.name, m.scope, m.key, m.secretName)
    if (!resp.success) {
      systemStore.addToast({ type: 'error', title: 'Convert failed', message: resp.error || 'Unknown error' })
      return
    }
    await serversStore.fetchServers(true)
    systemStore.addToast({ type: 'success', title: `Converted ${m.key} to secret`, message: '' })
    closeConvertModal()
  } catch (e: any) {
    systemStore.addToast({ type: 'error', title: 'Convert failed', message: e?.message || String(e) })
  } finally {
    convertModal.value.busy = false
  }
}

// hasIsolationData is true when there's anything to show in the Docker
// Isolation Overrides section — either a per-server override or a resolved
// default the user might want to inspect. Stdio servers without docker
// isolation enabled have neither and the section is hidden entirely.
const hasIsolationData = computed(() => {
  if (!server.value) return false
  const iso = server.value.isolation
  const def = server.value.isolation_defaults
  if (iso && (iso.image || iso.network_mode || (iso.extra_args && iso.extra_args.length) || iso.working_dir || iso.memory_limit || iso.cpu_limit)) {
    return true
  }
  if (def && (def.image || def.network_mode || (def.extra_args && def.extra_args.length) || def.working_dir || def.runtime_type)) {
    return true
  }
  return false
})

// Locale-aware absolute timestamp for "Connected At" / similar fields.
// We use the absolute form (not relative-time) because it matches what
// users see in the macOS tray and in `mcpproxy upstream list` — a single
// authoritative source-of-truth representation.
function formatConfigTime(isoString: string | null | undefined): string {
  if (!isoString) return ''
  const date = new Date(isoString)
  if (isNaN(date.getTime())) return isoString
  return date.toLocaleString()
}

// healthLevelBadgeClass returns the daisyUI class set for a Health.Level
// badge, mirroring the existing color choices used elsewhere in the app
// (see e.g. server-list dot color logic).
function healthLevelBadgeClass(level: string): string {
  switch (level) {
    case 'healthy':
      return 'badge badge-success badge-sm'
    case 'degraded':
      return 'badge badge-warning badge-sm'
    case 'unhealthy':
      return 'badge badge-error badge-sm'
    default:
      return 'badge badge-ghost badge-sm'
  }
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
            // server is a computed from the store — no manual reassignment needed.
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
          // server is a computed from the store — no manual reassignment needed.
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
