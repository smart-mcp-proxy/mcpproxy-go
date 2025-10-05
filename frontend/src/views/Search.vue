<template>
  <div class="space-y-6">
    <!-- Page Header -->
    <div class="text-center mb-8">
      <h1 class="text-4xl font-bold mb-4">Search Tools</h1>
      <p class="text-base-content/70 text-lg">Find tools across all MCP servers using intelligent BM25 search</p>
    </div>

    <!-- Search Interface -->
    <div class="card bg-base-100 shadow-lg max-w-4xl mx-auto">
      <div class="card-body">
        <div class="flex flex-col space-y-4">
          <!-- Main Search Input -->
          <div class="relative">
            <input
              v-model="searchQuery"
              type="text"
              placeholder="Search for tools (e.g. 'echo', 'file operations', 'random number')..."
              class="input input-bordered input-lg w-full pl-12 pr-4"
              @keyup.enter="performSearch"
              @input="debouncedSearch"
            />
            <svg class="absolute left-4 top-1/2 transform -translate-y-1/2 w-6 h-6 text-base-content/50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
            </svg>
          </div>

          <!-- Search Options -->
          <div class="flex flex-wrap gap-4 items-center">
            <div class="form-control">
              <label class="label">
                <span class="label-text">Results per page</span>
              </label>
              <select v-model="searchLimit" class="select select-bordered select-sm">
                <option :value="5">5</option>
                <option :value="10">10</option>
                <option :value="20">20</option>
                <option :value="50">50</option>
              </select>
            </div>

            <div class="form-control">
              <label class="label">
                <span class="label-text">Minimum relevance</span>
              </label>
              <select v-model="minScore" class="select select-bordered select-sm">
                <option :value="0">Any</option>
                <option :value="0.1">Low (0.1+)</option>
                <option :value="0.3">Medium (0.3+)</option>
                <option :value="0.5">High (0.5+)</option>
                <option :value="0.8">Very High (0.8+)</option>
              </select>
            </div>

            <button
              class="btn btn-primary"
              :disabled="!searchQuery.trim() || searching"
              @click="performSearch"
            >
              <span v-if="searching" class="loading loading-spinner loading-sm"></span>
              Search
            </button>

            <button
              v-if="searchQuery"
              class="btn btn-outline btn-sm"
              @click="clearSearch"
            >
              Clear
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- Search Results -->
    <div v-if="hasSearched">
      <!-- Results Header -->
      <div class="flex justify-between items-center">
        <div>
          <h2 class="text-2xl font-semibold">Search Results</h2>
          <p class="text-base-content/70">
            {{ searchResults.length }} results for "<span class="font-medium">{{ lastSearchQuery }}</span>"
            <span v-if="searchDuration">({{ searchDuration }}ms)</span>
          </p>
        </div>
        <div v-if="searchResults.length > 0" class="flex items-center space-x-2">
          <div class="badge badge-outline">BM25 Ranked</div>
        </div>
      </div>

      <!-- Loading State -->
      <div v-if="searching" class="text-center py-12">
        <span class="loading loading-spinner loading-lg"></span>
        <p class="mt-4">Searching tools...</p>
      </div>

      <!-- Error State -->
      <div v-else-if="searchError" class="alert alert-error">
        <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
        <span>{{ searchError }}</span>
        <button class="btn btn-sm" @click="performSearch">Retry</button>
      </div>

      <!-- No Results -->
      <div v-else-if="searchResults.length === 0" class="text-center py-12">
        <svg class="w-16 h-16 mx-auto mb-4 opacity-50" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
        </svg>
        <h3 class="text-xl font-semibold mb-2">No tools found</h3>
        <p class="text-base-content/70 mb-4">
          Try different keywords or check if your servers are connected.
        </p>
        <div class="space-x-2">
          <button class="btn btn-outline" @click="clearSearch">
            New Search
          </button>
          <router-link to="/servers" class="btn btn-primary">
            Check Servers
          </router-link>
        </div>
      </div>

      <!-- Results List -->
      <div v-else class="space-y-3">
        <div
          v-for="(result, index) in filteredResults"
          :key="`${result.tool.server_name}:${result.tool.name}`"
          class="card bg-base-100 shadow-md hover:shadow-lg transition-shadow"
        >
          <div class="card-body py-4">
            <div class="flex items-start justify-between gap-4">
              <div class="flex-1 min-w-0">
                <!-- Tool Name and Server Badge -->
                <div class="flex items-center gap-2 mb-2 flex-wrap">
                  <h3 class="text-lg font-bold text-base-content">{{ result.tool.name }}</h3>
                  <div class="badge badge-secondary badge-sm">{{ result.tool.server_name }}</div>
                  <div class="badge badge-ghost badge-sm">
                    Score: {{ result.score.toFixed(2) }}
                  </div>
                </div>

                <!-- Description (truncated) -->
                <p class="text-sm text-base-content/70 line-clamp-2 mb-2">
                  {{ result.tool.description || 'No description available' }}
                </p>

                <!-- Metadata -->
                <div class="flex items-center gap-3 text-xs text-base-content/60">
                  <span>#{{ index + 1 }} in results</span>
                  <span v-if="result.tool.input_schema" class="flex items-center gap-1">
                    <svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
                    </svg>
                    Schema available
                  </span>
                </div>
              </div>

              <!-- Action Buttons -->
              <div class="flex flex-col gap-2 flex-shrink-0">
                <button
                  class="btn btn-sm btn-primary"
                  @click="viewToolDetails(result)"
                >
                  View Details
                </button>
                <router-link
                  :to="`/servers/${result.tool.server_name}`"
                  class="btn btn-sm btn-outline"
                >
                  Server Info
                </router-link>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Empty State (before any search) -->
    <div v-else class="text-center py-16">
      <svg class="w-20 h-20 mx-auto mb-6 text-base-content/30" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
      </svg>
      <h3 class="text-2xl font-semibold mb-4">Powerful Tool Search</h3>
      <p class="text-base-content/70 text-lg mb-6 max-w-2xl mx-auto">
        Use our BM25-powered search to find the perfect tool for your task. Search by name, description, or functionality.
      </p>
      <div class="grid grid-cols-1 md:grid-cols-3 gap-4 max-w-4xl mx-auto">
        <div class="card bg-base-200 border border-base-300">
          <div class="card-body text-center">
            <h4 class="font-semibold mb-2">Natural Language</h4>
            <p class="text-sm text-base-content/70">Search using natural descriptions like "send email" or "file operations"</p>
          </div>
        </div>
        <div class="card bg-base-200 border border-base-300">
          <div class="card-body text-center">
            <h4 class="font-semibold mb-2">Relevance Scoring</h4>
            <p class="text-sm text-base-content/70">Results ranked by relevance with visual score indicators</p>
          </div>
        </div>
        <div class="card bg-base-200 border border-base-300">
          <div class="card-body text-center">
            <h4 class="font-semibold mb-2">Cross-Server</h4>
            <p class="text-sm text-base-content/70">Search across all connected MCP servers simultaneously</p>
          </div>
        </div>
      </div>
    </div>

    <!-- Tool Details Modal -->
    <div v-if="selectedTool" class="modal modal-open">
      <div class="modal-box max-w-4xl">
        <h3 class="font-bold text-lg mb-4">{{ selectedTool.tool.name }}</h3>

        <div class="space-y-4">
          <div class="grid grid-cols-2 gap-4">
            <div>
              <label class="block text-sm font-medium mb-1">Server</label>
              <div class="badge badge-secondary">{{ selectedTool.tool.server_name }}</div>
            </div>
            <div>
              <label class="block text-sm font-medium mb-1">Relevance Score</label>
              <div class="flex items-center space-x-2">
                <span class="font-mono">{{ selectedTool.score.toFixed(3) }}</span>
                <div class="w-20 bg-base-300 rounded-full h-2">
                  <div
                    class="bg-primary h-2 rounded-full"
                    :style="{ width: Math.min(100, selectedTool.score * 100) + '%' }"
                  ></div>
                </div>
              </div>
            </div>
          </div>

          <div>
            <label class="block text-sm font-medium mb-1">Description</label>
            <p class="text-sm">{{ selectedTool.tool.description || 'No description available' }}</p>
          </div>

          <div v-if="selectedTool.tool.input_schema">
            <label class="block text-sm font-medium mb-1">Input Schema</label>
            <div class="mockup-code">
              <pre><code>{{ JSON.stringify(selectedTool.tool.input_schema, null, 2) }}</code></pre>
            </div>
          </div>
        </div>

        <div class="modal-action">
          <router-link
            :to="`/servers/${selectedTool.tool.server_name}`"
            class="btn btn-outline"
            @click="selectedTool = null"
          >
            View Server
          </router-link>
          <button class="btn" @click="selectedTool = null">Close</button>
        </div>
      </div>
    </div>

    <!-- Hints Panel (Bottom of Page) -->
    <CollapsibleHintsPanel :hints="searchHints" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import type { SearchResult } from '@/types'
