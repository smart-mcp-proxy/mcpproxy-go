<template>
  <div class="space-y-6">
    <!-- Telemetry Notice Banner -->
    <TelemetryBanner />

    <!-- Upgrade nudge (Spec 079): dismissible per-version update banner -->
    <UpdateBanner />

    <!-- "What needs me": servers in a bad state and tools awaiting approval.
         These live above the panel switcher rather than inside a panel — they
         are the one thing on this page the user is expected to act on, and
         burying them under a tab means the landing page can look calm while a
         server is down or an unreviewed tool is waiting. Each renders only
         when it has something to say, so a healthy install sees neither. -->
    <!-- Servers Needing Attention Banner (using unified health status) -->
    <!-- UX audit F14: below `sm` the alert stacks instead of sharing its row
         with "View All Servers", and `min-w-0` lets the text shrink, so at 390px
         "Host not found" wraps as words rather than one character per line. -->
    <div
      v-if="serversNeedingAttention.length > 0"
      class="alert alert-vertical sm:alert-horizontal alert-warning"
    >
      <svg class="w-6 h-6 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.732-.833-2.5 0L3.732 16.5c-.77.833.192 2.5 1.732 2.5z" />
      </svg>
      <div class="flex-1 min-w-0">
        <h3 class="font-bold">{{ serversNeedingAttention.length }} server{{ serversNeedingAttention.length !== 1 ? 's' : '' }} need{{ serversNeedingAttention.length === 1 ? 's' : '' }} attention</h3>
        <div class="text-sm space-y-1 mt-1">
          <div v-for="server in serversNeedingAttention.slice(0, 3)" :key="server.name" class="flex flex-wrap items-center gap-x-2 gap-y-1 min-w-0">
            <span :class="server.health?.level === 'unhealthy' ? 'text-error' : 'text-warning'" aria-hidden="true">●</span>
            <router-link :to="serverDetailPath(server.name)" class="font-medium link link-hover break-words">{{ server.name }}</router-link>
            <!-- No opacity: the failure reason is the point of the banner, and a
                 faded foreground on a filled alert is what F9 measured at 2.x:1. -->
            <span class="break-words">{{ server.health?.summary }}</span>
            <button
              v-if="server.health?.action === 'login'"
              @click="triggerServerAction(server.name, 'oauth_login')"
              class="btn btn-xs btn-primary"
            >
              Login
            </button>
            <button
              v-if="server.health?.action === 'restart'"
              @click="triggerServerAction(server.name, 'restart')"
              class="btn btn-xs btn-primary"
            >
              Restart
            </button>
            <button
              v-if="server.health?.action === 'enable'"
              @click="triggerServerAction(server.name, 'enable')"
              class="btn btn-xs btn-primary"
            >
              Enable
            </button>
            <router-link
              v-if="server.health?.action === 'set_secret'"
              to="/secrets"
              class="btn btn-xs btn-primary"
            >
              Set Secret
            </router-link>
            <router-link
              v-if="server.health?.action === 'configure'"
              :to="serverDetailPath(server.name, 'config')"
              class="btn btn-xs btn-primary"
            >
              Configure
            </router-link>
            <!-- Audit F11: DNS / malformed-URL failures are address problems.
                 Restart redials the same broken address; Edit URL does not. -->
            <router-link
              v-if="server.health?.action === 'edit_url'"
              :to="`${serverDetailPath(server.name, 'config')}&focus=endpoint`"
              class="btn btn-xs btn-primary"
              data-test="attention-edit-url"
            >
              Edit URL
            </router-link>
          </div>
          <div v-if="serversNeedingAttention.length > 3" class="text-xs opacity-60">
            ... and {{ serversNeedingAttention.length - 3 }} more
          </div>
        </div>
      </div>
      <!-- `whitespace-nowrap` + `shrink-0`: at 390px the label was wrapping onto
           three clipped lines inside the alert grid (UX audit F14/F36). -->
      <router-link to="/servers" class="btn btn-sm shrink-0 whitespace-nowrap">
        View All Servers
      </router-link>
    </div>

    <!-- Tools Pending Quarantine Approval Banner -->
    <div
      v-if="totalPendingTools > 0"
      class="alert alert-warning"
    >
      <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
      </svg>
      <div class="flex-1">
        <h3 class="font-bold">{{ totalPendingTools }} tool{{ totalPendingTools !== 1 ? 's' : '' }} pending approval across {{ serversWithPendingTools.length }} server{{ serversWithPendingTools.length !== 1 ? 's' : '' }}</h3>
        <div class="text-sm space-y-1 mt-1">
          <div v-for="entry in serversWithPendingTools.slice(0, 5)" :key="entry.serverName" class="flex items-center gap-2">
            <span class="text-warning">&#9679;</span>
            <router-link :to="serverDetailPath(entry.serverName)" class="font-medium link link-hover">{{ entry.serverName }}</router-link>
            <span class="opacity-70">{{ entry.count }} tool{{ entry.count !== 1 ? 's' : '' }} pending</span>
          </div>
          <div v-if="serversWithPendingTools.length > 5" class="text-xs opacity-60">
            ... and {{ serversWithPendingTools.length - 5 }} more server{{ serversWithPendingTools.length - 5 !== 1 ? 's' : '' }}
          </div>
        </div>
      </div>
      <router-link to="/servers" class="btn btn-sm">
        Review Tools
      </router-link>
    </div>

    <!-- Usage ↔ Overview switcher (Spec 069 T016). Usage is the default panel
         (analytics-as-landing-page); each tab maps to a deep-linkable route
         (/usage, /overview) so the panel survives a reload or a shared link. -->
    <div role="tablist" class="tabs tabs-boxed w-fit" data-test="dashboard-view-switcher">
      <a
        role="tab"
        class="tab"
        :class="activeView === 'usage' ? 'tab-active' : ''"
        data-test="dashboard-tab-usage"
        @click="selectUsage"
      >Usage</a>
      <a
        role="tab"
        class="tab"
        :class="activeView === 'overview' ? 'tab-active' : ''"
        data-test="dashboard-tab-overview"
        @click="selectOverview"
      >Overview</a>
    </div>

    <!-- Usage view: the panel wrapper is always in the DOM (kept hidden with
         v-show so switching back is instant and the Overview subtree is never
         torn down, SC-006). The heavy chart bundle + the usage fetch inside
         UsageView are mounted only once the panel has been active
         (usageEverActive) AND the server list has arrived, and stay code-split
         behind Suspense, so the Dashboard shell still paints immediately
         (SC-004). -->
    <div v-show="activeView === 'usage'" data-test="dashboard-usage-panel">
      <!-- First run: no upstream servers configured, so the analytics panel
           would only ever show "no data". Point the new user at the one action
           that makes the dashboard useful instead. -->
      <div
        v-if="showFirstRunCta"
        class="card bg-base-200 border border-base-300"
        data-test="dashboard-usage-first-run"
      >
        <div class="card-body items-center text-center py-12">
          <svg class="w-12 h-12 opacity-40" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />
          </svg>
          <h3 class="font-semibold text-lg mt-2">Add your first server to see usage</h3>
          <p class="text-sm opacity-60 max-w-md">
            No upstream MCP servers are configured yet, so there is nothing to chart.
            Add one and this page will show call volume, token sinks, error rates and a timeline.
          </p>
          <div class="flex flex-wrap gap-2 justify-center mt-2">
            <button
              class="btn btn-primary btn-sm"
              data-test="dashboard-first-run-add-server"
              @click="showAddServer = true"
            >
              Add your first server
            </button>
            <router-link to="/repositories" class="btn btn-sm btn-ghost">Browse Registry</router-link>
            <button
              class="btn btn-sm btn-ghost"
              data-test="dashboard-first-run-overview"
              @click="selectOverview"
            >
              Go to Overview
            </button>
          </div>
        </div>
      </div>
      <!-- Hold the charts back until the server list has arrived: mounting
           UsageView first would fire a usage aggregate request and flash the
           "no usage data yet" card on a fresh install, only to be replaced by
           the CTA above a moment later. Both requests are issued together in
           onMounted, so this costs no extra round-trip. -->
      <div
        v-else-if="!serversFetchSettled && !serversStore.loaded"
        class="flex justify-center py-16"
        data-test="dashboard-usage-pending"
      >
        <span class="loading loading-spinner loading-lg"></span>
      </div>
      <Suspense v-else-if="usageEverActive">
        <UsageView />
        <template #fallback>
          <div class="flex justify-center py-16"><span class="loading loading-spinner loading-lg"></span></div>
        </template>
      </Suspense>
    </div>

    <!-- Overview: v-show (not v-if) so its state survives a switch to Usage and back (SC-006). -->
    <div v-show="activeView === 'overview'" class="space-y-6" data-test="dashboard-overview-panel">
    <!-- Hub Visualization -->
    <div class="grid grid-cols-1 lg:grid-cols-[280px_1fr_280px] gap-0 min-h-[520px] relative">

      <!-- Left Column: AI Agents / Clients -->
      <div class="flex flex-col justify-center items-center lg:items-end space-y-3 py-6 lg:pr-0">
        <h3 class="text-xs font-bold uppercase tracking-widest text-base-content/60 mb-1 w-full max-w-[260px] text-center lg:text-right">AI Agents</h3>

        <!-- Single big clients box. Audit F10: "Connected" is now the same fact
             /sessions shows — a live MCP session — instead of a flag the
             content-read-free connect listing can never set. Each live client
             links to its session row. -->
        <div class="card card-compact bg-base-100 shadow-sm border border-base-300 w-full max-w-[260px]" data-test="dashboard-agents-box">
          <div class="card-body py-3 px-4">
            <div v-if="liveClients.length > 0" class="mb-1">
              <div class="flex items-center gap-2 mb-1">
                <div class="w-2.5 h-2.5 rounded-full bg-success shrink-0"></div>
                <span class="text-xs font-bold uppercase tracking-wide text-base-content/60">Connected</span>
              </div>
              <router-link
                v-for="client in liveClients"
                :key="client.name"
                to="/sessions"
                class="flex items-baseline justify-between gap-2 text-sm link link-hover"
                :data-test="`dashboard-live-client-${client.name}`"
              >
                <span class="font-medium truncate">{{ client.name }}</span>
                <span class="text-xs opacity-50 shrink-0">{{ formatRelativeTime(client.lastActivity) }}</span>
              </router-link>
            </div>
            <div v-if="availableClientNames.length > 0">
              <div class="text-xs text-base-content/60 mt-1" data-test="dashboard-available-clients">
                Available: {{ availableClientNames.join(', ') }}
              </div>
            </div>
            <div v-if="liveClients.length === 0 && availableClientNames.length === 0" class="text-sm text-base-content/60 text-center py-2">
              No clients detected
            </div>
          </div>
        </div>

        <!-- Left Action Buttons -->
        <div class="flex flex-col gap-2 w-full max-w-[260px] pt-3">
          <button @click="showConnectModal = true" class="btn btn-primary btn-sm w-full gap-1">
            Connect Clients
          </button>
          <button @click="showAddServer = true" class="btn btn-secondary btn-outline btn-sm w-full gap-1">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12" />
            </svg>
            Import from client configs
          </button>
          <router-link to="/sessions" class="btn btn-ghost btn-sm w-full gap-1">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z" />
            </svg>
            Recent Sessions
          </router-link>
        </div>
      </div>

      <!-- Center Column: MCPProxy Hub -->
      <div class="flex flex-col items-center justify-center relative py-6">
        <!-- Connection lines: one fat horizontal line each side, big green running dot -->
        <svg class="absolute inset-0 w-full h-full pointer-events-none hidden lg:block overflow-visible" preserveAspectRatio="none">
          <!-- Left fat line (clients → hub) -->
          <line x1="0" y1="50%" x2="42%" y2="50%" stroke="var(--color-success)" stroke-width="4" stroke-opacity="0.25" />
          <!-- Right fat line (hub → servers) -->
          <line x1="58%" y1="50%" x2="100%" y2="50%" stroke="var(--color-success)" stroke-width="4" stroke-opacity="0.25" />

          <!-- Green dots travel once every 20s cycle, 4 dots total -->
          <!-- Left dot 1: clients → hub -->
          <circle r="7" fill="var(--color-success)" opacity="0">
            <animate attributeName="cx" values="0%;0%;42%;42%" keyTimes="0;0.05;0.15;1" dur="20s" repeatCount="indefinite" />
            <animate attributeName="cy" values="50%;50%;50%;50%" dur="20s" repeatCount="indefinite" />
            <animate attributeName="opacity" values="0;0.9;0.9;0;0" keyTimes="0;0.05;0.13;0.15;1" dur="20s" repeatCount="indefinite" />
          </circle>
          <!-- Left dot 2: clients → hub, staggered -->
          <circle r="6" fill="var(--color-success)" opacity="0">
            <animate attributeName="cx" values="0%;0%;42%;42%" keyTimes="0;0.1;0.2;1" dur="20s" repeatCount="indefinite" />
            <animate attributeName="cy" values="50%;50%;50%;50%" dur="20s" repeatCount="indefinite" />
            <animate attributeName="opacity" values="0;0.7;0.7;0;0" keyTimes="0;0.1;0.18;0.2;1" dur="20s" repeatCount="indefinite" />
          </circle>

          <!-- Right dot 1: servers → hub -->
          <circle r="7" fill="var(--color-success)" opacity="0">
            <animate attributeName="cx" values="100%;100%;58%;58%" keyTimes="0;0.07;0.17;1" dur="20s" repeatCount="indefinite" />
            <animate attributeName="cy" values="50%;50%;50%;50%" dur="20s" repeatCount="indefinite" />
            <animate attributeName="opacity" values="0;0.9;0.9;0;0" keyTimes="0;0.07;0.15;0.17;1" dur="20s" repeatCount="indefinite" />
          </circle>
          <!-- Right dot 2: servers → hub, staggered -->
          <circle r="6" fill="var(--color-success)" opacity="0">
            <animate attributeName="cx" values="100%;100%;58%;58%" keyTimes="0;0.12;0.22;1" dur="20s" repeatCount="indefinite" />
            <animate attributeName="cy" values="50%;50%;50%;50%" dur="20s" repeatCount="indefinite" />
            <animate attributeName="opacity" values="0;0.7;0.7;0;0" keyTimes="0;0.12;0.2;0.22;1" dur="20s" repeatCount="indefinite" />
          </circle>

          <!-- Static green dots at hub connection points -->
          <circle cx="42%" cy="50%" r="5" fill="var(--color-success)" opacity="0.7" />
          <circle cx="58%" cy="50%" r="5" fill="var(--color-success)" opacity="0.7" />
        </svg>

        <!--
          Token savings badge (above hub). This is the product's headline claim
          and it used to be a chip with no tooltip, no destination and no window
          — so when it read the same before and after 16 tool calls, there was
          nothing on screen to explain why (audit finding F23, #1046). It is a
          PER-REQUEST structural estimate over the current tool catalog; it says
          so, explains itself on hover, and opens the breakdown that derives it.
        -->
        <div class="mb-6 z-10">
          <button
            v-if="tokenSavingsData && tokenSavingsData.saved_tokens_percentage > 0"
            type="button"
            data-test="dashboard-token-savings-chip"
            class="badge badge-lg gap-1 px-4 py-3 bg-primary/10 text-primary border-primary/30 hover:bg-primary/20 transition-colors cursor-pointer"
            :title="tokensSavedExplainer"
            @click="openTokenSavingsDetails"
          >
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 7h8m0 0v8m0-8l-8 8-4-4-6 6" />
            </svg>
            <span class="text-lg font-bold">{{ tokenSavingsData.saved_tokens_percentage >= 99.995 ? '99.99' : tokenSavingsData.saved_tokens_percentage >= 10 ? tokenSavingsData.saved_tokens_percentage.toFixed(1) : tokenSavingsData.saved_tokens_percentage.toFixed(0) }}%</span>
            <span class="text-xs font-medium">smaller tool context per request</span>
          </button>
        </div>

        <!-- Logo Hub -->
        <div class="relative z-10">
          <div class="w-36 h-36 flex items-center justify-center transition-all duration-500"
            :class="systemStore.isRunning ? 'hub-glow' : ''">
            <img :src="logoSvg" alt="MCPProxy" class="w-28 h-28" />
          </div>
          <div class="text-center mt-1 select-none">
            <div class="text-xs font-bold uppercase tracking-wider" :class="systemStore.isRunning ? 'text-primary' : 'text-base-content/60'">
              MCPProxy
            </div>
            <div class="text-xs font-medium" :class="systemStore.isRunning ? 'text-success' : 'text-error'">
              {{ systemStore.isRunning ? 'active' : 'stopped' }}
            </div>
            <div v-if="uptime" class="text-[10px] text-base-content/60">{{ uptime }}</div>
          </div>
        </div>

        <!-- Security Status -->
        <div class="z-10 w-full max-w-[300px] space-y-2 mt-4">
          <!-- Docker Isolation (hidden until status has been fetched to avoid
               flashing a false "disabled" state on initial page load) -->
          <div v-if="dockerStatus" class="flex items-center gap-2 text-xs px-3 py-2 rounded-lg"
               :class="dockerStatus.available ? 'bg-success/10 text-success' : 'bg-warning/10 text-warning'">
            <svg class="w-4 h-4 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                    d="M20 7l-8-4-8 4m16 0l-8 4m8-4v10l-8 4m0-10L4 7m8 4v10M4 7v10l8 4" />
            </svg>
            <span v-if="dockerStatus.available" class="font-medium">Docker isolation active</span>
            <span v-else class="font-medium">Docker isolation disabled — enable Docker to protect your system</span>
          </div>

          <!-- Quarantine (hidden until config fetch completes) -->
          <div v-if="quarantineEnabled !== null" class="flex items-center gap-2 text-xs px-3 py-2 rounded-lg"
               :class="quarantineEnabled ? 'bg-success/10 text-success' : 'bg-warning/10 text-warning'">
            <svg class="w-4 h-4 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                    d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
            </svg>
            <span v-if="quarantineEnabled" class="font-medium">Quarantine protection active</span>
            <span v-else class="font-medium">Quarantine disabled — enable to prevent prompt injection attacks</span>
          </div>

          <!-- Activity Log link -->
          <router-link to="/activity" class="flex items-center gap-2 text-xs px-3 py-2 rounded-lg bg-base-100/50 border border-base-300 hover:bg-base-200 transition-colors">
            <svg class="w-4 h-4 shrink-0 opacity-60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z" />
            </svg>
            <span class="font-medium opacity-70">Activity Log</span>
          </router-link>
        </div>
      </div>

      <!-- Right Column: Upstream Servers -->
      <div class="flex flex-col justify-center items-center lg:items-start space-y-3 py-6 lg:pl-4">
        <h3 class="text-xs font-bold uppercase tracking-widest text-base-content/60 mb-1 w-full max-w-[240px] text-center lg:text-left">Upstream Servers</h3>

        <!-- Connected servers card -->
        <router-link to="/servers" class="card card-compact bg-base-100 shadow-sm border border-base-300 w-full max-w-[240px] hover:shadow-md transition-shadow">
          <div class="card-body py-3 px-4">
            <div class="flex items-center gap-2">
              <div class="w-2.5 h-2.5 rounded-full bg-success shrink-0"></div>
              <span class="text-2xl font-bold leading-none">{{ serversStore.serverCount.connected }}</span>
              <span class="text-sm opacity-60">connected</span>
            </div>
            <div class="text-sm mt-1">
              <span class="font-bold">{{ serversStore.totalTools }}</span>
              <span class="opacity-60"> tools available</span>
            </div>
            <div
              v-if="disabledCount > 0"
              class="text-xs text-base-content/60 mt-0.5"
            >
              {{ disabledCount }} disabled
            </div>
          </div>
        </router-link>

        <!-- Quarantine card -->
        <router-link
          v-if="serversStore.serverCount.quarantined > 0"
          to="/servers"
          class="card card-compact bg-warning/10 border border-warning/30 w-full max-w-[240px] hover:shadow-md transition-shadow"
        >
          <div class="card-body py-3 px-4">
            <div class="flex items-center gap-2">
              <svg class="w-4 h-4 text-warning shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z" />
              </svg>
              <span class="text-lg font-bold text-warning leading-none">{{ serversStore.serverCount.quarantined }}</span>
              <span class="text-sm">in quarantine</span>
            </div>
          </div>
        </router-link>

        <!-- Right Action Buttons -->
        <div class="flex flex-col gap-2 w-full max-w-[240px] pt-3">
          <button @click="showAddServer = true" class="btn btn-primary btn-sm w-full gap-1">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6v6m0 0v6m0-6h6m-6 0H6" />
            </svg>
            Add Server
          </button>
          <router-link to="/repositories" class="btn btn-ghost btn-sm w-full gap-1">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
            </svg>
            Browse Registry
          </router-link>
          <router-link to="/security" class="btn btn-ghost btn-sm w-full gap-1">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
            </svg>
            Security Scan
            <span v-if="securityScannerLoaded && securityTotalScans === 0" class="badge badge-ghost badge-xs ml-1">Run first scan</span>
            <span v-else-if="securityTotalFindings > 0" class="badge badge-warning badge-xs ml-1">{{ securityTotalFindings }} issue{{ securityTotalFindings === 1 ? '' : 's' }}</span>
          </router-link>
        </div>
      </div>
    </div>

    <!-- Token Savings Collapsible Detail — where the headline chip lands. -->
    <div
      v-if="tokenSavingsData"
      ref="tokenSavingsDetails"
      data-test="dashboard-token-savings-details"
      class="collapse collapse-arrow bg-base-100 shadow-sm border border-base-300"
    >
      <input type="checkbox" v-model="tokenDetailsOpen" aria-label="Show token savings details" />
      <div class="collapse-title font-medium flex items-center gap-3">
        <svg class="w-5 h-5 text-success" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 7h8m0 0v8m0-8l-8 8-4-4-6 6" />
        </svg>
        Token Savings Details
        <span class="badge badge-success badge-sm ml-auto">{{ formatNumber(tokenSavingsData.saved_tokens) }} saved</span>
      </div>
      <div class="collapse-content">
        <div class="grid grid-cols-1 lg:grid-cols-2 gap-6 pt-2">
          <!-- Token Savings Stats -->
          <div>
            <div class="grid grid-cols-3 gap-4">
              <div :title="tokensSavedExplainer">
                <div class="text-sm opacity-60">Tokens Saved / request</div>
                <div class="text-2xl font-bold text-success">{{ formatNumber(tokenSavingsData.saved_tokens) }}</div>
                <div class="text-xs opacity-60">{{ tokenSavingsData.saved_tokens_percentage.toFixed(1) }}% reduction</div>
              </div>
              <div>
                <div class="text-sm opacity-60">Full Tool List</div>
                <div class="text-xl font-bold">{{ formatNumber(tokenSavingsData.total_server_tool_list_size) }}</div>
                <div class="text-xs opacity-60">All servers</div>
              </div>
              <div>
                <div class="text-sm opacity-60">Typical Query</div>
                <div class="text-xl font-bold">{{ formatNumber(tokenSavingsData.average_query_result_size) }}</div>
                <div class="text-xs opacity-60">BM25 result</div>
              </div>
            </div>
          </div>

          <!-- Token Distribution Chart -->
          <div>
            <div class="flex items-center justify-center">
              <div class="w-48 h-48">
                <TokenPieChart v-if="pieChartSegments.length > 0" :data="pieChartSegments" />
              </div>
            </div>
            <div class="mt-3 space-y-1.5 max-h-32 overflow-y-auto">
              <div
                v-for="(segment, index) in pieChartSegments"
                :key="index"
                class="flex items-center justify-between text-sm"
              >
                <div class="flex items-center space-x-2 min-w-0">
                  <div class="w-2.5 h-2.5 rounded shrink-0" :style="{ backgroundColor: segment.color }"></div>
                  <span class="truncate text-xs">{{ segment.name }}</span>
                </div>
                <div class="flex items-center space-x-2 shrink-0">
                  <span class="font-mono text-xs">{{ formatNumber(segment.value) }}</span>
                  <span class="text-xs text-base-content/60">({{ segment.percentage.toFixed(1) }}%)</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Hints Panel (Bottom of Page) -->
    <CollapsibleHintsPanel :hints="dashboardHints" />
    </div>
    <!-- /Overview panel -->

    <!-- Modals -->
    <ConnectModal :show="showConnectModal" @close="showConnectModal = false" />
    <AddServerModal :show="showAddServer" @close="showAddServer = false" @added="handleServerAdded" />
    <OnboardingWizard :show="onboardingStore.wizardOpen" @close="onboardingStore.closeWizard" />
  </div>
</template>

<script setup lang="ts">
import { serverDetailPath } from '@/utils/serverRoute'
import { computed, nextTick, ref, watch, onMounted, onUnmounted, defineAsyncComponent } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import { useSecurityScannerStatus, refreshSecurityScannerStatus } from '@/composables/useSecurityScannerStatus'
import api from '@/services/api'
import logoSvg from '@/assets/logo.svg'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import TelemetryBanner from '@/components/TelemetryBanner.vue'
import UpdateBanner from '@/components/UpdateBanner.vue'
import TokenPieChart from '@/components/TokenPieChart.vue'
import ConnectModal from '@/components/ConnectModal.vue'
import AddServerModal from '@/components/AddServerModal.vue'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import { useOnboardingStore } from '@/stores/onboarding'
import type { Hint } from '@/components/CollapsibleHintsPanel.vue'
import type { ClientStatus } from '@/types'
import { liveClientsFromSessions } from '@/utils/sessionLabel'
import { formatRelativeTime } from '@/utils/activity'

// Usage view is code-split so chart.js + the usage fetch stay out of the
// Dashboard's first-paint critical path (Spec 069 SC-004).
const UsageView = defineAsyncComponent(() => import('@/views/Usage.vue'))

const serversStore = useServersStore()
const systemStore = useSystemStore()
const onboardingStore = useOnboardingStore()

// Usage ↔ Overview switcher state (Spec 069 T016). `usageEverActive` gates the
// first mount; `activeView` then toggles via v-show so both panels keep state.
//
// The active panel comes from the route (`meta.dashboardView`): `/` and
// `/usage` land on the analytics panel, `/overview` on the hub overview.
// Clicking a tab rewrites the URL (replace, so the switcher does not pile up
// history entries) — the routes share this component, so switching never
// remounts the Dashboard and both panels keep their state.
type DashboardView = 'overview' | 'usage'

const route = useRoute()
const router = useRouter()

function routeView(): DashboardView {
  return (route.meta?.dashboardView as DashboardView | undefined) === 'overview' ? 'overview' : 'usage'
}

const activeView = ref<DashboardView>(routeView())
const usageEverActive = ref(activeView.value === 'usage')

// The panel a navigation is currently heading for. `route` only reflects a
// *confirmed* navigation, so comparing against it alone loses a fast second tab
// click: it would still see the pre-navigation route, decide it has nothing to
// do, and then be overwritten when the first navigation lands. Tracking the
// in-flight target makes the newest click win — the router cancels the
// superseded navigation for us.
//
// `navSeq` identifies which navigation owns the marker. Comparing the panel
// value alone is not enough: two navigations to the same panel (Overview →
// Usage → Overview) share a value, so the older one's settlement would clear a
// marker still owned by the newer one, and the next click would again compare
// against the stale current route and be swallowed.
let pendingView: DashboardView | null = null
let navSeq = 0

function selectView(view: DashboardView) {
  if (view === 'usage') {
    usageEverActive.value = true
  }
  activeView.value = view
  // `/` is also a usage route — don't rewrite it to `/usage` needlessly.
  if ((pendingView ?? routeView()) === view) {
    return
  }
  const seq = ++navSeq
  pendingView = view
  const targetRoute = view === 'usage' ? 'usage' : 'dashboard-overview'
  void router.replace({ name: targetRoute }).finally(() => {
    // Only the newest navigation clears the marker; a superseded one must not.
    if (seq === navSeq) {
      pendingView = null
    }
  })
}
const selectUsage = () => selectView('usage')
const selectOverview = () => selectView('overview')

// Keep the panel in sync when the route changes underneath us (sidebar link,
// browser back/forward, a pasted deep link).
watch(() => route.meta?.dashboardView, () => {
  const view = routeView()
  if (view === 'usage') {
    usageEverActive.value = true
  }
  activeView.value = view
})

// Modal state
const showConnectModal = ref(false)
const showAddServer = ref(false)

// Auto-refresh interval
let refreshInterval: ReturnType<typeof setInterval> | null = null

// --- Client statuses ---
const clientStatuses = ref<ClientStatus[]>([])

// Audit F10: "Connected" means a live MCP session, the same fact /sessions and
// the macOS tray render. `ClientStatus.connected` cannot be used here — the
// stat-only connect listing leaves it false for every client by design.
const liveClients = computed(() => liveClientsFromSessions(recentSessions.value))

// Everything else installed on this machine that COULD be connected. A client
// currently holding a live session is not repeated here.
const availableClientNames = computed(() => {
  const live = new Set(liveClients.value.map(c => c.name))
  return clientStatuses.value
    .filter(c => c.supported && c.exists)
    .map(c => c.name)
    .filter(name => !live.has(name))
})

function clientIcon(client: ClientStatus): string {
  const iconMap: Record<string, string> = {
    'claude-desktop': '\u2728',
    'claude-code': '\u{1F4BB}',
    'cursor': '\u{1F4DD}',
    'vscode': '\u{1F4D0}',
    'windsurf': '\u{1F3C4}',
    'zed': '\u26A1',
    'cline': '\u{1F916}',
    'continue': '\u27A1\uFE0F',
  }
  return iconMap[client.id] || client.icon || '\u{1F527}'
}

const loadClientStatuses = async () => {
  try {
    const response = await api.getConnectStatus()
    if (response.success && response.data) {
      clientStatuses.value = Array.isArray(response.data) ? response.data : []
    }
  } catch {
    // Connect endpoint may not exist yet - graceful degradation
  }
}

// --- Activity count ---
const activityCount = ref(0)

const loadActivitySummary = async () => {
  try {
    const response = await api.getActivitySummary('24h')
    if (response.success && response.data) {
      activityCount.value = response.data.total_count || 0
    }
  } catch {
    // Silently fail
  }
}

// --- Security status ---
const dockerStatus = ref<{available: boolean, version?: string} | null>(null)
// quarantineEnabled is null until the config fetch completes so the template
// can skip rendering the "Quarantine disabled" warning on initial load. A
// plain `false` default would briefly display the warning before data arrives.
const quarantineEnabled = ref<boolean | null>(null)

// Security scanner totals for the "Security Scan" dashboard chip (F-12).
// We reuse the shared composable so we don't double-fetch /security/overview.
const {
  totalFindings: securityTotalFindings,
  totalScans: securityTotalScans,
  loaded: securityScannerLoaded,
} = useSecurityScannerStatus()

const loadSecurityStatus = async () => {
  try {
    // Docker status from dedicated endpoint. The badge reads "active" only when
    // Docker isolation is genuinely in effect: the user enabled it AND a real
    // Docker daemon is reachable. docker_available now reflects a genuine daemon
    // probe (MCP-2478) — we must NOT fake it from connected stdio servers, which
    // do not imply isolation is on.
    const dockerResponse = await api.getDockerStatus()
    if (dockerResponse.success && dockerResponse.data) {
      const daemonAvailable = dockerResponse.data.docker_available ?? false
      const isolationEnabled = dockerResponse.data.isolation_enabled ?? false
      dockerStatus.value = { available: daemonAvailable && isolationEnabled }
    }
  } catch {
    // Docker endpoint may not exist - treat as unavailable
    dockerStatus.value = { available: false }
  }

  try {
    // Quarantine status from config endpoint
    const configResponse = await api.getConfig()
    if (configResponse.success && configResponse.data) {
      const cfg = configResponse.data.config
      // quarantine_enabled defaults to true when omitted (nil)
      quarantineEnabled.value = cfg?.quarantine_enabled ?? true
    }
  } catch {
    // Fallback: assume enabled (safe default)
    quarantineEnabled.value = true
  }
}

// --- Uptime ---
// Track when we first saw the server running via SSE
const serverFirstSeen = ref<number>(0)

watch(() => systemStore.isRunning, (running: boolean) => {
  if (running && !serverFirstSeen.value) {
    serverFirstSeen.value = Date.now()
  }
}, { immediate: true })

// Audit F36: uptime used to be measured from the moment THIS PAGE first saw the
// core running, so every reload reset it and the hub read "just started"
// indefinitely — a stuck state, not a fact. The core now reports its own
// start time (`started_at` on /status and every SSE status frame); the
// page-local first-seen fallback is kept only for older cores that omit it.
const uptime = computed(() => {
  if (!systemStore.isRunning) return ''

  const startedAt = systemStore.status?.started_at
  if (startedAt && startedAt > 0) {
    return formatUptime(Math.floor(Date.now() / 1000) - startedAt)
  }

  if (serverFirstSeen.value) {
    return formatUptime(Math.floor((Date.now() - serverFirstSeen.value) / 1000))
  }

  return 'online'
})

function formatUptime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return 'online'
  if (seconds < 60) return `${Math.max(seconds, 1)}s uptime`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m uptime`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h uptime`
  return `${Math.floor(seconds / 86400)}d uptime`
}

