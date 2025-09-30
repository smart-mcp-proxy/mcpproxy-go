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

    <!-- Hints Panel -->
    <HintsPanel :hints="repositoriesHints" />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import HintsPanel from '@/components/HintsPanel.vue'
import type { Hint } from '@/components/HintsPanel.vue'

// Repositories hints
const repositoriesHints = computed<Hint[]>(() => {
  return [
    {
      icon: '📦',
      title: 'Add MCP Servers Manually',
      description: 'While the repository explorer is being developed, you can add servers via CLI',
      sections: [
        {
          title: 'Popular MCP servers',
          list: [
            '@modelcontextprotocol/server-filesystem - File system operations',
            '@modelcontextprotocol/server-github - GitHub API integration',
            '@modelcontextprotocol/server-git - Git operations',
            '@modelcontextprotocol/server-slack - Slack integration',
            '@modelcontextprotocol/server-postgres - PostgreSQL database'
          ]
        },
        {
          title: 'Add npm-based server',
          codeBlock: {
            language: 'bash',
            code: `# Add filesystem server\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"add","name":"filesystem","command":"npx","args_json":"[\\"@modelcontextprotocol/server-filesystem\\"]","protocol":"stdio","enabled":true}'`
          }
        },
        {
          title: 'Add Python-based server',
          codeBlock: {
            language: 'bash',
            code: `# Add Python MCP server via uvx\nmcpproxy call tool --tool-name=upstream_servers \\\n  --json_args='{"operation":"add","name":"python-server","command":"uvx","args_json":"[\\"your-package\\"]","protocol":"stdio","enabled":true}'`
          }
        }
      ]
    },
    {
      icon: '🤖',
      title: 'Discover Servers with LLM Agents',
      description: 'Let AI help you find and configure MCP servers',
      sections: [
        {
          title: 'Example LLM prompts',
          list: [
            'Find MCP servers for working with GitHub and add them to my configuration',
            'What MCP servers are available for file system operations?',
            'Help me find and install MCP servers for database access',
            'Search for MCP servers related to Slack and add the best one'
          ]
        },
        {
          title: 'LLM-driven discovery',
          text: 'AI agents can help you:',
          list: [
            'Search npm registry for @modelcontextprotocol/* packages',
            'Find Python packages that work with uvx',
            'Recommend servers based on your use case',
            'Automatically configure and add servers'
          ]
        }
      ]
    },
    {
      icon: '🔗',
      title: 'Official MCP Resources',
      description: 'Learn more about available MCP servers',
      sections: [
        {
          title: 'Useful links',
          list: [
            'MCP GitHub: github.com/modelcontextprotocol',
            'npm registry: search for "@modelcontextprotocol"',
            'Community servers: Check GitHub topics for "mcp-server"'
          ]
        }
      ]
    }
  ]
})
</script>