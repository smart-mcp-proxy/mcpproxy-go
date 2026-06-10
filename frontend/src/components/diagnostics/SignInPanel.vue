<template>
  <!--
    MCP-1821 — calm OAuth Sign-in CTA.

    Replaces the red "Server Error / file a bug" panel for OAuth-protected
    upstreams that simply need the user to authenticate. A first-time login is
    a calm amber prompt; an expired/revoked session keeps an error tone but
    still leads with the actionable Re-login button.
  -->
  <div
    class="alert"
    :class="isReauth ? 'alert-error' : 'alert-warning'"
    role="alert"
    :aria-label="title"
    data-test="oauth-signin-panel"
  >
    <span class="text-2xl shrink-0" aria-hidden="true">🔑</span>
    <div class="w-full">
      <h3 class="font-bold" data-test="oauth-signin-title">{{ title }}</h3>
      <p class="text-sm mt-1" data-test="oauth-signin-body">{{ body }}</p>

      <!-- Quarantine coexists: login is allowed while quarantined, but tools
           stay blocked until the server is approved. Clarify both gates. -->
      <p
        v-if="quarantined"
        class="text-xs opacity-80 mt-2"
        data-test="oauth-signin-quarantine-note"
      >
        This server is also quarantined. You can sign in now, but its tools stay
        blocked until you Approve the server.
      </p>

      <div class="mt-3 flex items-center gap-3 flex-wrap">
        <button
          type="button"
          class="btn btn-sm"
          :class="isReauth ? 'btn-error' : 'btn-warning'"
          :disabled="loading"
          data-test="oauth-signin-login-btn"
          @click="$emit('login')"
        >
          <span v-if="loading" class="loading loading-spinner loading-xs"></span>
          {{ buttonLabel }}
        </button>

        <a
          v-if="resolvedDocsUrl"
          :href="resolvedDocsUrl"
          target="_blank"
          rel="noopener noreferrer"
          class="link link-hover text-xs"
          data-test="oauth-signin-docs-link"
        >
          Learn about OAuth sign-in →
        </a>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { OAuthSignInState } from '@/utils/health'

interface Props {
  serverName: string
  state: OAuthSignInState
  docsUrl?: string
  quarantined?: boolean
  loading?: boolean
}

const props = defineProps<Props>()

defineEmits<{
  (e: 'login'): void
}>()

const DEFAULT_DOCS_URL = 'https://docs.mcpproxy.app/features/oauth-authentication'

const isReauth = computed(() => props.state === 'reauth')

const title = computed(() =>
  isReauth.value
    ? `🔑 Session expired — sign in to ${props.serverName}`
    : `🔑 Sign in to ${props.serverName}`,
)

const body = computed(() =>
  isReauth.value
    ? `Your session for ${props.serverName} expired or was revoked. Sign in again to reconnect.`
    : `${props.serverName} needs you to sign in before mcpproxy can connect and discover its tools.`,
)

const buttonLabel = computed(() => (isReauth.value ? 'Re-login' : 'Log in'))

const resolvedDocsUrl = computed(() => props.docsUrl || DEFAULT_DOCS_URL)
</script>
