<template>
  <div class="space-y-6" data-test="tools-page">
    <!-- Page Header -->
    <div class="flex flex-wrap justify-between items-start gap-4">
      <div>
        <h1 class="text-3xl font-bold">Tools</h1>
        <p class="text-base-content/70 mt-1">Monitor and edit all individual tools</p>
      </div>
      <div class="flex items-center gap-3">
        <div v-if="stats" class="badge badge-outline badge-lg">
          {{ stats.total }} tools
        </div>
        <button @click="loadTools" class="btn btn-sm btn-ghost" :disabled="loading" data-test="tools-refresh">
          <svg class="w-4 h-4" :class="{ 'animate-spin': loading }" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
        </button>
      </div>
    </div>

    <!-- Summary Stat Cards -->
    <div v-if="stats" class="stats shadow bg-base-100 w-full">
      <button
        type="button"
        :class="['stat text-left transition-colors cursor-pointer hover:bg-base-200/60', activeStatCard === 'total' ? 'bg-base-200 ring-2 ring-inset ring-primary/40' : '']"
        data-test="stat-total"
        @click="selectStatCard('total')"
      >
        <div class="stat-title">Total</div>
        <div class="stat-value text-2xl">{{ stats.total }}</div>
      </button>
      <button
        type="button"
        :class="['stat text-left transition-colors cursor-pointer hover:bg-base-200/60', activeStatCard === 'enabled' ? 'bg-base-200 ring-2 ring-inset ring-primary/40' : '']"
        data-test="stat-enabled"
        @click="selectStatCard('enabled')"
      >
        <div class="stat-title">Enabled</div>
        <div class="stat-value text-2xl text-success">{{ stats.enabled }}</div>
      </button>
      <button
        type="button"
        :class="['stat text-left transition-colors cursor-pointer hover:bg-base-200/60', activeStatCard === 'disabled' ? 'bg-base-200 ring-2 ring-inset ring-primary/40' : '']"
        data-test="stat-disabled"
        @click="selectStatCard('disabled')"
      >
        <div class="stat-title">Disabled</div>
        <div class="stat-value text-2xl text-warning">{{ stats.disabled }}</div>
      </button>
      <!-- Spec 109 FR-027: this card is a link to the review queue, not an
           in-page filter toggle — "needs review" means something to act on
           elsewhere, not another way to slice this table. -->
      <router-link
        to="/review"
        class="stat text-left transition-colors hover:bg-base-200/60"
        data-test="stat-pending"
      >
        <div class="stat-title">Needs review</div>
        <div class="stat-value text-2xl" :class="stats.pending_approval > 0 ? 'text-error' : ''">
          {{ stats.pending_approval }}
        </div>
      </router-link>
    </div>

    <!-- Partial-error banner -->
    <div v-if="partial && failedServers.length > 0" class="alert alert-warning">
      <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <div>
        <div class="font-medium">Partial results — some servers could not be read</div>
        <div class="text-sm">Failed servers: {{ failedServers.join(', ') }}</div>
      </div>
    </div>

    <!-- Filters -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body py-4">
        <div class="flex flex-wrap gap-4 items-end">
          <!-- Spec 108-j J6: view the catalogue as a client or a profile. It
               only looks: it never changes what that subject can access. Both
               choices write the same URL parameters the header Viewing chip
               writes (useScopeQuery), and choosing one clears the other
               because GET /tools takes one subject at a time. -->
          <div v-if="viewAsAvailable" class="form-control min-w-[160px]">
            <label class="label py-1" for="tools-view-as">
              <span class="label-text text-xs">View as</span>
            </label>
            <select
              id="tools-view-as"
              class="select select-bordered select-sm"
              :value="viewAsSelectValue"
              data-test="tools-view-as-select"
              @change="pickViewAs(($event.target as HTMLSelectElement).value)"
            >
              <option value="">Nobody (all tools)</option>
              <optgroup v-if="!isTenant && clientOptions.length > 0" label="Clients">
                <option v-for="client in clientOptions" :key="client.id" :value="`client:${client.id}`">{{ client.display_name }}</option>
              </optgroup>
              <optgroup v-if="profilesStore.profiles.length > 0" label="Profiles">
                <option v-for="profile in profilesStore.profiles" :key="profile.name" :value="`profile:${profile.name}`">{{ profile.title || profile.name }}</option>
              </optgroup>
              <option v-if="viewAsSelectOrphan" :value="viewAsSelectValue">{{ viewAsSelectOrphan }}</option>
            </select>
          </div>

          <!-- Search -->
          <div class="form-control flex-1 min-w-[200px]">
            <label class="label py-1">
              <span class="label-text text-xs">Search</span>
            </label>
            <div class="relative">
              <input
                v-model="searchQuery"
                type="text"
                placeholder="Search by name, description, or server..."
                class="input input-bordered input-sm w-full pl-8"
                data-test="tools-search"
              />
              <svg class="absolute left-2.5 top-2 w-4 h-4 text-base-content/40" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
              </svg>
            </div>
          </div>

          <!-- Server filter -->
          <div class="form-control min-w-[140px]">
            <label class="label py-1">
              <span class="label-text text-xs">Server</span>
            </label>
            <select v-model="filterServer" class="select select-bordered select-sm" aria-label="Filter by server" data-test="filter-server">
              <option value="">All Servers</option>
              <option v-for="srv in availableServers" :key="srv" :value="srv">{{ srv }}</option>
            </select>
          </div>

          <!-- Status filter -->
          <div class="form-control min-w-[130px]">
            <label class="label py-1">
              <span class="label-text text-xs">Status</span>
            </label>
            <select v-model="filterStatus" class="select select-bordered select-sm" aria-label="Filter by status" data-test="filter-status">
              <option value="">All</option>
              <option value="enabled">Enabled</option>
              <option value="disabled">Disabled</option>
              <option value="config_denied">Config Denied</option>
            </select>
          </div>

          <!-- Tier filter. Spec 109 FR-028/X11: "Tier" (not "Risk" — risk
               stays the scan-score term), values from the server-computed
               `tier` field, never derived locally. `?risk=` stays a query
               alias for `?tier=` for old bookmarks/links. -->
          <div class="form-control min-w-[120px]">
            <label class="label py-1">
              <span class="label-text text-xs">Tier</span>
            </label>
            <select v-model="filterTier" class="select select-bordered select-sm" aria-label="Filter by tier" data-test="filter-tier">
              <option value="">All</option>
              <option value="read">Read</option>
              <option value="write">Write</option>
              <option value="destructive">Destructive</option>
              <option value="unannotated">Unannotated</option>
            </select>
          </div>

          <!-- Approval filter -->
          <div class="form-control min-w-[120px]">
            <label class="label py-1">
              <span class="label-text text-xs">Approval</span>
            </label>
            <!-- Spec 109 FR-027: one review-state vocabulary everywhere
                 (Web/macOS/CLI/review payload) — no "awaiting" (it duplicated
                 pending+changed with a different name). -->
            <select v-model="filterApproval" class="select select-bordered select-sm" aria-label="Filter by approval state" data-test="filter-approval">
              <option value="">All</option>
              <option value="approved">Approved</option>
              <option value="pending">New, needs review</option>
              <option value="changed">Changed, needs review</option>
            </select>
          </div>

          <!-- Clear Filters -->
          <button v-if="hasActiveFilters" @click="clearFilters" class="btn btn-sm btn-ghost">
            Clear Filters
          </button>
        </div>

        <!-- Spec 108-j J4: the profile/client/token chips (removable), with the
             disabled variants for a value Tools cannot apply. -->
        <div v-if="scopeQuery" class="mt-2" data-test="tools-scope-chips">
          <ScopeChips
            page="tools"
            :scope-query="scopeQuery"
            :disabled="disabledScope"
            :conflicting="scopeConflict ? ['client', 'profile'] : []"
            :unavailable="scopeUnavailableNames"
          />
        </div>

        <!-- Active filter chips -->
        <div v-if="hasActiveFilters" class="flex flex-wrap gap-2 mt-2 pt-2 border-t border-base-300">
          <span class="text-xs text-base-content/60">Active filters:</span>
          <span v-if="searchQuery" class="badge badge-sm badge-outline">Search: {{ searchQuery }}</span>
          <span v-if="filterServer" class="badge badge-sm badge-outline">Server: {{ filterServer }}</span>
          <span v-if="filterStatus" class="badge badge-sm badge-outline">Status: {{ filterStatus }}</span>
          <span v-if="filterTier" class="badge badge-sm badge-outline">Tier: {{ filterTier }}</span>
          <span v-if="filterApproval" class="badge badge-sm badge-outline">Approval: {{ toolApprovalLabel(filterApproval) }}</span>
        </div>
      </div>
    </div>

    <!-- Spec 108-j J6/J14 (FR-032): what the viewed subject can do. A status
         region, in words: a view must never look like it edits the subject's
         access. -->
    <div v-if="viewAsActive && !scopeError" role="status" class="alert flex-wrap items-start shadow-md" data-test="tools-view-as-banner">
      <div class="flex-1 min-w-0 space-y-1">
        <div class="font-medium" data-test="tools-view-as-summary">{{ viewAsBannerText }}</div>
        <div v-if="disabledServerNote" class="text-sm" data-test="tools-view-as-disabled-note">
          {{ disabledServerNote }}
          <router-link v-if="disabledServersLink" :to="disabledServersLink" class="link link-primary">See them</router-link>
        </div>
        <div class="text-xs opacity-80">This is a view of {{ viewAsSubjectLabel }}'s access. Nothing here changes it.</div>
      </div>
      <div v-if="viewAsAdmin" class="join" role="group" aria-label="Show tools" data-test="tools-show-filter">
        <button
          v-for="option in SHOW_OPTIONS"
          :key="option.value"
          type="button"
          class="btn btn-xs join-item"
          :class="showFilter === option.value ? 'btn-primary' : 'btn-ghost'"
          :aria-pressed="showFilter === option.value"
          :data-test="`tools-show-${option.value}`"
          @click="showFilter = option.value"
        >{{ option.label }}</button>
      </div>
    </div>

    <!-- Batch action bar -->
    <div v-if="selectedKeys.size > 0" class="alert shadow-md" data-test="tools-batch-bar">
      <div class="flex items-center gap-3 w-full flex-wrap">
        <span class="font-medium">{{ selectedKeys.size }} tool{{ selectedKeys.size === 1 ? '' : 's' }} selected</span>
        <button
          @click="batchEnable(true)"
          :disabled="batchLoading || viewAsActive"
          :title="viewAsActive ? LEAVE_VIEW_AS : undefined"
          class="btn btn-sm btn-success"
          data-test="batch-enable"
        >
          <span v-if="batchLoading" class="loading loading-spinner loading-xs"></span>
          Enable selected
        </button>
        <button
          @click="batchEnable(false)"
          :disabled="batchLoading || viewAsActive"
          :title="viewAsActive ? LEAVE_VIEW_AS : undefined"
          class="btn btn-sm btn-warning"
          data-test="batch-disable"
        >
          <span v-if="batchLoading" class="loading loading-spinner loading-xs"></span>
          Disable selected
        </button>
        <button
          @click="batchApproval('approve')"
          :disabled="batchLoading || viewAsActive || !hasApprovableSelection"
          :title="viewAsActive ? LEAVE_VIEW_AS : undefined"
          class="btn btn-sm btn-primary"
          data-test="batch-approve"
        >
          <span v-if="batchLoading" class="loading loading-spinner loading-xs"></span>
          Approve
        </button>
        <button
          @click="batchApproval('reject')"
          :disabled="batchLoading || viewAsActive || !hasApprovableSelection"
          :title="viewAsActive ? LEAVE_VIEW_AS : undefined"
          class="btn btn-sm btn-error"
          data-test="batch-reject"
        >
          <span v-if="batchLoading" class="loading loading-spinner loading-xs"></span>
          Reject
        </button>
        <button @click="selectedKeys.clear()" class="btn btn-sm btn-ghost ml-auto">
          Clear selection
        </button>
      </div>
    </div>

    <!-- Batch result summary -->
    <div v-if="batchResult" class="alert" :class="batchResult.failed > 0 ? 'alert-warning' : 'alert-success'">
      <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <div class="flex-1">
        <div class="font-medium">Batch action complete: {{ batchResult.succeeded }} succeeded, {{ batchResult.failed }} failed</div>
        <div v-if="batchResult.failedTools.length > 0" class="text-sm mt-1">
          Failed: {{ batchResult.failedTools.join(', ') }}
        </div>
      </div>
      <button @click="batchResult = null" class="btn btn-sm btn-ghost">Dismiss</button>
    </div>

    <!-- Table card -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body p-0">
        <!-- Spec 108-j J18 (rule 8): GET /tools takes one subject at a time, so
             both applied means no request at all, never one silently dropped. -->
        <div v-if="scopeConflict" class="text-center py-12 px-4 space-y-3" data-test="tools-view-as-conflict">
          <h2 class="text-lg font-semibold">Tools can view as a client or a profile, not both</h2>
          <p class="text-sm text-base-content/70">The address names both. Pick the one to keep.</p>
          <div class="flex flex-wrap justify-center gap-2">
            <button type="button" class="btn btn-sm btn-primary" data-test="tools-view-as-keep-client" @click="resolveConflict('client')">View as {{ conflictClientLabel }}</button>
            <button type="button" class="btn btn-sm btn-primary" data-test="tools-view-as-keep-profile" @click="resolveConflict('profile')">View as {{ conflictProfileLabel }}</button>
          </div>
        </div>

        <!-- Loading -->
        <div v-else-if="loading && allTools.length === 0" class="flex justify-center py-12">
          <span class="loading loading-spinner loading-lg"></span>
        </div>

        <!-- Spec 108-j J19: a refused view-as never blanks the page. -->
        <div v-else-if="scopeError" role="alert" class="alert alert-warning m-4" data-test="tools-scope-error">
          <span>{{ scopeError }}</span>
          <button type="button" class="btn btn-sm btn-ghost" data-test="tools-scope-error-clear" @click="clearViewAs">Clear filter</button>
        </div>

        <!-- Error -->
        <div v-else-if="error" class="alert alert-error m-4">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <span>{{ error }}</span>
          <button @click="loadTools" class="btn btn-sm btn-ghost">Retry</button>
        </div>

        <!-- Empty state -->
        <div v-else-if="filteredTools.length === 0 && !loading" class="text-center py-12 text-base-content/60">
          <svg class="w-16 h-16 mx-auto mb-4 opacity-30" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
          </svg>
          <!-- Audit F11: a bare "No matching tools" cannot be told apart from a
               typo, an empty catalogue, or a server still waiting in quarantine.
               When a search ran, name the query, the scope it actually covered,
               and a next step. -->
          <div v-if="searchQuery" data-test="tools-empty-search">
            <p class="text-lg">No tools match "{{ searchQuery }}"</p>
            <!-- Zero scope has TWO causes and they need opposite advice.
                 "Clear your filters" is itself a false claim when there was
                 nothing to filter: with every server quarantined or none
                 connected, GET /api/v1/tools returns an empty catalogue and no
                 filter is responsible for it. Split on allTools. -->
            <!-- States the OBSERVED catalogue and nothing else. Round-3 review:
                 "no connected server is exposing any" was itself unsupportable —
                 GET /api/v1/tools returns success with an empty list and
                 `partial: true` when a server's tool fetch FAILED
                 (internal/httpapi/server.go), which is "we could not read them",
                 not "there are none". That case gets its own clause instead of a
                 wrong cause. -->
            <p v-if="allTools.length === 0" class="text-sm mt-1">
              There are no tools in this list to search.<template v-if="partial"> Some servers
              could not be read, so their tools are missing from it — see the warning above.</template>
            </p>
            <p v-else-if="searchScope.length === 0" class="text-sm mt-1">
              Nothing was in scope to search — the other active filters excluded every
              tool before the query ran. Clear them to search the full list.
            </p>
            <p v-else class="text-sm mt-1">
              Searched {{ searchScope.length }} tool{{ searchScope.length === 1 ? '' : 's' }}
              across {{ searchScopeServerCount }} server{{ searchScopeServerCount === 1 ? '' : 's' }}<template v-if="!filterStatus">, disabled tools included</template>.
              Every word has to match — try fewer words.
            </p>
            <p v-if="quarantinedServerCount > 0" class="text-sm mt-1">
              {{ quarantinedServerCount }} quarantined server{{ quarantinedServerCount === 1 ? '' : 's' }}
              {{ quarantinedServerCount === 1 ? 'is' : 'are' }} not listed here —
              <router-link to="/review" class="link">review in the Review queue</router-link>.
            </p>
          </div>
          <template v-else-if="viewAsActive && allTools.length === 0">
            <p class="text-lg" data-test="tools-view-as-empty">{{ viewAsEmptyText }}</p>
            <p class="text-sm mt-1">Clear the filter to see every tool.</p>
          </template>
          <template v-else>
            <p class="text-lg">
              {{ hasActiveFilters ? 'No matching tools' : 'No tools available' }}
            </p>
            <p class="text-sm mt-1">
              {{ hasActiveFilters ? 'Try adjusting your filters' : 'Connect MCP servers to see their tools here.' }}
            </p>
          </template>
          <div class="mt-4 space-x-2">
            <button v-if="viewAsActive && allTools.length === 0" type="button" class="btn btn-outline btn-sm" @click="clearViewAs">Clear filter</button>
            <button v-else-if="hasActiveFilters" @click="clearFilters" class="btn btn-outline btn-sm">Clear Filters</button>
            <router-link v-else to="/servers" class="btn btn-primary btn-sm">Manage Servers</router-link>
          </div>
        </div>

        <!-- Table -->
        <div v-else class="overflow-x-auto">
          <table class="table table-sm" data-test="tools-table">
            <thead>
              <tr>
                <th class="w-10">
                  <input
                    type="checkbox"
                    class="checkbox checkbox-sm"
                    aria-label="Select all tools on this page"
                    :checked="allPageSelected"
                    :indeterminate="somePageSelected && !allPageSelected"
                    :disabled="viewAsActive"
                    :title="viewAsActive ? LEAVE_VIEW_AS : undefined"
                    @change="toggleSelectAll"
                    data-test="tools-select-all"
                  />
                </th>
                <th class="cursor-pointer hover:bg-base-200 select-none" @click="sortBy('name')">
                  Tool {{ getSortIndicator('name') }}
                </th>
                <th class="cursor-pointer hover:bg-base-200 select-none" @click="sortBy('server_name')">
                  Server {{ getSortIndicator('server_name') }}
                </th>
                <th v-if="viewAsAdmin">Access</th>
                <th :class="viewAsAdmin ? 'hidden lg:table-cell' : ''">Description</th>
                <th :class="viewAsAdmin ? 'hidden lg:table-cell' : ''" class="cursor-pointer hover:bg-base-200 select-none" @click="sortBy('tier')">
                  Tier {{ getSortIndicator('tier') }}
                </th>
                <th class="cursor-pointer hover:bg-base-200 select-none" @click="sortBy('approval_status')">
                  Approval {{ getSortIndicator('approval_status') }}
                </th>
                <th class="cursor-pointer hover:bg-base-200 select-none" @click="sortBy('enabled')">
                  Enabled {{ getSortIndicator('enabled') }}
                </th>
                <th class="cursor-pointer hover:bg-base-200 select-none" @click="sortBy('usage')">
                  Usage {{ getSortIndicator('usage') }}
                </th>
                <th class="cursor-pointer hover:bg-base-200 select-none" :class="viewAsAdmin ? 'hidden lg:table-cell' : ''" @click="sortBy('last_used')">
                  Last Used {{ getSortIndicator('last_used') }}
                </th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="tool in paginatedTools"
                :key="toolKey(tool)"
                class="hover cursor-pointer"
                :class="{ 'bg-primary/5': selectedKeys.has(toolKey(tool)), 'bg-base-200/70': viewAsAdmin && !isCallable(tool) }"
                :data-not-callable="viewAsAdmin && !isCallable(tool) ? 'true' : undefined"
                @click="openDetail(tool)"
                data-test="tool-row"
              >
                <td @click.stop>
                  <input
                    type="checkbox"
                    class="checkbox checkbox-sm"
                    :aria-label="`Select tool ${tool.name}`"
                    :checked="selectedKeys.has(toolKey(tool))"
                    :disabled="viewAsActive"
                    @change="toggleSelect(tool)"
                  />
                </td>
                <td>
                  <code class="text-xs bg-base-200 px-1.5 py-0.5 rounded">{{ tool.name }}</code>
                </td>
                <td>
                  <router-link
                    :to="serverDetailPath(tool.server_name)"
                    class="link link-primary text-sm font-medium"
                    @click.stop
                  >
                    {{ tool.server_name }}
                  </router-link>
                </td>
                <!-- Spec 108-j J6: the subject's verdict for this tool. The state
                     and the reason are words (never opacity alone) and are not
                     dimmed; only the secondary cells of a tool the subject
                     cannot call are. -->
                <td v-if="viewAsAdmin" :data-test="`tools-row-access-${rowId(tool)}`">
                  <div class="flex flex-wrap items-center gap-1">
                    <span class="badge badge-sm whitespace-nowrap" :class="accessBadgeClass(tool)">{{ accessState(tool) }}</span>
                    <span v-if="!isCallable(tool)" class="text-xs" data-test="tools-access-reason">{{ accessReasonLabel(tool.access?.reason, tool) }}</span>
                    <button
                      v-if="!isCallable(tool)"
                      type="button"
                      class="btn btn-ghost btn-xs min-h-6 min-w-6"
                      :aria-label="`Why is ${tool.name} not callable?`"
                      :data-test="`tools-why-${rowId(tool)}`"
                      @click.stop="openWhy(tool)"
                    >Why?</button>
                  </div>
                </td>
                <td :class="viewAsAdmin ? 'hidden lg:table-cell' : ''">
                  <!-- Descriptions are clipped to keep the row height stable;
                       without a title the clipped half was unreadable without
                       opening the tool (audit F36) — expose the full text on
                       hover/focus instead of losing it entirely. -->
                  <div
                    class="max-w-xs truncate text-sm text-base-content/70"
                    :title="tool.description || undefined"
                  >
                    {{ tool.description || '—' }}
                  </div>
                </td>
                <td :class="viewAsAdmin ? 'hidden lg:table-cell' : ''">
                  <span class="badge badge-sm" :class="getTierBadgeClass(tool)">
                    {{ getTierLabel(tool) }}
                  </span>
                  <span v-if="viewAsAdmin && tool.profile_tier && tool.profile_tier !== tool.tier" class="block text-xs mt-0.5" data-test="tools-profile-tier">as {{ tool.profile_tier }}</span>
                </td>
                <td>
                  <span v-if="tool.approval_status" class="badge badge-sm whitespace-nowrap" :class="getApprovalBadgeClass(tool.approval_status)">
                    {{ toolApprovalLabel(tool.approval_status) }}
                  </span>
                  <span v-else class="text-base-content/30 text-xs">—</span>
                  <!-- Spec 109 FR-027 (link map "Tools row with pending/changed"):
                       a plain path, not useScopeQuery.linkTo — /review/:server is
                       a path-param route with no sticky parameters. -->
                  <router-link
                    v-if="isApprovable(tool)"
                    :to="reviewPath(tool.server_name, tool.approval_status as 'pending' | 'changed')"
                    class="link link-primary text-xs ml-1"
                    data-test="tool-review-link"
                    @click.stop
                  >Review</router-link>
                  <!-- Compact hold evidence: reason icon + TPA ids + overflow. -->
                  <div
                    v-if="holdEvidenceFor(tool)"
                    class="flex items-center gap-1 mt-1 text-xs whitespace-nowrap"
                    :class="holdEvidenceFor(tool)!.toneClass"
                    :title="holdEvidenceFor(tool)!.description"
                    data-test="tool-hold-evidence"
                  >
                    <span aria-hidden="true">{{ holdEvidenceFor(tool)!.icon }}</span>
                    <span
                      v-if="holdEvidenceFor(tool)!.verdict"
                      class="badge badge-xs"
                      :class="holdEvidenceFor(tool)!.verdict!.badgeClass"
                      data-test="tool-hold-verdict"
                    >
                      {{ holdEvidenceFor(tool)!.verdict!.label }}
                    </span>
                    <!-- The reason reads inline when no signal chips crowd it out;
                         otherwise it stays available to screen readers. -->
                    <span :class="holdEvidenceFor(tool)!.signals.length ? 'sr-only' : ''">
                      {{ holdEvidenceFor(tool)!.label }}
                    </span>
                    <span
                      v-for="signal in holdEvidenceFor(tool)!.signals"
                      :key="signal.raw"
                      class="badge badge-xs"
                      :class="holdEvidenceFor(tool)!.chipClass"
                      :title="signal.raw"
                      data-test="tool-hold-signal"
                    >
                      {{ signal.label }}
                    </span>
                    <span
                      v-if="holdEvidenceFor(tool)!.collapsedCount > 0"
                      class="opacity-70"
                      :title="`${holdEvidenceFor(tool)!.collapsedCount} more matched signal(s)`"
                      data-test="tool-hold-signal-more"
                    >+{{ holdEvidenceFor(tool)!.collapsedCount }}</span>
                  </div>
                </td>
                <td>
                  <span v-if="tool.config_denied" class="badge badge-sm badge-error">config-denied</span>
                  <span v-else-if="tool.disabled" class="badge badge-sm badge-warning">disabled</span>
                  <span v-else class="badge badge-sm badge-success">enabled</span>
                </td>
                <td class="text-sm text-right">
                  <router-link
                    v-if="tool.usage && toolCallsLink(tool)"
                    :to="toolCallsLink(tool)!"
                    class="link"
                    data-test="tool-calls-link"
                    @click.stop
                  >
                    {{ tool.usage }}
                  </router-link>
                  <span v-else>{{ tool.usage || 0 }}</span>
                </td>
                <td class="text-sm text-base-content/60" :class="viewAsAdmin ? 'hidden lg:table-cell' : ''">
                  <span v-if="tool.last_used">{{ formatRelativeTime(tool.last_used) }}</span>
                  <span v-else class="text-base-content/30">never</span>
                </td>
              </tr>
            </tbody>
          </table>

          <!-- Pagination -->
          <div v-if="totalPages > 1" class="flex justify-between items-center px-4 py-3 border-t border-base-300">
            <div class="text-sm text-base-content/60">
              Showing {{ (currentPage - 1) * pageSize + 1 }}–{{ Math.min(currentPage * pageSize, sortedTools.length) }} of {{ sortedTools.length }}
            </div>
            <div class="join">
              <button @click="currentPage = 1" :disabled="currentPage === 1" class="join-item btn btn-sm">«</button>
              <button @click="currentPage = Math.max(1, currentPage - 1)" :disabled="currentPage === 1" class="join-item btn btn-sm">‹</button>
              <button class="join-item btn btn-sm">{{ currentPage }} / {{ totalPages }}</button>
              <button @click="currentPage = Math.min(totalPages, currentPage + 1)" :disabled="currentPage === totalPages" class="join-item btn btn-sm">›</button>
              <button @click="currentPage = totalPages" :disabled="currentPage === totalPages" class="join-item btn btn-sm">»</button>
            </div>
            <div class="form-control">
              <select v-model.number="pageSize" class="select select-bordered select-sm" aria-label="Rows per page">
                <option :value="25">25 / page</option>
                <option :value="50">50 / page</option>
                <option :value="100">100 / page</option>
              </select>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Tool Detail Modal -->
    <div v-if="selectedTool" class="modal modal-open" @click.self="selectedTool = null">
      <div class="modal-box max-w-3xl">
        <div class="flex justify-between items-start mb-4">
          <div>
            <h3 class="font-bold text-lg">
              <code class="text-base bg-base-200 px-2 py-1 rounded">{{ selectedTool.name }}</code>
            </h3>
            <div class="flex items-center gap-2 mt-2">
              <router-link :to="serverDetailPath(selectedTool.server_name)" class="link link-primary text-sm">
                {{ selectedTool.server_name }}
              </router-link>
              <span class="badge badge-sm" :class="getTierBadgeClass(selectedTool)">{{ getTierLabel(selectedTool) }}</span>
              <span v-if="selectedTool.config_denied" class="badge badge-sm badge-error">config-denied</span>
              <span v-else-if="selectedTool.disabled" class="badge badge-sm badge-warning">disabled</span>
              <span v-else class="badge badge-sm badge-success">enabled</span>
            </div>
          </div>
          <button class="btn btn-sm btn-circle btn-ghost" @click="selectedTool = null">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        <div class="space-y-4">
          <div v-if="selectedTool.description">
            <h4 class="text-sm font-semibold mb-1 text-base-content/70">Description</h4>
            <p class="text-sm">{{ selectedTool.description }}</p>
          </div>

          <div class="flex gap-6 text-sm">
            <div>
              <span class="text-base-content/60">Usage (30d):</span>
              <span class="ml-1 font-medium">{{ selectedTool.usage || 0 }}</span>
            </div>
            <div v-if="selectedTool.last_used">
              <span class="text-base-content/60">Last used:</span>
              <span class="ml-1">{{ formatRelativeTime(selectedTool.last_used) }}</span>
            </div>
            <div v-if="selectedTool.approval_status">
              <span class="text-base-content/60">Approval:</span>
              <span class="badge badge-sm ml-1 whitespace-nowrap" :class="getApprovalBadgeClass(selectedTool.approval_status)">
                {{ toolApprovalLabel(selectedTool.approval_status) }}
              </span>
            </div>
          </div>

          <div v-if="selectedTool.annotations && Object.keys(selectedTool.annotations).length > 0">
            <h4 class="text-sm font-semibold mb-1 text-base-content/70">Annotations</h4>
            <div class="flex flex-wrap gap-2">
              <span v-if="selectedTool.annotations.readOnlyHint" class="badge badge-sm badge-info">readOnly</span>
              <span v-if="selectedTool.annotations.destructiveHint" class="badge badge-sm badge-error">destructive</span>
              <span v-if="selectedTool.annotations.idempotentHint" class="badge badge-sm badge-ghost">idempotent</span>
              <span v-if="selectedTool.annotations.openWorldHint" class="badge badge-sm badge-ghost">openWorld</span>
            </div>
          </div>

          <div v-if="selectedToolSchema">
            <h4 class="text-sm font-semibold mb-1 flex items-center gap-2 text-base-content/70">
              Input Schema
              <span class="badge badge-sm badge-ghost">JSON</span>
            </h4>
            <div class="mockup-code max-h-64 overflow-y-auto">
              <pre class="text-xs"><code>{{ JSON.stringify(selectedToolSchema, null, 2) }}</code></pre>
            </div>
          </div>
        </div>

        <div class="modal-action">
          <button class="btn btn-sm" @click="selectedTool = null">Close</button>
        </div>
      </div>
    </div>

    <!-- Spec 108-j J6/J17 (FR-046): "Why?" from a view-as row. -->
    <AccessExplainer
      :open="explain !== null"
      :subject="explain?.subject ?? { kind: 'client', name: '' }"
      :tool="explain?.tool"
      @close="explain = null"
    />

    <!-- Hints Panel -->
    <CollapsibleHintsPanel :hints="toolsHints" />
  </div>