import api from '@/services/api'
import CollapsibleHintsPanel from '@/components/CollapsibleHintsPanel.vue'
import type { Hint } from '@/components/CollapsibleHintsPanel.vue'

const route = useRoute()

// State
const searchQuery = ref('')
const lastSearchQuery = ref('')
const searchResults = ref<SearchResult[]>([])
const searching = ref(false)
const searchError = ref<string | null>(null)
const hasSearched = ref(false)
const searchDuration = ref<number | null>(null)
const selectedTool = ref<SearchResult | null>(null)

// Search options
const searchLimit = ref(10)
const minScore = ref(0)

// Computed
const filteredResults = computed(() => {
  return searchResults.value.filter(result => result.score >= minScore.value)
})

// Debounced search
let searchTimeout: number | null = null
const debouncedSearch = () => {
  if (searchTimeout) {
    clearTimeout(searchTimeout)
  }
  searchTimeout = setTimeout(() => {
    if (searchQuery.value.trim()) {
      performSearch()
    }
  }, 500)
}

// Methods
async function performSearch() {
  if (!searchQuery.value.trim()) return

  searching.value = true
  searchError.value = null
  searchDuration.value = null
  lastSearchQuery.value = searchQuery.value

  const startTime = Date.now()

  try {
    const response = await api.searchTools(searchQuery.value, searchLimit.value)

    if (response.success && response.data) {
      searchResults.value = response.data.results || []
      searchDuration.value = Date.now() - startTime
      hasSearched.value = true
    } else {
      searchError.value = response.error || 'Search failed'
      searchResults.value = []
    }
  } catch (err) {
    searchError.value = err instanceof Error ? err.message : 'Search failed'
    searchResults.value = []
  } finally {
    searching.value = false
  }
}