// --- Recent Sessions ---
const recentSessions = ref<any[]>([])

const loadSessions = async () => {
  try {
    // status=active + a roomier limit (audit F10): an unfiltered top-5 can be
    // filled entirely by closed sessions and hide every live client.
    const response = await api.getSessions(25, 'active')
    if (response.success && response.data) {
      recentSessions.value = response.data.sessions || []
    }
  } catch {
    // Silently fail
  }
}

// --- Token Savings ---
const tokenSavingsData = ref<any>(null)

const loadTokenSavings = async () => {
  try {
    const response = await api.getTokenStats()
    if (response.success && response.data) {
      tokenSavingsData.value = response.data
    }
  } catch {
    // Silently fail
  }
}

// --- First run (no servers configured) ---
// Two different questions, deliberately answered by two different flags:
//
// `serversFetchSettled` — has our own initial request finished, successfully or
// not? Together with `loaded` it gates the chart mount below, so a failed fetch
// falls through to the usage panel rather than spinning forever, and an
// authoritative list arriving by SSE first clears the spinner immediately.
//
// `serversStore.loaded` — has a server list ever arrived successfully? Only
// that justifies telling the user they have no servers. It cannot be inferred
// from `loading.error`: that field is shared, other components (App.vue) and
// silent background refreshes write it concurrently, and a success never clears
// it — so an unrelated failure would suppress the CTA, and a later successful
// refresh could never bring it back.
const serversFetchSettled = ref(false)
const showFirstRunCta = computed(
  () => serversStore.loaded && serversStore.serverCount.total === 0
)