</template>

<script setup lang="ts">
import { serverDetailPath, reviewPath } from '@/utils/serverRoute'
import { tierLabel, toolApprovalLabel } from '@/utils/toolQuarantine'
import { formatDate } from '@/utils/datetime'
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { isScopeParamAvailable, useScopeQuery } from '@/composables/useScopeQuery'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import AccessExplainer from '@/components/AccessExplainer.vue'
import ScopeChips from '@/components/scope/ScopeChips.vue'
import type { Hint } from '@/components/CollapsibleHintsPanel.vue'
import type { GlobalTool, GlobalToolsStats, ViewAsCounts } from '@/types/api'
import { parseHoldEvidence, displaySignals, reasonPresentation, verdictPresentation } from '@/utils/holdEvidence'
import { accessReasonLabel, accessStateLabel } from '@/utils/accessReason'
import { tierPhrase } from '@/utils/profiles'
import { pickScopeParams, scopeParamsKey } from '@/utils/scopeParams'
import api from '@/services/api'
import { useAuthStore } from '@/stores/auth'
import { useClientsStore } from '@/stores/clients'
import { useProfilesStore } from '@/stores/profiles'
import { useSystemStore } from '@/stores/system'
import { useServersStore } from '@/stores/servers'

const systemStore = useSystemStore()
const serversStore = useServersStore()
const authStore = useAuthStore()
const clientsStore = useClientsStore()
const profilesStore = useProfilesStore()

