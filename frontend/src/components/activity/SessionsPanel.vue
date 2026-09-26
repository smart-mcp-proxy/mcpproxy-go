<!--
  Spec 109-k (activity-scope-filters), T118: the Activity page's "Sessions"
  view (url-filter-contract.md `view` -> REST: `GET /sessions`, no `/activity`
  request). This is Sessions.vue's table + info card, lifted out into a
  standalone component so both the (now-redirecting, see router/index.ts)
  `/sessions` route and `/activity?view=sessions` render the same content —
  the audit found the latter had NOTHING: switching to "Sessions" left the
  page showing the (filtered-to-nothing) activity table instead of a session
  list.

  The parent (Activity.vue) already polls `GET /sessions` for its own
  session-name join (`loadSessions()`/`sessionsRaw`), so this component takes
  the list as a prop rather than fetching its own copy — one request, not two
  racing each other.
-->
<template>
  <div class="space-y-6">
    <div class="card bg-base-100 shadow-md">
      <div class="card-body">
        <div v-if="sessions.length === 0" class="text-center py-12 text-base-content/60" data-test="sessions-empty">
          <svg class="w-16 h-16 mx-auto mb-4 opacity-30" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z" />
          </svg>
          <p class="text-lg">No sessions found</p>
          <p class="text-sm mt-1">Sessions will appear here when MCP clients connect</p>
        </div>

        <div v-else class="overflow-x-auto" data-test="sessions-table">
          <table class="table">
            <thead>
              <tr>
                <th>Session ID</th>
                <th>Client</th>
                <th>Status</th>
                <th>Capabilities</th>
                <th>Tool Calls</th>
                <th>Tokens</th>
                <th>Started</th>
                <th>Last Active</th>
                <th class="whitespace-nowrap">Actions</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="session in sessions" :key="session.id" data-test="sessions-row">
                <td>
                  <code class="text-xs bg-base-200 px-2 py-1 rounded" :title="session.id">
                    {{ session.id.substring(0, 12) }}...
                  </code>
                </td>
                <td>
                  <div class="font-medium">{{ session.client_name || 'Unknown' }}</div>
                  <div v-if="session.client_version" class="text-xs text-base-content/60">
                    v{{ session.client_version }}
                  </div>
                </td>
                <td>
                  <div class="badge" :class="session.status === 'active' ? 'badge-success' : 'badge-neutral'">
                    {{ session.status === 'active' ? 'Active' : 'Closed' }}
                  </div>
                </td>
                <td>
                  <div class="flex flex-wrap gap-1">
                    <span v-if="session.has_roots" class="badge badge-sm badge-info" title="Client supports roots capability">Roots</span>
                    <span v-if="session.has_sampling" class="badge badge-sm badge-info" title="Client supports sampling capability">Sampling</span>
                    <span
                      v-if="session.experimental && session.experimental.length > 0"
                      class="badge badge-sm badge-warning"
                      :title="`Experimental features: ${session.experimental.join(', ')}`"
                    >
                      Experimental ({{ session.experimental.length }})
                    </span>
                    <span v-if="!session.has_roots && !session.has_sampling && (!session.experimental || session.experimental.length === 0)" class="text-xs text-base-content/40">
                      None
                    </span>
                  </div>
                </td>
                <td><span class="font-mono">{{ session.tool_call_count }}</span></td>
                <td><span class="font-mono text-sm" title="Total tokens used in this session">{{ session.total_tokens.toLocaleString() }}</span></td>
                <td>
                  <div class="text-sm">{{ formatTimestamp(session.start_time) }}</div>
                  <div class="text-xs text-base-content/60">{{ formatRelativeTime(session.start_time) }}</div>
                </td>
                <td>
                  <div class="text-sm">{{ formatTimestamp(session.last_activity) }}</div>
                  <div class="text-xs text-base-content/60">{{ formatRelativeTime(session.last_activity) }}</div>
                </td>
                <td class="whitespace-nowrap">
                  <!-- Link map: "Activity row (Sessions view)" -> session ->
                       `/activity?view=calls&session=<work_session_id>` — the
                       WORK session (Spec 082), falling back to the transport
                       id so a pre-082 row still resolves (toRest() routes
                       either one by the `ws-` prefix rule). -->
                  <router-link
                    :to="scopeQuery.linkTo('activity', { view: 'calls', session: session.work_session_id || session.id })"
                    class="btn btn-xs btn-primary whitespace-nowrap"
                    title="View activity for this session"
                    data-test="session-view-activity"
                  >
                    View Activity
                  </router-link>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div v-if="sessions.length > 0" class="text-sm text-base-content/60 mt-4 text-center">
          Showing {{ sessions.length }} most recent sessions
        </div>
      </div>
    </div>

    <div class="card bg-base-100 shadow-md">
      <div class="card-body">
        <h2 class="card-title text-lg">About MCP Sessions</h2>
        <div class="prose prose-sm max-w-none">
          <p>
            MCP sessions represent individual connections from AI clients (like Claude Code) to MCPProxy.
            Each session tracks:
          </p>
          <ul class="text-sm space-y-1 mt-2">
            <li><strong>Tool Calls:</strong> Number of tool invocations made during the session</li>
            <li><strong>Token Usage:</strong> Total tokens consumed across all tool calls</li>
            <li><strong>Duration:</strong> Time from connection to disconnection</li>
          </ul>
          <p class="mt-2 text-xs text-base-content/60">
            Sessions are retained for the 100 most recent connections.
          </p>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { MCPSession } from '@/types/api'
import { formatDateTime } from '@/utils/datetime'
import { useScopeQuery } from '@/composables/useScopeQuery'

defineProps<{ sessions: MCPSession[] }>()

const scopeQuery = useScopeQuery('activity')

const formatTimestamp = (timestamp: string): string => formatDateTime(timestamp)

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
</script>