// --- Token savings: the headline claim, and the panel that derives it --------
//
// F23 (#1046). The figure is a STRUCTURAL estimate over the current tool
// catalog, not a running total of savings realised so far — which is why it does
// not move when calls are made. That was invisible: no tooltip, no window, no
// drill-down. The chip now carries this text and opens the breakdown below.
const tokensSavedExplainer =
  'Estimated per-request saving: the tokens it would take to put every ' +
  'upstream tool definition in your agent\'s context, minus what retrieve_tools ' +
  'returns for one query. It is a property of your current tool catalog, so it ' +
  'changes when you add, remove or reconnect servers — not with each call.'

const tokenDetailsOpen = ref(false)
const tokenSavingsDetails = ref<HTMLElement | null>(null)

const openTokenSavingsDetails = () => {
  tokenDetailsOpen.value = true
  // Let the collapse expand before scrolling, or the target moves under us.
  nextTick(() => {
    tokenSavingsDetails.value?.scrollIntoView({ behavior: 'smooth', block: 'center' })
  })
}

// --- Disabled server count ---
//
// Disabled and quarantined must be MUTUALLY EXCLUSIVE here (audit finding F27,
// #1046): the rail shows both, and a server that is both was counted twice,
// reading as two problems needing two decisions. The old formula
// (total − connected − quarantined) was wrong in the other direction too — an
// ENABLED server that simply cannot connect was reported as "disabled", which
// hides the actual fault behind an administrative word. Ask the question
// directly instead: switched off, and not already spoken for by quarantine.
const disabledCount = computed(() => serversStore.serverCount.disabled)