// Quarantined servers contribute no tools to GET /api/v1/tools (#1064), so they
// are silently outside every search on this page. App.vue already fetches the
// server list app-wide; this only reads the count so the empty state can say so.
const quarantinedServerCount = computed(() => serversStore.serverCount.quarantined)
// Undefined when the view is mounted without a router — several unit suites do
// exactly that, and a query prefill is not worth making them install one.
const route = useRoute() as ReturnType<typeof useRoute> | undefined
// Spec 109-k: the row "Calls" link (url-filter-contract.md link map). Guarded
// the same way as `route` above — several unit suites mount this view with no
// router installed, and useScopeQuery() itself calls useRoute()/useRouter().
const scopeQuery = route ? useScopeQuery('tools') : undefined

/** `/activity?view=calls&tool=<server:tool>` for a tool row's "Calls" link
 * (url-filter-contract.md link map: "Tools row" -> "Calls"). Null when no
 * router is installed (unit-test harnesses that mount Tools.vue standalone). */
function toolCallsLink(tool: GlobalTool) {
  if (!scopeQuery) return null
  return scopeQuery.linkTo('activity', { view: 'calls', tool: `${tool.server_name}:${tool.name}` })
}

// ---- State ----
const allTools = ref<GlobalTool[]>([])
const stats = ref<GlobalToolsStats | null>(null)
// Spec 108 FR-032: set only for a non-administrator `profile=` view-as.
const viewAsCounts = ref<ViewAsCounts | null>(null)
// Spec 108-j J19: a refused view-as (404 or 403), shown inline with a way out.
const scopeError = ref<string | null>(null)
const partial = ref(false)
const failedServers = ref<string[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const selectedTool = ref<GlobalTool | null>(null)

// ---- Spec 108-j: view-as (profile / client scope) ----
//
// `client` and `profile` are the URL parameters the header Viewing chip and the
// "View as" select write. GET /tools takes ONE subject, so both applied is a
// conflict (rule 8: no request, a two-button state), and a value the endpoint
// cannot take (`-` unattributed, or a client from a non-administrator) is a
// disabled chip that is never sent (rule 5). The chips come from
// useScopeQuery, which already hides a parameter the build does not advertise
// (rule 7); the REST names come from its toRest() through pickScopeParams.
const LEAVE_VIEW_AS = 'Leave view-as to change tools'
const UNATTRIBUTED_NOTE = 'Unattributed applies to Activity and Usage only'
const TENANT_CLIENT_NOTE = 'Viewing as a client requires an administrator'

const isTenant = computed(() => authStore.principalKind === 'tenant')
const chipValue = (name: string): string => scopeQuery?.chips.value.find(chip => chip.name === name)?.value ?? ''

const disabledScope = computed<Record<string, string>>(() => {
  const out: Record<string, string> = {}
  if (chipValue('client') === '-') out.client = UNATTRIBUTED_NOTE
  else if (chipValue('client') && isTenant.value) out.client = TENANT_CLIENT_NOTE
  if (chipValue('profile') === '-') out.profile = UNATTRIBUTED_NOTE
  return out
})

/** The subject the request will carry: the values from toRest(), minus the two
 * Tools cannot apply. */
const appliedScope = computed<{ client?: string; profile?: string }>(() => {
  if (!scopeQuery) return {}
  const rest = pickScopeParams(scopeQuery.toRest())
  const out: { client?: string; profile?: string } = {}
  if (rest.client && !disabledScope.value.client) out.client = rest.client
  if (rest.profile && !disabledScope.value.profile) out.profile = rest.profile
  return out
})
const scopeConflict = computed(() => Boolean(appliedScope.value.client && appliedScope.value.profile))
const requestScope = computed(() => (scopeConflict.value ? {} : appliedScope.value))
const viewAsActive = computed(() => Boolean(requestScope.value.client || requestScope.value.profile))
// An administrator sees every row with its verdict; a tenant's profile view is
// the visible rows only plus a count (no reason, no "Why?").
const viewAsAdmin = computed(() => viewAsActive.value && !isTenant.value)
const requestKey = computed(() => scopeParamsKey(requestScope.value) + (scopeConflict.value ? '!' : ''))

// The server answered /status but the wait timed out first: the filters stay
// hidden (rule 7) and the page fetches unfiltered, with a disabled chip.
const scopeWaitTimedOut = ref(false)
const scopeUnavailableNames = computed(() => {
  if (!scopeWaitTimedOut.value || !route) return []
  return (['client', 'profile'] as const).filter(name => {
    const raw = route.query[name]
    return typeof raw === 'string' && raw !== '' && !isScopeParamAvailable(name)
  })
})

const viewAsAvailable = computed(() => isScopeParamAvailable('profile') || isScopeParamAvailable('client'))
const clientOptions = computed(() => clientsStore.clients.filter(client => client.credential_state !== 'none'))
const viewAsSelectValue = computed(() => {
  if (scopeConflict.value) return ''
  if (appliedScope.value.client) return `client:${appliedScope.value.client}`
  if (appliedScope.value.profile) return `profile:${appliedScope.value.profile}`
  return ''
})
// A subject in the URL that is not in the loaded lists still shows in the select.
const viewAsSelectOrphan = computed(() => {
  const value = viewAsSelectValue.value
  if (!value) return ''
  const [kind, id] = [value.slice(0, value.indexOf(':')), value.slice(value.indexOf(':') + 1)]
  const known = kind === 'client' ? clientOptions.value.some(client => client.id === id) : profilesStore.profiles.some(profile => profile.name === id)
  return known ? '' : id
})

function pickViewAs(value: string) {
  if (!scopeQuery) return
  if (value.startsWith('client:')) scopeQuery.set({ client: value.slice(7), profile: undefined })
  else if (value.startsWith('profile:')) scopeQuery.set({ profile: value.slice(8), client: undefined })
  else scopeQuery.set({ client: undefined, profile: undefined })
}

function clearViewAs() {
  scopeQuery?.clear(['client', 'profile'])
}

function resolveConflict(keep: 'client' | 'profile') {
  scopeQuery?.clear([keep === 'client' ? 'profile' : 'client'])
}

const profileLabelFor = (name: string) => profilesStore.titleFor(name)
const clientLabelFor = (id: string) => clientsStore.clients.find(client => client.id === id)?.display_name ?? id
const conflictClientLabel = computed(() => clientLabelFor(appliedScope.value.client ?? ''))
const conflictProfileLabel = computed(() => profileLabelFor(appliedScope.value.profile ?? ''))

/** "Work · Read-only": a profile's title and what its tier cap means. */
function profilePhrase(name: string): string {
  const title = profilesStore.titleFor(name)
  const p = profilesStore.byName.get(name)
  const tier = tierPhrase(p?.max_tier, p?.tool_counts)
  return tier ? `${title} · ${tier}` : title
}

const viewAsSubjectLabel = computed(() => {
  const { client, profile } = requestScope.value
  return client ? clientLabelFor(client) : `profile ${profileLabelFor(profile ?? '')}`
})

// Spec 108-j J6: the numbers are computed from the rows the administrator got.
const viewAsCountsFromRows = computed(() => {
  let visible = 0
  let callable = 0
  let hidden = 0
  for (const tool of allTools.value) {
    const access = tool.access
    if (!access) { visible++; callable++; continue }
    if (access.visible) visible++
    else hidden++
    if (access.callable) callable++
  }
  return { visible, callable, hidden }
})

const viewAsBannerText = computed(() => {
  const { client, profile } = requestScope.value
  if (isTenant.value) {
    const hidden = viewAsCounts.value?.hidden ?? 0
    return `${hidden} tool${hidden === 1 ? '' : 's'} hidden by this profile`
  }
  const counts = viewAsCountsFromRows.value
  let who: string
  if (client) {
    const profileName = clientsStore.clients.find(row => row.id === client)?.profile
    who = `Viewing as ${clientLabelFor(client)}${profileName ? ` (${profilePhrase(profileName)})` : ''}`
  } else {
    who = `Viewing as profile ${profilePhrase(profile ?? '')}`
  }
  return `${who}: ${counts.visible} visible · ${counts.callable} callable · ${counts.hidden} hidden`
})

const viewAsEmptyText = computed(() => {
  if (isTenant.value) return 'No tools visible under this profile'
  return `${viewAsSubjectLabel.value[0].toUpperCase()}${viewAsSubjectLabel.value.slice(1)} can see no tools`
})

// J14 (#1437 item 2): a disabled server's tools may or may not be in the listing
// (the last-known set can linger after a disable), so say so instead of implying
// the list is whole.
const disabledServerNote = computed(() => {
  const count = serversStore.serverCount.disabled
  if (!viewAsActive.value || count === 0) return ''
  return `${count} disabled server${count === 1 ? '' : 's'}: ${count === 1 ? 'its' : 'their'} tools may not be listed.`
})
const disabledServersLink = computed(() => (scopeQuery ? scopeQuery.linkTo('servers', { status: 'disabled' }) : null))

const SHOW_OPTIONS = [
  { value: 'all', label: 'All' },
  { value: 'callable', label: 'Callable' },
  { value: 'not_callable', label: 'Not callable' },
] as const
// Page-local, never in the URL: it only narrows the rows already loaded.
const showFilter = ref<'all' | 'callable' | 'not_callable'>('all')

const isCallable = (tool: GlobalTool): boolean => tool.access?.callable !== false
const accessState = (tool: GlobalTool): string => accessStateLabel(tool.access ?? { visible: true, callable: true })
const accessBadgeClass = (tool: GlobalTool): string => {
  if (isCallable(tool)) return 'badge-success'
  return tool.access?.visible ? 'badge-warning' : 'badge-error'
}
// A tool the subject cannot call is marked by a tinted row (`bg-base-200/70`) and,
// above all, by the state and reason words in its Access cell. Opacity is not used:
// it multiplies the alpha of every cell and drops the row's text under AA contrast.
const rowId = (tool: GlobalTool): string => `${tool.server_name}__${tool.name}`

const explain = ref<{ subject: { kind: 'client' | 'profile'; name: string }; tool: string } | null>(null)
function openWhy(tool: GlobalTool) {
  const { client, profile } = requestScope.value
  const subject = client ? { kind: 'client' as const, name: client } : { kind: 'profile' as const, name: profile ?? '' }
  explain.value = { subject, tool: `${tool.server_name}:${tool.name}` }
}

// ---- Filters ----
const searchQuery = ref('')
const filterServer = ref('')
const filterStatus = ref('')
const filterTier = ref('')
const filterApproval = ref('')

// Debounce search
let searchTimer: ReturnType<typeof setTimeout> | null = null
watch(searchQuery, () => {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => { currentPage.value = 1 }, 300)
})

