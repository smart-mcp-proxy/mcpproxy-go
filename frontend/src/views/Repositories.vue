<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="flex justify-between items-center">
      <div>
        <h1 class="text-3xl font-bold">Repositories</h1>
        <p class="text-base-content/70 mt-1">Browse and discover MCP server repositories</p>
      </div>
      <div class="flex items-center space-x-2">
        <div class="badge badge-outline">Coming Soon</div>
      </div>
    </div>

    <!-- Placeholder Content -->
    <div class="card bg-base-100 shadow-md">
      <div class="card-body">
        <div class="text-center py-12">
          <svg class="w-16 h-16 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10" />
          </svg>
          <h3 class="text-xl font-semibold mb-2">Repository Explorer</h3>
          <p class="text-base-content/70 mb-4">
            This feature will allow you to browse and discover MCP server repositories from various registries.
          </p>
          <div class="space-y-2">
            <p class="text-sm text-base-content/60">
              <strong>Coming in Phase 7:</strong>
            </p>
            <ul class="text-sm text-base-content/60 space-y-1">
              <li>• Search MCP server registries</li>
              <li>• Browse by categories and tags</li>
              <li>• One-click server installation</li>
              <li>• LLM-friendly server descriptions</li>
            </ul>
          </div>
          <div class="mt-6">
            <router-link to="/servers" class="btn btn-primary">
              Manage Current Servers
            </router-link>
          </div>
        </div>
      </div>
    </div>

    <!-- Hint Panel for LLM Usage -->
    <div class="collapse collapse-arrow bg-base-200">
      <input type="checkbox" />
      <div class="collapse-title text-xl font-medium">
        💡 LLM Usage Hints
      </div>
      <div class="collapse-content">
        <div class="space-y-3 text-sm">
          <p class="text-base-content/70">
            While the repository explorer is under development, you can still add MCP servers via CLI or LLM commands:
          </p>
          <div class="mockup-code">
            <pre><code># Add server via CLI
mcpproxy call tool --tool-name=upstream_servers \
  --json_args='{"operation":"add","name":"new-server","url":"https://api.example.com/mcp","enabled":true}'</code></pre>
          </div>
          <p class="text-base-content/70">
            Or ask an LLM agent to help discover and add MCP servers for your specific use case.
          </p>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
// Placeholder component for Phase 7 implementation
// This will eventually contain the repositories explorer functionality
</script>