// --- Servers needing attention ---
// Only show servers that have actionable problems, not transient states like "Connecting..."
const serversNeedingAttention = computed(() => {
  return serversStore.servers.filter(server => {
    if (!server.health) return false
    if (server.health.admin_state === 'disabled' || server.health.admin_state === 'quarantined') return false
    // Only unhealthy servers with an actionable remedy need attention
    // Degraded is for transient states (connecting) — not worth alerting
    if (server.health.level === 'unhealthy') return true
    // Degraded only if there's a specific action the user should take
    if (server.health.level === 'degraded' && server.health.action) return true
    return false
  })
})

// --- Quarantine pending tools ---
interface PendingToolEntry {
  serverName: string
  count: number
}
const pendingToolsByServer = ref<PendingToolEntry[]>([])

const serversWithPendingTools = computed(() =>
  pendingToolsByServer.value.filter(entry => entry.count > 0)
)

const totalPendingTools = computed(() =>
  serversWithPendingTools.value.reduce((sum, entry) => sum + entry.count, 0)
)

const loadPendingTools = async () => {
  try {
    const enabledServers = serversStore.servers.filter(s => s.enabled)
    const results: PendingToolEntry[] = []

    const promises = enabledServers.map(async (server) => {
      try {
        const response = await api.getToolApprovals(server.name)
        if (response.success && response.data?.tools) {
          const pendingCount = response.data.tools.filter(
            (t: any) => t.status === 'pending' || t.status === 'changed'
          ).length
          if (pendingCount > 0) {
            results.push({ serverName: server.name, count: pendingCount })
          }
        }
      } catch {
        // Silently ignore per-server failures
      }
    })

    await Promise.all(promises)
    results.sort((a, b) => b.count - a.count)
    pendingToolsByServer.value = results
  } catch {
    // Silently fail
  }
}