// ---- Sort ----
type SortCol = 'name' | 'server_name' | 'tier' | 'approval_status' | 'enabled' | 'usage' | 'last_used'
const sortColumn = ref<SortCol>('name')
const sortDirection = ref<'asc' | 'desc'>('asc')

function sortBy(col: SortCol) {
  if (sortColumn.value === col) {
    sortDirection.value = sortDirection.value === 'asc' ? 'desc' : 'asc'
  } else {
    sortColumn.value = col
    sortDirection.value = col === 'usage' || col === 'last_used' ? 'desc' : 'asc'
  }
}

function getSortIndicator(col: SortCol): string {
  if (sortColumn.value !== col) return ''
  return sortDirection.value === 'asc' ? '↑' : '↓'
}

// ---- Pagination ----
const currentPage = ref(1)
const pageSize = ref(25)

// ---- Selection ----
const selectedKeys = ref(new Set<string>())

function toolKey(tool: GlobalTool): string {
  return `${tool.server_name}\x00${tool.name}`
}

const allPageSelected = computed(() =>
  paginatedTools.value.length > 0 && paginatedTools.value.every(t => selectedKeys.value.has(toolKey(t)))
)

const somePageSelected = computed(() =>
  paginatedTools.value.some(t => selectedKeys.value.has(toolKey(t)))
)

