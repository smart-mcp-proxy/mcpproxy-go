<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Agent Tokens</h1>
        <p class="text-base-content/70 mt-1">Create and manage scoped API tokens for AI agents and automation</p>
      </div>
      <div class="flex gap-2">
        <button
          @click="refreshTokens"
          :disabled="loading"
          class="btn btn-outline"
        >
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
          <span v-if="loading" class="loading loading-spinner loading-sm"></span>
          {{ loading ? 'Refreshing...' : 'Refresh' }}
        </button>
        <button
          @click="openCreateDialog"
          class="btn btn-primary"
        >
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
          </svg>
          Create Token
        </button>
      </div>
    </div>

    <!-- Spec 134 FR-016: the profile/token filters this tab applies, as
         removable chips ("Viewing as <name>"); a carried client is marked
         "not applied on this page". View as sits in the Clients filter row. -->
    <ScopeChips v-if="scopeQuery" page="tokens" :scope-query="scopeQuery" :unavailable="scopeUnavailable" />

    <!-- Summary Stats — clickable cards drive the token filter (issue #436) -->
    <div class="stats shadow bg-base-100 w-full">
      <button
        type="button"
        data-test="kpi-card-total"
        :class="['stat text-left transition-colors cursor-pointer hover:bg-base-200/60', tokenFilter === 'all' ? 'bg-base-200 ring-2 ring-inset ring-primary/40' : '']"
        :aria-pressed="tokenFilter === 'all'"
        @click="tokenFilter = 'all'"
      >
        <div class="stat-title">Total Tokens</div>
        <div class="stat-value">{{ tokens.length }}</div>
        <div class="stat-desc">All agent tokens</div>
      </button>
      <button
        type="button"
        data-test="kpi-card-active"
        :class="['stat text-left transition-colors cursor-pointer hover:bg-base-200/60', tokenFilter === 'active' ? 'bg-base-200 ring-2 ring-inset ring-primary/40' : '']"
        :aria-pressed="tokenFilter === 'active'"
        @click="tokenFilter = tokenFilter === 'active' ? 'all' : 'active'"
      >
        <div class="stat-title">Active</div>
        <div class="stat-value text-success">{{ activeCount }}</div>
        <div class="stat-desc">Currently valid</div>
      </button>
      <button
        type="button"
        data-test="kpi-card-expired"
        :class="['stat text-left transition-colors cursor-pointer hover:bg-base-200/60', tokenFilter === 'expired' ? 'bg-base-200 ring-2 ring-inset ring-primary/40' : '']"
        :aria-pressed="tokenFilter === 'expired'"
        @click="tokenFilter = tokenFilter === 'expired' ? 'all' : 'expired'"
      >
        <div class="stat-title">Expired / Revoked</div>
        <div class="stat-value text-warning">{{ expiredOrRevokedCount }}</div>
        <div class="stat-desc">No longer usable</div>
      </button>
    </div>

    <!-- Loading State -->
    <div v-if="loading" class="text-center py-12">
      <span class="loading loading-spinner loading-lg"></span>
      <p class="mt-4">Loading tokens...</p>
    </div>

    <!-- Error State -->
    <div v-else-if="error" class="alert alert-error">
      <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <div>
        <h3 class="font-bold">Failed to load tokens</h3>
        <div class="text-sm">{{ error }}</div>
      </div>
      <button @click="refreshTokens" class="btn btn-sm">
        Try Again
      </button>
    </div>

    <!-- Empty State -->
    <div v-else-if="tokens.length === 0" class="text-center py-12">
      <svg class="w-24 h-24 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z" />
      </svg>
      <h3 class="text-xl font-semibold mb-2">No agent tokens yet</h3>
      <p class="text-base-content/70 mb-4">
        Create scoped tokens for your AI agents and automated workflows.
      </p>
      <button @click="openCreateDialog" class="btn btn-primary">
        <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
        </svg>
        Create Your First Token
      </button>
    </div>

    <!-- Filter empty state: tokens exist but none match the active filter -->
    <div v-else-if="filteredTokens.length === 0" class="text-center py-12" data-test="tokens-filter-empty">
      <p class="text-base-content/70 mb-4">
        No tokens match the <span class="font-semibold">{{ tokenFilter }}</span> filter.
      </p>
      <button @click="tokenFilter = 'all'" class="btn btn-sm btn-outline">
        Show all
      </button>
    </div>

    <!-- Token List Table. Spec 134 FR-061: five columns fit 1280 px without a
         horizontal scrollbar; Kind, Mode, Prefix and Last used sit in the row
         disclosure (the token name opens it), and every row action stays. -->
    <div v-else class="rounded-box border border-base-300 bg-base-100" data-test="tokens-table">
      <table class="table w-full max-sm:block">
        <thead class="max-sm:sr-only">
          <tr>
            <th scope="col">Name</th>
            <th scope="col">Profile</th>
            <th scope="col">Expires</th>
            <th scope="col">Status</th>
            <th scope="col">Actions</th>
          </tr>
        </thead>
        <tbody class="max-sm:block">
          <template v-for="token in filteredTokens" :key="token.name">
          <tr :data-test="`token-row-${token.name}`" class="max-sm:flex max-sm:flex-col max-sm:gap-1 max-sm:border-b max-sm:border-base-300 max-sm:px-3 max-sm:py-3">
            <td class="font-medium align-top max-sm:p-0 max-sm:border-0! min-w-0">
              <button
                type="button"
                class="link link-hover text-left break-all max-sm:min-h-11"
                :aria-expanded="expandedToken === token.name ? 'true' : 'false'"
                :data-test="`token-expand-${token.name}`"
                @click="expandedToken = expandedToken === token.name ? '' : token.name"
              >{{ token.name }}</button>
              <!-- Spec 134 FR-061: describes the configured limits (own server and permission
                   list), not effective access, which a profile or a lock can narrow further. -->
              <StateTag v-if="token.legacy_scope" tone="review" class="ml-2" :data-test="`token-legacy-badge-${token.name}`" title="This token carries its own server list and permissions">Own server list</StateTag>
              <CredentialLifecycle
                v-if="token.issuer || token.purpose || token.profile_state === 'dangling'"
                class="mt-1"
                :id="token.name"
                :kind="isClientCredential(token) ? 'client' : 'token'"
                :profile="token.profile_pin"
                :mode="token.profile_mode"
                :issuer="token.issuer"
                :lease="token.lease"
                :expires-at="token.expires_at"
                :created-at="token.created_at"
                :revoked="token.revoked"
                :revoked-at="token.revoked_at"
                :purpose="token.purpose"
                :profile-state="token.profile_state"
                :hide-lease="true"
                :hide-binding="!!token.profile_pin"
                :now="now"
              />
              <!-- Spec 109-l: the Token-row links of the link map (agent rows only;
                   a client credential is filtered as a client, from Clients). -->
              <div v-if="tokenLinksAvailable && !isClientCredential(token)" class="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-sm font-normal">
                <router-link class="link inline-flex min-h-11 items-center sm:min-h-0" :data-test="`token-row-link-activity-${token.name}`" :aria-label="`Activity for token ${token.name}`" :to="scopeQuery!.linkTo('activity', { view: 'calls', token: token.name, client: '' })">Activity</router-link>
                <router-link class="link inline-flex min-h-11 items-center sm:min-h-0" :data-test="`token-row-link-usage-${token.name}`" :aria-label="`Usage for token ${token.name}`" :to="scopeQuery!.linkTo('usage', { token: token.name, client: '' })">Usage</router-link>
              </div>
            </td>
            <td class="align-top max-sm:p-0 max-sm:border-0!">
              <span class="sm:hidden text-sm text-base-content/70">Profile </span>
              <span v-if="token.profile_pin" class="badge badge-outline badge-sm max-w-[12rem] min-w-0 justify-start overflow-hidden whitespace-nowrap" :title="profilesStore.titleFor(token.profile_pin)" :data-test="`token-profile-${token.name}`"><span class="truncate">{{ profilesStore.titleFor(token.profile_pin) }}</span></span>
              <span v-else class="text-base-content/70 text-sm">&mdash;<span class="sr-only">No profile</span></span>
            </td>
            <td class="align-top whitespace-nowrap max-sm:p-0 max-sm:border-0! text-sm">
              <span class="sm:hidden text-base-content/70">Expires </span><span :class="{ 'text-warning': isExpiringSoon(token), 'text-error': isExpired(token) && !token.lease }" :data-test="`token-expiry-${token.name}`">
                {{ expiryText(token) }}
              </span>
            </td>
            <td class="align-top max-sm:p-0 max-sm:border-0!">
              <span v-if="token.revoked" class="flex flex-col items-start gap-0.5" :data-test="`token-state-${token.name}`">
                <StateTag tone="error">Revoked</StateTag>
                <span v-if="token.revoked_at" class="text-xs text-base-content/70 whitespace-nowrap">{{ formatDate(token.revoked_at) }}</span>
              </span>
              <StateTag v-else-if="isExpired(token) && token.lease" tone="off" :data-test="`token-state-${token.name}`">Lease ended</StateTag>
              <StateTag v-else-if="isExpired(token)" tone="review">Expired</StateTag>
              <StateTag v-else tone="ok">Active</StateTag>
            </td>
            <td class="align-top max-sm:p-0 max-sm:border-0!">
              <!-- A client credential is rotated through the staged path on the
                   Clients page, never regenerated or deleted here (Spec 108-i I20). -->
              <div v-if="isClientCredential(token)" class="text-sm" :data-test="`token-client-note-${token.name}`">
                Client credential for {{ token.client_id }} &mdash;
                <router-link class="link" :to="{ name: 'clients', query: { focus: token.client_id } }">manage from Clients</router-link>
              </div>
              <div v-else class="flex flex-wrap gap-1">
                <button
                  @click="handleRegenerate(token.name)"
                  :disabled="token.revoked"
                  class="btn btn-xs btn-outline whitespace-nowrap max-sm:min-h-11"
                  title="Regenerate token secret"
                >
                  <UiIcon name="refresh" class="w-4 h-4" />
                  Regenerate
                </button>
                <button
                  @click="handleRevoke(token.name)"
                  :disabled="token.revoked"
                  class="btn btn-xs btn-error btn-outline whitespace-nowrap max-sm:min-h-11"
                  title="Revoke token"
                >
                  <UiIcon name="x" class="w-4 h-4" />
                  Revoke
                </button>
                <button
                  v-if="token.revoked || isExpired(token)"
                  @click="handleDelete(token.name)"
                  class="btn btn-xs btn-error btn-outline whitespace-nowrap max-sm:min-h-11"
                  title="Permanently delete token and free its name for reuse"
                >
                  Delete
                </button>
              </div>
            </td>
          </tr>
          <!-- The row disclosure: the facts that left the table, then the scope of the
               token, read only. A token with its own server list shows that list and its
               permissions; a profile token's scope comes from the profile. -->
          <tr v-if="expandedToken === token.name" :data-test="`token-detail-${token.name}`" class="max-sm:block">
            <td colspan="5" class="bg-base-200/40 space-y-2 max-sm:block">
              <dl class="flex flex-wrap gap-x-6 gap-y-1 text-sm" :data-test="`token-facts-${token.name}`">
                <div v-if="token.profile_pin" class="flex gap-2"><dt class="text-base-content/70">Profile id</dt><dd :data-test="`token-profile-id-${token.name}`"><code class="text-sm">{{ token.profile_pin }}</code></dd></div>
                <div class="flex gap-2"><dt class="text-base-content/70">Kind</dt><dd :data-test="`token-kind-${token.name}`">{{ isClientCredential(token) ? 'Client' : 'Agent' }}</dd></div>
                <div class="flex gap-2"><dt class="text-base-content/70">Mode</dt><dd>{{ isClientCredential(token) && token.profile_mode ? modeLabel(token.profile_mode) : '—' }}</dd></div>
                <div class="flex gap-2"><dt class="text-base-content/70">Prefix</dt><dd><code class="text-sm bg-base-200 px-2 py-1 rounded">{{ token.token_prefix }}</code></dd></div>
                <div class="flex gap-2"><dt class="text-base-content/70">Last used</dt><dd :data-test="`token-last-used-${token.name}`">{{ token.last_used_at ? formatDate(token.last_used_at) : 'Never' }}</dd></div>
              </dl>
              <div class="flex flex-wrap items-center gap-2 text-sm">
                <span class="text-base-content/70">Servers</span>
                <span v-for="server in token.allowed_servers" :key="server" class="badge badge-outline badge-sm">{{ server }}</span>
                <span class="text-base-content/70 ml-3">Permissions</span>
                <span v-for="perm in token.permissions" :key="perm" class="badge badge-sm" :class="permissionBadgeClass(perm)">{{ perm }}</span>
              </div>
              <div v-if="token.legacy_scope && !isClientCredential(token)" class="flex flex-wrap items-center gap-2 text-sm" :data-test="`token-migrate-${token.name}`">
                <span>Migrate: create a new token with a profile.</span>
                <button type="button" class="btn btn-xs btn-outline max-sm:min-h-11" :data-test="`token-migrate-button-${token.name}`" @click="openMigrateDialog(token)">Create with a profile&hellip;</button>
              </div>
            </td>
          </tr>
          </template>
        </tbody>
      </table>
    </div>

    <!-- Token Secret Display (shown after creation or regeneration) -->
    <div v-if="newTokenSecret" class="alert alert-warning shadow-lg">
      <svg class="w-6 h-6 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
      </svg>
      <div class="flex-1">
        <h3 class="font-bold">Save this token now!</h3>
        <p class="text-sm mb-2">This token cannot be retrieved again after you dismiss this message.</p>
        <div class="flex items-center gap-2">
          <code class="text-sm bg-neutral text-neutral-content px-3 py-2 rounded font-mono break-all">{{ newTokenSecret }}</code>
          <button
            @click="copyToken"
            class="btn btn-sm btn-neutral shrink-0"
            :class="{ 'btn-success': copied }"
          >
            {{ copied ? 'Copied!' : 'Copy' }}
          </button>
        </div>
      </div>
      <button @click="dismissTokenSecret" class="btn btn-sm btn-ghost shrink-0">Dismiss</button>
    </div>

    <!-- Create Token Dialog -->
    <dialog ref="createDialog" class="modal" aria-labelledby="create-token-title">
      <div class="modal-box">
        <h3 id="create-token-title" class="font-bold text-lg mb-4">Create Agent Token</h3>

        <div class="space-y-4">
          <!-- Name -->
          <div class="form-control">
            <label class="label" for="token-name">
              <span class="label-text font-medium">Token Name</span>
            </label>
            <input
              id="token-name"
              v-model="createForm.name"
              type="text"
              placeholder="e.g., ci-pipeline, dev-agent"
              class="input input-bordered w-full"
              :class="{ 'input-error': createFormErrors.name }"
            />
            <label class="label" v-if="createFormErrors.name">
              <span class="label-text-alt text-error">{{ createFormErrors.name }}</span>
            </label>
            <label class="label" v-else>
              <span class="label-text-alt">Alphanumeric, hyphens, and underscores only</span>
            </label>
          </div>

          <!-- Profile first (Spec 108-i I20): scope comes from the profile. -->
          <div class="form-control">
            <label class="label" for="token-profile">
              <span class="label-text font-medium">Profile</span>
            </label>
            <select
              id="token-profile"
              v-model="createForm.profile"
              class="select select-bordered w-full"
              :class="{ 'select-error': createFormErrors.profile }"
              data-test="token-profile-select"
            >
              <option value="" disabled>Choose&hellip;</option>
              <option v-for="profile in profilesStore.profiles" :key="profile.name" :value="profile.name">{{ profile.title || profile.name }}{{ profile.title ? ` (${profile.name})` : '' }}</option>
              <option :value="LEGACY">None &mdash; legacy scope</option>
            </select>
            <label class="label" v-if="createFormErrors.profile">
              <span class="label-text-alt text-error">{{ createFormErrors.profile }}</span>
            </label>
            <label class="label" v-else-if="!profilesStore.hasProfiles">
              <span class="label-text-alt" data-test="token-no-profiles-hint">Create a profile to scope tokens simply</span>
            </label>
          </div>

          <!-- Expiry -->
          <div class="form-control">
            <label class="label">
              <span class="label-text font-medium">Expires In</span>
            </label>
            <select v-model="createForm.expiresIn" class="select select-bordered w-full">
              <option value="168h">7 days</option>
              <option value="720h">30 days</option>
              <option value="2160h">90 days</option>
              <option value="8760h">365 days</option>
            </select>
          </div>

          <details class="collapse collapse-arrow border border-base-300 rounded-lg" data-test="token-legacy-scope">
            <summary class="collapse-title text-sm font-medium">Legacy scope (advanced)</summary>
            <div class="collapse-content space-y-4">
              <p v-if="!legacyEnabled" class="text-xs text-base-content/60">Choose &ldquo;None &mdash; legacy scope&rdquo; as the profile to set servers and permissions by hand.</p>
              <fieldset :disabled="!legacyEnabled" class="space-y-4">
          <!-- Allowed Servers -->
          <div class="form-control">
            <label class="label">
              <span class="label-text font-medium">Allowed Servers</span>
            </label>
            <label class="flex items-center gap-2 cursor-pointer mb-2 px-1">
              <input
                type="checkbox"
                :checked="createForm.allServers"
                @change="toggleAllServers"
                class="checkbox checkbox-sm checkbox-primary"
              />
              <span class="text-sm font-medium">All servers</span>
              <span class="badge badge-ghost badge-xs">wildcard</span>
            </label>
            <div
              v-if="!createForm.allServers"
              class="border border-base-300 rounded-lg p-3 max-h-48 overflow-y-auto space-y-1"
            >
              <div v-if="availableServers.length === 0" class="text-sm text-base-content/50 py-2 text-center">
                No servers configured
              </div>
              <label
                v-for="server in availableServers"
                :key="server.name"
                class="flex items-center gap-2 cursor-pointer hover:bg-base-200 rounded px-2 py-1"
              >
                <input
                  type="checkbox"
                  :value="server.name"
                  v-model="createForm.selectedServers"
                  class="checkbox checkbox-sm"
                />
                <span class="text-sm">{{ server.name }}</span>
                <span
                  v-if="server.connected"
                  class="badge badge-success badge-xs ml-auto"
                >connected</span>
                <span
                  v-else
                  class="badge badge-ghost badge-xs ml-auto"
                >offline</span>
              </label>
            </div>
            <label class="label" v-if="!createForm.allServers && createFormErrors.servers">
              <span class="label-text-alt text-error">{{ createFormErrors.servers }}</span>
            </label>
          </div>

          <!-- Permissions -->
          <div class="form-control">
            <label class="label">
              <span class="label-text font-medium">Permissions</span>
            </label>
            <div class="flex flex-col gap-2">
              <label class="flex items-center gap-2 cursor-not-allowed">
                <input type="checkbox" checked disabled class="checkbox checkbox-sm checkbox-info" />
                <span class="text-sm">read</span>
                <span class="badge badge-info badge-xs">always included</span>
              </label>
              <label class="flex items-center gap-2 cursor-pointer">
                <input
                  v-model="createForm.permWrite"
                  type="checkbox"
                  class="checkbox checkbox-sm checkbox-warning"
                />
                <span class="text-sm">write</span>
              </label>
              <label class="flex items-center gap-2 cursor-pointer">
                <input
                  v-model="createForm.permDestructive"
                  type="checkbox"
                  class="checkbox checkbox-sm checkbox-error"
                />
                <span class="text-sm">destructive</span>
              </label>
            </div>
          </div>

              </fieldset>
            </div>
          </details>

        </div>

        <div class="modal-action">
          <button @click="closeCreateDialog" class="btn">Cancel</button>
          <button
            @click="handleCreate"
            :disabled="creating"
            class="btn btn-primary"
          >
            <span v-if="creating" class="loading loading-spinner loading-sm"></span>
            {{ creating ? 'Creating...' : 'Create Token' }}
          </button>
        </div>
      </div>
      <form method="dialog" class="modal-backdrop">
        <button>close</button>
      </form>
    </dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import ScopeChips from '@/components/scope/ScopeChips.vue'
import { useRoute, useRouter } from 'vue-router'
import apiClient from '@/services/api'
import { formatDateTimeShort } from '@/utils/datetime'
import { useSystemStore } from '@/stores/system'
import { useServersStore } from '@/stores/servers'
import { useProfilesStore } from '@/stores/profiles'
import { isScopeParamAvailable, useScopeQuery } from '@/composables/useScopeQuery'
import { describeError, modeLabel } from '@/utils/profiles'
import { expiresText, expiringSoon, isNeverExpires, leaseText } from '@/utils/credentials'
import { CREDENTIALS_CHANGED_EVENT, CLIENT_BINDING_CHANGED_EVENT } from '@/stores/clients'
import { PROFILES_CHANGED_EVENT } from '@/stores/profiles'
import CredentialLifecycle from '@/components/clients/CredentialLifecycle.vue'
import StateTag from '@/components/StateTag.vue'
import UiIcon from '@/components/UiIcon.vue'
import { useNow } from '@/composables/useNow'
import type { ApiError } from '@/services/api'
import type { AgentTokenInfo, CreateAgentTokenRequest, Server } from '@/types'

const route = useRoute()
// Spec 134 FR-016: a carried value the build does not list stays visible and removable.
const scopeUnavailable = computed(() =>
  (['profile', 'token'] as const).filter((name) => typeof route?.query[name] === 'string' && route.query[name] !== '' && !isScopeParamAvailable(name)))
const router = useRouter()
const systemStore = useSystemStore()
const serversStore = useServersStore()
const profilesStore = useProfilesStore()
// Spec 108-f/108-i: the `profile` and `token` filters of the URL contract are
// sent to GET /tokens, so the Viewing chip narrows this list on the server.
const scopeQuery = route ? useScopeQuery('tokens') : undefined
// Spec 109-l: the Token-row Activity / Usage links appear only once the build lists `token`.
// A link is never dead: both destinations must be registered routes.
const tokenLinksAvailable = computed(() =>
  Boolean(scopeQuery) && isScopeParamAvailable('token') && router.hasRoute('activity') && router.hasRoute('usage'))
// The sentinel option of the profile select: a token with no profile, scoped by
// its own server list and permissions (the pre-108 shape).
const LEGACY = '__legacy__'
const expandedToken = ref('')

const loading = ref(true)
const error = ref<string | null>(null)
const tokens = ref<AgentTokenInfo[]>([])
const creating = ref(false)
const newTokenSecret = ref<string | null>(null)
const copied = ref(false)

const createDialog = ref<HTMLDialogElement | null>(null)

const createForm = ref({
  name: '',
  profile: '',
  allServers: true,
  selectedServers: [] as string[],
  permWrite: false,
  permDestructive: false,
  expiresIn: '720h',
})

const createFormErrors = ref<{ name?: string; servers?: string; profile?: string }>({})
const legacyEnabled = computed(() => createForm.value.profile === LEGACY)

function isClientCredential(token: AgentTokenInfo): boolean {
  return token.kind === 'client'
}

// Available servers for the checkbox list
const availableServers = computed(() => {
  return serversStore.servers.map((s: Server) => ({
    name: s.name,
    connected: s.enabled && s.tool_count > 0,
  })).sort((a, b) => a.name.localeCompare(b.name))
})

function toggleAllServers(event: Event) {
  const checked = (event.target as HTMLInputElement).checked
  createForm.value.allServers = checked
  if (checked) {
    createForm.value.selectedServers = []
  }
}

// Computed stats
const activeCount = computed(() => {
  return tokens.value.filter(t => !t.revoked && !isExpired(t)).length
})

const expiredOrRevokedCount = computed(() => {
  return tokens.value.filter(t => t.revoked || isExpired(t)).length
})

// KPI-card-driven filter (issue #436): 'all' | 'active' | 'expired'
const tokenFilter = ref<'all' | 'active' | 'expired'>('all')

const filteredTokens = computed(() => {
  if (tokenFilter.value === 'active') {
    return tokens.value.filter(t => !t.revoked && !isExpired(t))
  }
  if (tokenFilter.value === 'expired') {
    return tokens.value.filter(t => t.revoked || isExpired(t))
  }
  return tokens.value
})

// Helper functions
// A reactive clock: an open page advances lease countdowns and expiry states.
const now = useNow()

function isExpired(token: AgentTokenInfo): boolean {
  if (isNeverExpires(token.expires_at, token.kind)) return false
  return new Date(token.expires_at).getTime() < now.value
}

// A lease (<= 24 h at issue) running out is the plan, not a warning (UI-005).
function isExpiringSoon(token: AgentTokenInfo): boolean {
  if (isExpired(token)) return false
  return expiringSoon(token, now.value)
}

function expiryText(token: AgentTokenInfo): string {
  return token.revoked ? expiresText(token.expires_at, token.kind) : (leaseText(token, now.value) || expiresText(token.expires_at, token.kind))
}

function formatDate(dateStr: string): string {
  return formatDateTimeShort(dateStr)
}

function permissionBadgeClass(perm: string): string {
  switch (perm) {
    case 'read': return 'badge-info'
    case 'write': return 'badge-warning'
    case 'destructive': return 'badge-error'
    default: return 'badge-ghost'
  }
}

// Data loading
let tokensTicket = 0
async function loadTokens(silent = false) {
  const ticket = ++tokensTicket
  // A live invalidation (credentials.changed and friends) refetches without
  // the spinner, so an open table updates in place (Spec 115 UI-004).
  if (!silent) loading.value = true
  error.value = null

  try {
    const rest = scopeQuery?.toRest()
    const response = await apiClient.listAgentTokens({ profile: rest?.profile, token: rest?.token })
    // A newer load owns the rows and the loading flag.
    if (ticket !== tokensTicket) return
    if (response.success && response.data) {
      tokens.value = response.data.tokens || []
    } else {
      error.value = response.error || 'Failed to load tokens'
    }
  } catch (err: any) {
    if (ticket !== tokensTicket) return
    error.value = err.message || 'Failed to load tokens'
    console.error('Failed to load tokens:', err)
  } finally {
    if (ticket === tokensTicket) loading.value = false
  }
}

const refreshTokens = () => loadTokens()
const refreshTokensSilently = () => { void loadTokens(true) }

// Create token
function openCreateDialog() { openCreateDialogWith({}) }

function openCreateDialogWith(preset: { name?: string; profile?: string }) {
  createForm.value = {
    name: preset.name ?? '',
    // `?profile=` (a profile card's "Create token with this profile") presets
    // the choice; otherwise the operator must choose one.
    profile: preset.profile ?? (typeof route?.query.profile === 'string' ? route.query.profile : ''),
    allServers: true,
    selectedServers: [],
    permWrite: false,
    permDestructive: false,
    expiresIn: '720h',
  }
  createFormErrors.value = {}
  // Ensure servers are loaded for the checkbox list
  if (serversStore.servers.length === 0) {
    serversStore.fetchServers()
  }
  if (!profilesStore.loaded) void profilesStore.fetchProfiles()
  createDialog.value?.showModal()
}

function closeCreateDialog() {
  createDialog.value?.close()
}

async function handleCreate() {
  // Validate
  createFormErrors.value = {}
  const name = createForm.value.name.trim()

  if (!name) {
    createFormErrors.value.name = 'Token name is required'
    return
  }

  if (!/^[a-zA-Z0-9_-]+$/.test(name)) {
    createFormErrors.value.name = 'Only alphanumeric characters, hyphens, and underscores allowed'
    return
  }

  if (!createForm.value.profile) {
    createFormErrors.value.profile = 'Choose a profile, or "None \u2014 legacy scope"'
    return
  }

  const legacy = createForm.value.profile === LEGACY
  // Validate servers (legacy scope only: a profile supplies its own)
  if (legacy && !createForm.value.allServers && createForm.value.selectedServers.length === 0) {
    createFormErrors.value.servers = 'Select at least one server or choose "All servers"'
    return
  }

  creating.value = true

  try {
    let request: CreateAgentTokenRequest
    if (legacy) {
      const allowedServers = createForm.value.allServers
        ? ['*']
        : [...createForm.value.selectedServers]

      const permissions: string[] = ['read']
      if (createForm.value.permWrite) permissions.push('write')
      if (createForm.value.permDestructive) permissions.push('destructive')
      request = { name, allowed_servers: allowedServers, permissions, expires_in: createForm.value.expiresIn }
    } else {
      // With a profile the server defaults the scope from it.
      request = { name, profile: createForm.value.profile, expires_in: createForm.value.expiresIn }
    }

    const response = await apiClient.createAgentToken(request)

    if (response.success && response.data) {
      newTokenSecret.value = response.data.token
      copied.value = false
      closeCreateDialog()
      await loadTokens()

      systemStore.addToast({
        type: 'success',
        title: 'Token Created',
        message: `Agent token "${name}" created successfully`,
      })
    } else {
      systemStore.addToast({
        type: 'error',
        title: 'Create Failed',
        message: response.error || 'Failed to create token',
      })
    }
  } catch (err: any) {
    // A 400 names the field (the reserved `client-` prefix is on `name`): show
    // it under the input rather than only as a toast.
    const refused = err as ApiError
    if (refused.field === 'name') createFormErrors.value.name = refused.message
    else if (refused.field === 'profile') createFormErrors.value.profile = refused.message
    else systemStore.addToast({
      type: 'error',
      title: 'Create Failed',
      message: describeError(err, 'Failed to create token'),
    })
  } finally {
    creating.value = false
  }
}

// Regenerate token
async function handleRegenerate(name: string) {
  if (!confirm(`Regenerate the secret for token "${name}"? The old secret will stop working immediately.`)) {
    return
  }

  try {
    const response = await apiClient.regenerateAgentToken(name)
    if (response.success && response.data) {
      newTokenSecret.value = response.data.token
      copied.value = false

      systemStore.addToast({
        type: 'success',
        title: 'Token Regenerated',
        message: `Token "${name}" has been regenerated. Save the new secret now.`,
      })
    } else {
      systemStore.addToast({
        type: 'error',
        title: 'Regenerate Failed',
        message: response.error || 'Failed to regenerate token',
      })
    }
  } catch (err: any) {
    systemStore.addToast({
      type: 'error',
      title: 'Regenerate Failed',
      message: err.message || 'Failed to regenerate token',
    })
  }
}

// Revoke token
async function handleRevoke(name: string) {
  if (!confirm(`Revoke token "${name}"? This action cannot be undone.`)) {
    return
  }

  try {
    const response = await apiClient.revokeAgentToken(name)
    if (response.success || !response.error) {
      await loadTokens()

      systemStore.addToast({
        type: 'success',
        title: 'Token Revoked',
        message: `Token "${name}" has been revoked`,
      })
    } else {
      systemStore.addToast({
        type: 'error',
        title: 'Revoke Failed',
        message: response.error || 'Failed to revoke token',
      })
    }
  } catch (err: any) {
    systemStore.addToast({
      type: 'error',
      title: 'Revoke Failed',
      message: err.message || 'Failed to revoke token',
    })
  }
}

// Permanently delete a (revoked or expired) token, freeing its name for reuse
async function handleDelete(name: string) {
  if (!confirm(`Permanently delete token "${name}"? This removes it completely and frees the name for reuse. This action cannot be undone.`)) {
    return
  }

  try {
    const response = await apiClient.deleteAgentToken(name)
    if (response.success || !response.error) {
      await loadTokens()

      systemStore.addToast({
        type: 'success',
        title: 'Token Deleted',
        message: `Token "${name}" has been permanently deleted`,
      })
    } else {
      systemStore.addToast({
        type: 'error',
        title: 'Delete Failed',
        message: response.error || 'Failed to delete token',
      })
    }
  } catch (err: any) {
    systemStore.addToast({
      type: 'error',
      title: 'Delete Failed',
      message: err.message || 'Failed to delete token',
    })
  }
}

// Clipboard
async function copyToken() {
  if (!newTokenSecret.value) return
  try {
    await navigator.clipboard.writeText(newTokenSecret.value)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  } catch {
    // Fallback for non-HTTPS contexts
    const textarea = document.createElement('textarea')
    textarea.value = newTokenSecret.value
    document.body.appendChild(textarea)
    textarea.select()
    document.execCommand('copy')
    document.body.removeChild(textarea)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  }
}

function dismissTokenSecret() {
  newTokenSecret.value = null
  copied.value = false
}

// Spec 109 FR-052: "+ Add -> Token" arrives as /clients?tab=tokens&create=1.
// Open the create dialog, then drop `create` (keeping tab and any sticky
// params, `profile` included: it presets the dialog and stays the list filter)
// so a reload or Back does not reopen it.
function openMigrateDialog(token: AgentTokenInfo) {
  openCreateDialogWith({ name: `${token.name}-profile` })
}

function consumeCreateParam() {
  if (route?.query.create !== '1') return
  openCreateDialog()
  const { create: _create, ...rest } = route.query
  void router.replace({ query: rest })
}

// Spec 115 UI-004: an issue, revoke or binding change made anywhere (MCP
// included) refreshes the open table.
const LIVE_EVENTS = [CREDENTIALS_CHANGED_EVENT, CLIENT_BINDING_CHANGED_EVENT, PROFILES_CHANGED_EVENT]
onBeforeUnmount(() => {
  for (const name of LIVE_EVENTS) window.removeEventListener(name, refreshTokensSilently)
})

watch(() => route?.query.create, consumeCreateParam)
watch(() => [scopeQuery?.state.profile, scopeQuery?.state.token], () => { void loadTokens() })

onMounted(async () => {
  for (const name of LIVE_EVENTS) window.addEventListener(name, refreshTokensSilently)
  // The list shows each token's profile by title.
  if (!profilesStore.loaded) void profilesStore.fetchProfiles()
  consumeCreateParam()
  await new Promise(resolve => setTimeout(resolve, 100))
  loadTokens()
})
</script>