// --- Server actions ---
const triggerServerAction = async (serverName: string, action: string) => {
  try {
    switch (action) {
      case 'oauth_login':
        await serversStore.triggerOAuthLogin(serverName)
        systemStore.addToast({ type: 'success', title: 'OAuth Login', message: `OAuth login initiated for ${serverName}` })
        break
      case 'restart':
        await serversStore.restartServer(serverName)
        systemStore.addToast({ type: 'success', title: 'Server Restarted', message: `${serverName} is restarting` })
        break
      case 'enable':
        await serversStore.enableServer(serverName)
        systemStore.addToast({ type: 'success', title: 'Server Enabled', message: `${serverName} has been enabled` })
        break
      default:
        console.warn(`Unknown action: ${action}`)
    }
    setTimeout(() => serversStore.fetchServers(), 1000)
  } catch (error) {
    systemStore.addToast({
      type: 'error',
      title: 'Action Failed',
      message: error instanceof Error ? error.message : 'Unknown error',
    })
  }
}

// --- Add Server handler ---
const handleServerAdded = () => {
  showAddServer.value = false
  serversStore.fetchServers()
  systemStore.addToast({ type: 'success', title: 'Server Added', message: 'New server has been added successfully' })
}

// --- Formatters ---
const formatNumber = (num: number): string => {
  if (num >= 1000000) return `${(num / 1000000).toFixed(1)}M`
  if (num >= 1000) return `${(num / 1000).toFixed(1)}K`
  return num.toString()
}