function toggleSelectAll() {
  if (allPageSelected.value) {
    paginatedTools.value.forEach(t => selectedKeys.value.delete(toolKey(t)))
  } else {
    paginatedTools.value.forEach(t => selectedKeys.value.add(toolKey(t)))
  }
}

function toggleSelect(tool: GlobalTool) {
  const k = toolKey(tool)
  if (selectedKeys.value.has(k)) {
    selectedKeys.value.delete(k)
  } else {
    selectedKeys.value.add(k)
  }
}

// ---- Batch actions ----
const batchLoading = ref(false)
const batchResult = ref<{ succeeded: number; failed: number; failedTools: string[] } | null>(null)

async function batchEnable(enabled: boolean) {
  if (batchLoading.value || selectedKeys.value.size === 0) return
  batchLoading.value = true
  batchResult.value = null

  const targets = allTools.value.filter(t => selectedKeys.value.has(toolKey(t)))
  let succeeded = 0
  let failed = 0
  const failedTools: string[] = []

  for (const tool of targets) {
    try {
      const resp = await api.setToolEnabled(tool.server_name, tool.name, enabled)
      if (resp.success) {
        succeeded++
        // Update local state immediately so row reflects change
        tool.disabled = !enabled
      } else {
        failed++
        failedTools.push(`${tool.server_name}:${tool.name} (${resp.error || 'failed'})`)
      }
    } catch (err) {
      failed++
      failedTools.push(`${tool.server_name}:${tool.name}`)
    }
  }

  batchResult.value = { succeeded, failed, failedTools }
  selectedKeys.value.clear()
  batchLoading.value = false

  // Refresh to get authoritative server state
  await loadTools()
}