function viewToolDetails(tool: SearchResult) {
  selectedTool.value = tool
}

function clearSearch() {
  searchQuery.value = ''
  lastSearchQuery.value = ''
  searchResults.value = []
  hasSearched.value = false
  searchError.value = null
  searchDuration.value = null
}

// Initialize search from URL query parameter
onMounted(() => {
  const queryParam = route.query.q
  if (queryParam && typeof queryParam === 'string') {
    searchQuery.value = queryParam
    performSearch()
  }
})

// Search hints
const searchHints = computed<Hint[]>(() => {
  return [
    {
      icon: '🔍',
      title: 'How to Search Tools',
      description: 'Tips for getting the best search results',
      sections: [
        {
          title: 'Search strategies',
          list: [
            'Use descriptive keywords: "create file", "send email", "random number"',
            'Search by functionality rather than exact tool names',
            'Use multiple keywords to narrow results',
            'Adjust minimum relevance score to filter results'
          ]
        },
        {
          title: 'CLI search',
          codeBlock: {
            language: 'bash',
            code: `# Search from command line\nmcpproxy tools search "your query"\n\n# Limit results\nmcpproxy tools search "your query" --limit=20`
          }
        }
      ]
    },
    {
      icon: '🤖',
      title: 'Search with LLM Agents',
      description: 'Let AI agents search and discover tools for you',
      sections: [
        {
          title: 'Example LLM prompts',
          list: [
            'Search for all file-related tools across my MCP servers',
            'Find tools that can help me work with GitHub issues',
            'Show me the most relevant tools for sending notifications',
            'What tools are available for data analysis?'
          ]
        },
        {
          title: 'LLM can call retrieve_tools',
          text: 'AI agents can use the retrieve_tools built-in tool:',
          codeBlock: {
            language: 'bash',
            code: `# LLM agents call this tool internally\nmcpproxy call tool --tool-name=retrieve_tools \\\n  --json_args='{"query":"file operations","limit":10}'`
          }
        }
      ]
    },
    {
      icon: '💡',
      title: 'Understanding Search Results',
      description: 'How MCPProxy ranks and displays results',
      sections: [
        {
          title: 'BM25 scoring',
          text: 'MCPProxy uses BM25 (Best Matching 25) algorithm for relevance ranking:',
          list: [
            'Scores range from 0.0 to ~1.0+ (higher is more relevant)',
            'Takes into account keyword frequency and rarity',
            'Considers tool name and description',
            'Server-qualified names (server:tool) for easy identification'
          ]
        }
      ]
    }
  ]
})
</script>