// --- Pie chart ---
const pieChartColors = [
  '#3b82f6', '#10b981', '#f59e0b', '#ec4899', '#8b5cf6',
  '#06b6d4', '#ef4444', '#14b8a6', '#f97316', '#a855f7',
  '#6366f1', '#84cc16', '#f43f5e', '#0ea5e9', '#22c55e', '#eab308',
]

const pieChartSegments = computed(() => {
  if (!tokenSavingsData.value?.per_server_tool_list_sizes) return []

  const sizes = tokenSavingsData.value.per_server_tool_list_sizes
  const entries = Object.entries(sizes).sort((a, b) => (b[1] as number) - (a[1] as number))
  const total = entries.reduce((sum, [, value]) => sum + (value as number), 0)

  let offset = 0
  return entries.map(([name, value], index) => {
    const numValue = value as number
    const percentage = total > 0 ? (numValue / total) * 100 : 0
    const segment = {
      name,
      value: numValue,
      percentage,
      offset,
      color: pieChartColors[index % pieChartColors.length],
    }
    offset += percentage
    return segment
  })
})

// --- Dashboard hints ---
const dashboardHints = computed<Hint[]>(() => {
  const hints: Hint[] = []

  hints.push({
    icon: '\u{1F4A1}',
    title: 'CLI Commands for Managing MCPProxy',
    description: 'Useful commands for working with MCPProxy',
    sections: [
      {
        title: 'View all servers',
        codeBlock: {
          language: 'bash',
          code: `# List all upstream servers\nmcpproxy upstream list`,
        },
      },
      {
        title: 'Search for tools',
        codeBlock: {
          language: 'bash',
          code: `# Search across all server tools\nmcpproxy tools search "your query"\n\n# List tools from specific server\nmcpproxy tools list --server=server-name`,
        },
      },
      {
        title: 'Connect to AI clients',
        codeBlock: {
          language: 'bash',
          code: `# Register MCPProxy in Claude Desktop\nmcpproxy connect claude-desktop\n\n# List all detected clients\nmcpproxy connect --list`,
        },
      },
    ],
  })

  hints.push({
    icon: '\u{1F916}',
    title: 'Use MCPProxy with LLM Agents',
    description: 'Connect Claude or other LLM agents to MCPProxy',
    sections: [
      {
        title: 'Example LLM prompts',
        list: [
          'Search for tools related to GitHub issues across all my MCP servers',
          'List all available MCP servers and their connection status',
          'Add a new MCP server from npm package @modelcontextprotocol/server-filesystem',
          'Show me statistics about which tools are being used most frequently',
        ],
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
}`,
        },
      },
    ],
  })

  return hints
})

// --- Lifecycle ---
onMounted(() => {
  loadClientStatuses()
  loadTokenSavings()
  loadActivitySummary()
  loadSessions()
  loadSecurityStatus()
  // Populate security scanner totals for the Security Scan chip (F-12).
  void refreshSecurityScannerStatus()
  serversStore.fetchServers().then(() => {
    serversFetchSettled.value = true
    loadPendingTools()
  })

  // Auto-refresh every 30 seconds
  refreshInterval = setInterval(() => {
    loadClientStatuses()
    loadTokenSavings()
    loadActivitySummary()
    loadSessions()
    loadSecurityStatus()
    void refreshSecurityScannerStatus()
    loadPendingTools()
  }, 30000)

  systemStore.connectEventSource()
  // NOTE: no second fetchServers() here — it duplicated the request issued
  // above and, being unsequenced against it, made the first-run gate depend on
  // which of the two responses landed last.

  // Adaptive onboarding wizard (Spec 046): auto-show on first Web UI load
  // when the user has not yet engaged with the wizard and at least one
  // predicate (any client connected? any server configured?) is false.
  onboardingStore.fetchState().then((state) => {
    if (state?.should_show_wizard) {
      onboardingStore.openWizard()
    }
  })
})

onUnmounted(() => {
  if (refreshInterval) {
    clearInterval(refreshInterval)
    refreshInterval = null
  }
})
</script>

<style scoped>
/* Hub glow animation when MCPProxy is active — uses drop-shadow to follow the logo shape */
@keyframes hubGlow {
  0%, 100% {
    filter: drop-shadow(0 4px 8px color-mix(in oklch, var(--color-primary) 15%, transparent)) drop-shadow(0 2px 4px color-mix(in oklch, var(--color-primary) 10%, transparent));
  }
  50% {
    filter: drop-shadow(0 6px 16px color-mix(in oklch, var(--color-primary) 30%, transparent)) drop-shadow(0 3px 8px color-mix(in oklch, var(--color-primary) 15%, transparent));
  }
}

.hub-glow {
  animation: hubGlow 3s ease-in-out infinite;
}
</style>