// ---- Batch approve / reject (multi-server fan-out) ----
// A tool is "approvable" when it is awaiting a decision: pending (brand-new) or
// changed (rug-pull). Already-approved tools are silently skipped so the action
// never errors when a mixed selection includes them.
function isApprovable(tool: GlobalTool): boolean {
  return tool.approval_status === 'pending' || tool.approval_status === 'changed'
}

const hasApprovableSelection = computed(() =>
  allTools.value.some(t => selectedKeys.value.has(toolKey(t)) && isApprovable(t))
)

async function batchApproval(action: 'approve' | 'reject') {
  if (batchLoading.value || !hasApprovableSelection.value) return
  batchLoading.value = true
  batchResult.value = null

  // Group the approvable tools in the selection by server, then fan out one
  // call per server — the page spans multiple servers but each approve/block
  // endpoint is per-server.
  const byServer = new Map<string, string[]>()
  for (const tool of allTools.value) {
    if (!selectedKeys.value.has(toolKey(tool)) || !isApprovable(tool)) continue
    const names = byServer.get(tool.server_name) || []
    names.push(tool.name)
    byServer.set(tool.server_name, names)
  }

  let toolCount = 0
  byServer.forEach(names => { toolCount += names.length })

  const failedServers: string[] = []
  await Promise.all(
    Array.from(byServer.entries()).map(async ([server, names]) => {
      try {
        const resp = action === 'approve'
          ? await api.approveTools(server, names)
          : await api.blockTools(server, names)
        if (!resp.success) {
          failedServers.push(`${server} (${resp.error || 'failed'})`)
        }
      } catch (err) {
        failedServers.push(`${server} (${err instanceof Error ? err.message : 'failed'})`)
      }
    })
  )

  const verb = action === 'approve' ? 'Approved' : 'Rejected'
  const okServers = byServer.size - failedServers.length
  if (failedServers.length === 0) {
    systemStore.addToast({
      type: 'success',
      title: `${verb} tools`,
      message: `${verb} ${toolCount} tool${toolCount === 1 ? '' : 's'} across ${byServer.size} server${byServer.size === 1 ? '' : 's'}`,
    })
  } else {
    systemStore.addToast({
      type: 'error',
      title: `${verb} with errors`,
      message: `${okServers} of ${byServer.size} server${byServer.size === 1 ? '' : 's'} succeeded. Failed: ${failedServers.join(', ')}`,
    })
  }

  selectedKeys.value.clear()
  batchLoading.value = false

  // Refresh to get authoritative approval state + stats.
  await loadTools()
}

// ---- Computed: available filter options ----
const availableServers = computed(() => {
  const s = new Set<string>()
  allTools.value.forEach(t => s.add(t.server_name))
  return Array.from(s).sort()
})

const hasActiveFilters = computed(() =>
  !!searchQuery.value || !!filterServer.value || !!filterStatus.value || !!filterTier.value || !!filterApproval.value
)

// Clickable stat cards (parity with Servers page): each card drives the
// status filter and toggles off when its active card is clicked again. The
// "Needs review" card (Spec 109 FR-027) is a plain link to /review, not one of
// these — it never sets filterApproval.
type StatCard = 'total' | 'enabled' | 'disabled'

// Review round 4: Total must only read as active when the table is truly
// unfiltered. An approval-only filter (filterStatus empty) still narrows the
// table, so none of Total/Enabled/Disabled correctly describes it — return
// null rather than defaulting to 'total'.
// Round-9 fix: that filterApproval guard must only suppress TOTAL, not
// Enabled/Disabled. It used to run before the filterStatus checks, so it
// always won whenever an approval filter was set — activeStatCard() was
// null no matter what filterStatus held. That made selectStatCard's toggle
// condition (`activeStatCard.value === card`) permanently false for
// 'enabled'/'disabled': clicking the Enabled/Disabled stat card while an
// approval filter is active still applied filterStatus (so the table did
// filter), but the card never rendered as active and a second click ran the
// same no-op branch again instead of toggling off. filterStatus === 'enabled'
// / 'disabled' fully describes the row regardless of any additional approval
// filter, so those checks must run first; only fall through to the
// filterApproval-only null when filterStatus is empty.
const activeStatCard = computed<StatCard | null>(() => {
  if (filterStatus.value === 'enabled') return 'enabled'
  if (filterStatus.value === 'disabled') return 'disabled'
  if (filterApproval.value) return null
  return 'total'
})

function selectStatCard(card: StatCard) {
  // Toggle: re-clicking the active card resets to the unfiltered "total" view.
  // Also clears filterApproval (review round 1): Total is the row's "reset"
  // gesture (clearFilters clears both too), and leaving an approval filter
  // in place after Total is clicked filtered the table with no visible way
  // to tell from the stat row.
  if (card === 'total' || activeStatCard.value === card) {
    filterStatus.value = ''
    filterApproval.value = ''
    return
  }
  filterStatus.value = card // 'enabled' | 'disabled'
}

// ---- Tier (Spec 109 FR-028/X11): `tool.tier` comes from the backend
// (contracts.AnnotationTier) — never computed here. An unannotated tool is
// labelled "Unannotated", never silently shown as "write" (the X11 bug this
// replaces: the old local getRisk() defaulted anything without hints to
// "write").
function getTier(tool: GlobalTool): string {
  return tool.tier || 'unannotated'
}

function getTierLabel(tool: GlobalTool): string {
  return tierLabel(getTier(tool))
}

function getTierBadgeClass(tool: GlobalTool): string {
  const t = getTier(tool)
  if (t === 'destructive') return 'badge-error'
  if (t === 'write') return 'badge-warning'
  if (t === 'read') return 'badge-success'
  return 'badge-ghost'
}

function getApprovalBadgeClass(status: string): string {
  if (status === 'approved') return 'badge-success'
  if (status === 'pending') return 'badge-warning'
  if (status === 'changed') return 'badge-error'
  return 'badge-ghost'
}

// ---- Hold evidence (Spec 088 FR-008/FR-009/FR-012) ----
// This page is a cross-server list, so its evidence stays COMPACT: the reason
// icon carries the threat-vs-precaution distinction, plus the TPA signature ids
// and an overflow count. The full badge — descriptions, verdict, scan-report
// links — belongs to the server detail page, which is one click away.

/** Compact cap: TPA ids are never collapsed by it (utils/holdEvidence). */
const COMPACT_SIGNAL_CAP = 1

interface CompactHoldEvidence {
  icon: string
  /** Plain-language reason; shown inline when there are no signal chips. */
  label: string
  description: string
  toneClass: string
  chipClass: string
  signals: { label: string; raw: string }[]
  /** Delivered-but-collapsed signals only — never a claim beyond the list. */
  collapsedCount: number
  /** FR-009: verdict severity must be visible, or a warnings hold and a
      dangerous hold with the same signals would look identical. */
  verdict: { label: string; badgeClass: string } | null
}

function buildHoldEvidence(tool: GlobalTool): CompactHoldEvidence | null {
  // FR-012: only a tool still awaiting a decision may show hold evidence, so an
  // approved/released record never renders stale findings.
  if (!isApprovable(tool)) return null

  const evidence = parseHoldEvidence(tool)
  if (!evidence) return null
  const reason = reasonPresentation(evidence)
  if (!reason) return null

  const { visible, collapsedCount } = displaySignals(evidence, COMPACT_SIGNAL_CAP)

  return {
    icon: reason.tone === 'threat' ? '⚠️' : reason.tone === 'precaution' ? '🛡️' : 'ℹ️',
    label: reason.label,
    description: reason.description,
    toneClass:
      reason.tone === 'threat'
        ? 'text-error'
        : reason.tone === 'precaution'
          ? 'text-warning'
          : 'text-base-content/70',
    chipClass:
      reason.tone === 'threat'
        ? 'badge-error'
        : reason.tone === 'precaution'
          ? 'badge-warning'
          : 'badge-ghost',
    signals: visible.map(s => ({ label: s.label, raw: s.raw })),
    collapsedCount,
    verdict: (() => {
      const v = verdictPresentation(evidence.verdict)
      if (!v) return null
      return {
        label: v.label,
        badgeClass: v.tone === 'danger' ? 'badge-error' : v.tone === 'warning' ? 'badge-warning' : 'badge-ghost',
      }
    })(),
  }
}

// Parsed once per visible page rather than on every template access.
const holdEvidenceByKey = computed(() => {
  const map = new Map<string, CompactHoldEvidence>()
  for (const tool of paginatedTools.value) {
    const evidence = buildHoldEvidence(tool)
    if (evidence) map.set(toolKey(tool), evidence)
  }
  return map
})

function holdEvidenceFor(tool: GlobalTool): CompactHoldEvidence | undefined {
  return holdEvidenceByKey.value.get(toolKey(tool))
}

// ---- Computed: filtering ----
// The population the search runs against: every filter EXCEPT the search box.
// Split out so the empty state can name a scope that is literally true even when
// a stat card or dropdown is also narrowing the list. All the filters are ANDed,
// so pulling the search to the end leaves the result identical.
const searchScope = computed(() => {
  let tools = allTools.value

  if (filterServer.value) {
    tools = tools.filter(t => t.server_name === filterServer.value)
  }

  if (filterStatus.value === 'enabled') {
    tools = tools.filter(t => !t.disabled && !t.config_denied)
  } else if (filterStatus.value === 'disabled') {
    tools = tools.filter(t => t.disabled && !t.config_denied)
  } else if (filterStatus.value === 'config_denied') {
    tools = tools.filter(t => t.config_denied)
  }

  if (filterTier.value) {
    tools = tools.filter(t => getTier(t) === filterTier.value)
  }

  if (filterApproval.value) {
    tools = tools.filter(t => t.approval_status === filterApproval.value)
  }

  if (viewAsAdmin.value && showFilter.value !== 'all') {
    tools = tools.filter(t => (showFilter.value === 'callable' ? isCallable(t) : !isCallable(t)))
  }

  return tools
})

const filteredTools = computed(() => {
  // Audit F11: the whole query used to have to appear as one contiguous
  // substring of a single field, so "context7 documentation" matched nothing
  // while "documentation" matched it. Match each whitespace-separated term
  // independently instead (AND across terms, OR across fields). This is a strict
  // superset of the old behaviour -- if the whole query was a substring of a
  // field, so is every one of its terms -- so no result that matched before can
  // disappear. Still a substring filter, not BM25: this page is an inventory,
  // and the ranked index search is a separate surface.
  if (!searchQuery.value) return searchScope.value

  const terms = searchQuery.value.toLowerCase().split(/\s+/).filter(Boolean)
  return searchScope.value.filter(t => {
    const name = t.name.toLowerCase()
    const description = (t.description || '').toLowerCase()
    const server = t.server_name.toLowerCase()
    return terms.every(term =>
      name.includes(term) || description.includes(term) || server.includes(term)
    )
  })
})

// Scope actually covered by the search, for the empty state.
const searchScopeServerCount = computed(
  () => new Set(searchScope.value.map(t => t.server_name)).size
)

// ---- Computed: sorting ----
const sortedTools = computed(() => {
  const list = [...filteredTools.value]
  const col = sortColumn.value
  const dir = sortDirection.value

  list.sort((a, b) => {
    let av: string | number
    let bv: string | number

    switch (col) {
      case 'name':
        av = a.name; bv = b.name; break
      case 'server_name':
        av = a.server_name; bv = b.server_name; break
      case 'tier':
        av = getTier(a); bv = getTier(b); break
      case 'approval_status':
        av = a.approval_status || ''; bv = b.approval_status || ''; break
      case 'enabled': {
        // sort: enabled first in asc
        const ae = (!a.disabled && !a.config_denied) ? 1 : 0
        const be = (!b.disabled && !b.config_denied) ? 1 : 0
        av = ae; bv = be; break
      }
      case 'usage':
        av = a.usage || 0; bv = b.usage || 0; break
      case 'last_used':
        av = a.last_used ? new Date(a.last_used).getTime() : 0
        bv = b.last_used ? new Date(b.last_used).getTime() : 0
        break
      default:
        av = ''; bv = ''
    }

    if (typeof av === 'number' && typeof bv === 'number') {
      return dir === 'asc' ? av - bv : bv - av
    }
    const as = String(av); const bs = String(bv)
    return dir === 'asc' ? as.localeCompare(bs) : bs.localeCompare(as)
  })

  return list
})

// ---- Computed: pagination ----
const totalPages = computed(() => Math.ceil(sortedTools.value.length / pageSize.value))

const paginatedTools = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return sortedTools.value.slice(start, start + pageSize.value)
})

// ---- Computed: modal schema ----
const selectedToolSchema = computed(() => {
  if (!selectedTool.value) return null
  const t = selectedTool.value as any
  return t.input_schema || t.schema || null
})

// ---- Methods ----
// Every load takes a ticket and only the latest applies: choosing another
// subject, removing a chip or a refresh must never let an older answer for the
// previous subject land afterwards (R1).
let loadSeq = 0

async function loadTools() {
  if (!scopeReady) await scopeReadyPromise
  const seq = ++loadSeq
  // Rule 8 (J18): two subjects means no request at all.
  if (scopeConflict.value) {
    allTools.value = []
    stats.value = null
    viewAsCounts.value = null
    scopeError.value = null
    error.value = null
    loading.value = false
    return
  }

  loading.value = true
  error.value = null
  scopeError.value = null
  const scope = requestScope.value
  const scoped = Boolean(scope.client || scope.profile)

  try {
    const resp = scoped ? await api.getGlobalTools(scope) : await api.getGlobalTools()
    if (seq !== loadSeq) return
    if (resp.success && resp.data) {
      allTools.value = resp.data.tools || []
      stats.value = resp.data.stats
      partial.value = resp.data.partial || false
      failedServers.value = resp.data.failed_servers || []
      viewAsCounts.value = resp.data.counts ?? null
    } else {
      error.value = resp.error || 'Failed to load tools'
    }
  } catch (err) {
    if (seq !== loadSeq) return
    const status = (err as { status?: number } | null)?.status
    if (scoped && (status === 404 || status === 403)) {
      // J19: a refused view-as is an inline message with a way out, never a
      // blanked page. The rows of the previous subject are dropped so they are
      // not mistaken for this one's.
      allTools.value = []
      stats.value = null
      viewAsCounts.value = null
      scopeError.value = status === 403
        ? 'Requires an administrator'
        : scope.client ? 'Client not found' : 'Profile not found'
    } else {
      error.value = err instanceof Error ? err.message : 'Unknown error'
    }
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

function openDetail(tool: GlobalTool) {
  selectedTool.value = tool
}

function clearFilters() {
  searchQuery.value = ''
  filterServer.value = ''
  filterStatus.value = ''
  filterTier.value = ''
  filterApproval.value = ''
  currentPage.value = 1
}

function formatRelativeTime(ts: string): string {
  const diff = Date.now() - new Date(ts).getTime()
  if (diff < 60_000) return 'just now'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`
  if (diff < 30 * 86_400_000) return `${Math.floor(diff / 86_400_000)}d ago`
  return formatDate(ts)
}

// Reset page when filters/sort change
watch([filterServer, filterStatus, filterTier, filterApproval, sortColumn, sortDirection, showFilter], () => {
  currentPage.value = 1
})

// A shrinking result (another view-as subject, a refresh) must never strand the
// table on a page past the end: zero rows and no pager (F2.2).
watch(() => Math.ceil(sortedTools.value.length / pageSize.value), (pages) => {
  if (currentPage.value > pages) currentPage.value = Math.max(1, pages)
})

// Live QA fix (Spec 109-k, FR-080 "router.replace on change"): the controls
// above only ever READ the URL (applyQueryParam()) — clearing a filter, or
// picking a new one, updated the table but left the address bar showing the
// stale query, so a copied/bookmarked URL silently reapplied it on reload
// (SC-009's URL round-trip). searchQuery gets its own watcher just below,
// rather than joining this one: it changes on every keystroke, and the two
// need to stay independent of each other's timing.
watch([filterServer, filterStatus, filterTier, filterApproval], () => {
  if (!scopeQuery) return
  scopeQuery.set({
    server: filterServer.value || undefined,
    status: filterStatus.value || undefined,
    tier: filterTier.value || undefined,
    // `risk` is only ever a read-side alias (url-filter-contract.md rule 6);
    // once resolved into `filterTier` the canonical `tier` param is what gets
    // written back, so a stale `?risk=` left over from an old link does not
    // linger next to it.
    risk: undefined,
    approval: filterApproval.value || undefined,
  })
})

watch(searchQuery, value => {
  if (!scopeQuery) return
  scopeQuery.set({ q: value || undefined })
})

// Also reset when pageSize changes
watch(pageSize, () => { currentPage.value = 1 })

// ---- Hints ----
const toolsHints = computed<Hint[]>(() => [
  {
    icon: '🔍',
    title: 'Global Tools Overview',
    description: 'See every tool across all configured MCP servers in one place',
    sections: [
      {
        title: 'Audit and cleanup',
        list: [
          'Search by tool name, description, or server',
          'Filter by status, tier, or approval state',
          'Sort any column to find stale or unused tools',
          'Select multiple tools for batch enable/disable, or batch approve/reject across servers',
        ],
      },
      {
        title: 'List all tools via CLI',
        codeBlock: {
          language: 'bash',
          code: '# List all tools across all servers\nmcpproxy tools list\n\n# Filter by status\nmcpproxy tools list --status disabled\n\n# Disable tools in bulk\nmcpproxy tools disable server1:tool_a server2:tool_b',
        },
      },
    ],
  },
])

// ---- Lifecycle ----
onMounted(() => {
  // Tools is the canonical search surface (audit F20): the header box and the
  // retired /search route both arrive here with ?q=, so the query has to
  // prefill the filter rather than being silently dropped. Spec 109-k: the
  // rest of the contract's Tools-page parameters (`server`, `tier`/`risk`,
  // `status`, `approval` — url-filter-contract.md "Parameters", all
  // client-side here per the contract) are read the same way, so a deep link
  // (Home/Server-card links, a Clients row "Tools it sees", ...) actually
  // narrows the page instead of landing on the unfiltered table.
  applyQueryParam()
  // The Viewing chip and the sidebar normally load these lists; a direct visit
  // to a view-as URL must not wait for them to name the subject.
  if (!profilesStore.loaded && !profilesStore.loading) void profilesStore.fetchProfiles()
  if (!isTenant.value && clientsStore.clients.length === 0) void clientsStore.refreshPresence()
  void firstLoad()
})

// Spec 108-j J3 (rule 1): a URL that names a subject must not fetch before
// GET /status has said whether the build supports it, or the first request goes
// out unfiltered and every row flashes before the refetch. A URL with no scope
// parameter does not wait at all.
// Rule-1 ready gate (F2.1/F3.1/F4.1): every loader waits for the startup
// /status wait to finish, so no control, refresh or SSE event can send an
// unfiltered request while the first filtered one is still pending.
let scopeReady = false
let releaseScopeReady: () => void = () => {}
const scopeReadyPromise = new Promise<void>(resolve => { releaseScopeReady = resolve })
let scopeWatchArmed = false
async function firstLoad() {
  const urlHasScope = Boolean(route && ['profile', 'client', 'token'].some(name => {
    const raw = route.query[name]
    return typeof raw === 'string' && raw !== ''
  }))
  if (urlHasScope) {
    await systemStore.waitForScopeFeatures()
    // The wait is over: any named param still unavailable gets the disabled
    // chip, including when /status answered but did not advertise it (F5.2).
    scopeWaitTimedOut.value = true
  }
  scopeReady = true
  releaseScopeReady()
  scopeWatchArmed = true
  await loadTools()
}

// The applied subject changed (a chip removed, the header chip, the select, or
// the feature list arriving after the first fetch): refetch under it.
watch(requestKey, () => {
  if (!scopeWatchArmed) return
  showFilter.value = 'all'
  currentPage.value = 1
  selectedKeys.value.clear()
  void loadTools()
})

// A second search/filter from elsewhere while Tools is already open is a
// route query change, not a remount — without this watch the controls would
// appear to do nothing (this is exactly the gap the audit found: a URL nav
// after the initial mount had no effect either).
watch(
  () => route?.query,
  () => applyQueryParam(),
  { deep: true }
)

function applyQueryParam() {
  const q = route?.query
  if (!q) return
  const str = (v: unknown): string => (typeof v === 'string' ? v : '')

  const query = str(q.q)
  if (query && query !== searchQuery.value) {
    searchQuery.value = query
    currentPage.value = 1
  }

  // Symmetric by design (zcode review round 1, F3, matching Activity.vue's
  // applyRouteFilters): set from the query param when present AND cleared
  // when absent. Vue Router reuses this component across a same-route
  // navigation (no remount), so a "set only if present" read left a stale
  // filter in force after a later URL dropped the param — and the write-back
  // watch below then resurrected it into a URL that had just been cleared.
  filterServer.value = str(q.server)
  filterStatus.value = str(q.status)
  filterApproval.value = str(q.approval)

  // `?risk=` stays a query alias for `?tier=` for old bookmarks/links
  // (url-filter-contract.md rule 6); an explicit `tier` wins if somehow both
  // are present.
  filterTier.value = str(q.tier) || str(q.risk)
}
</script>
