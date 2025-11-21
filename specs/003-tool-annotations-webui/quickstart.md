# Quickstart: Tool Annotations & MCP Sessions

**Feature Branch**: `003-tool-annotations-webui`

## Overview

This feature adds transparency to tool calls in the MCPProxy WebUI by:
1. Displaying tool annotations (readOnly, destructive, idempotent, openWorld hints)
2. Tracking MCP sessions with metrics (status, duration, tokens)
3. Enabling session-based filtering in tool call history

## Development Setup

### Prerequisites

- Go 1.21+
- Node.js 18+
- pnpm (for frontend)

### Build & Run

```bash
# Build backend
go build -o mcpproxy ./cmd/mcpproxy

# Build frontend
cd frontend && pnpm install && pnpm build && cd ..

# Run server
./mcpproxy serve
```

### Development Mode

```bash
# Terminal 1: Backend with hot reload
go run ./cmd/mcpproxy serve --log-level=debug

# Terminal 2: Frontend dev server
cd frontend && pnpm dev
```

## Implementation Checklist

### Backend (Go)

#### 1. Data Types (`internal/contracts/types.go`)

Add new types:
```go
// ToolAnnotation represents MCP tool behavior hints
type ToolAnnotation struct {
    Title           string `json:"title,omitempty"`
    ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
    DestructiveHint *bool  `json:"destructiveHint,omitempty"`
    IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
    OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// MCPSession represents a client session
type MCPSession struct {
    ID            string     `json:"id"`
    ClientName    string     `json:"client_name,omitempty"`
    ClientVersion string     `json:"client_version,omitempty"`
    Status        string     `json:"status"`
    StartTime     time.Time  `json:"start_time"`
    EndTime       *time.Time `json:"end_time,omitempty"`
    ToolCallCount int        `json:"tool_call_count"`
    TotalTokens   int        `json:"total_tokens"`
}
```

Extend existing types:
```go
// Add to Tool struct
Annotations *ToolAnnotation `json:"annotations,omitempty"`

// Add to ToolCallRecord struct
Annotations *ToolAnnotation `json:"annotations,omitempty"`
```

#### 2. Storage (`internal/storage/manager.go`)

Add session bucket and operations:
```go
const SessionsBucket = "sessions"

func (m *Manager) CreateSession(session *MCPSession) error
func (m *Manager) UpdateSessionStats(sessionID string, tokens int) error
func (m *Manager) CloseSession(sessionID string) error
func (m *Manager) GetRecentSessions(limit int) ([]*MCPSession, int, error)
func (m *Manager) GetSessionByID(id string) (*MCPSession, error)
func (m *Manager) GetToolCallsBySession(sessionID string, limit, offset int) ([]*ToolCallRecord, int, error)
```

#### 3. API Endpoints (`internal/httpapi/server.go`)

Add new routes:
```go
r.Get("/api/v1/sessions", s.handleGetSessions)
r.Get("/api/v1/sessions/{id}", s.handleGetSessionDetail)
r.Get("/api/v1/sessions/{id}/tool-calls", s.handleGetSessionToolCalls)
```

Modify existing route:
```go
// Add session_id filter to handleGetToolCalls
sessionID := r.URL.Query().Get("session_id")
```

#### 4. Tool Call Recording (`internal/server/mcp.go`)

Capture annotations when recording:
```go
func (s *Server) recordToolCall(...) {
    // Lookup tool annotations from server's tool list
    annotations := s.getToolAnnotations(serverName, toolName)

    record := &storage.ToolCallRecord{
        // ... existing fields ...
        Annotations: annotations,
    }
}
```

#### 5. Session Lifecycle (`internal/server/session_store.go`)

Extend to persist sessions:
```go
func (s *SessionStore) SetSession(sessionID, clientName, clientVersion string) {
    // ... existing in-memory ...

    // Persist to storage
    session := &contracts.MCPSession{
        ID:         sessionID,
        ClientName: clientName,
        Status:     "active",
        StartTime:  time.Now(),
    }
    s.storage.CreateSession(session)
}
```

### Frontend (Vue 3 + TypeScript)

#### 1. Types (`frontend/src/types/api.ts`)

```typescript
export interface ToolAnnotation {
  title?: string
  readOnlyHint?: boolean
  destructiveHint?: boolean
  idempotentHint?: boolean
  openWorldHint?: boolean
}

export interface MCPSession {
  id: string
  client_name?: string
  client_version?: string
  status: 'active' | 'closed'
  start_time: string
  end_time?: string
  tool_call_count: number
  total_tokens: number
}

// Extend Tool interface
export interface Tool {
  // ... existing ...
  annotations?: ToolAnnotation
}

// Extend ToolCallRecord interface
export interface ToolCallRecord {
  // ... existing ...
  annotations?: ToolAnnotation
}
```

#### 2. API Service (`frontend/src/services/api.ts`)

```typescript
async getSessions(limit = 10, offset = 0): Promise<GetSessionsResponse> {
  return this.get(`/api/v1/sessions?limit=${limit}&offset=${offset}`)
}

async getSession(id: string): Promise<GetSessionDetailResponse> {
  return this.get(`/api/v1/sessions/${id}`)
}

async getToolCalls(limit = 50, offset = 0, sessionId?: string): Promise<GetToolCallsResponse> {
  let url = `/api/v1/tool-calls?limit=${limit}&offset=${offset}`
  if (sessionId) url += `&session_id=${sessionId}`
  return this.get(url)
}
```

#### 3. Components

**AnnotationBadges.vue**:
```vue
<template>
  <div class="flex gap-1" v-if="annotations">
    <span v-if="annotations.title" class="text-sm font-medium">
      {{ annotations.title }}
    </span>
    <template v-if="!compact">
      <span v-if="annotations.readOnlyHint" class="badge badge-info badge-sm">
        Read-only
      </span>
      <span v-if="annotations.destructiveHint" class="badge badge-error badge-sm">
        Destructive
      </span>
      <!-- ... other badges ... -->
    </template>
    <template v-else>
      <!-- Compact icons with tooltips -->
    </template>
  </div>
</template>
```

**SessionsTable.vue**:
```vue
<template>
  <table class="table">
    <thead>
      <tr>
        <th>Status</th>
        <th>Client</th>
        <th>Start Time</th>
        <th>Duration</th>
        <th>Tool Calls</th>
        <th>Tokens</th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="session in sessions" :key="session.id"
          @click="navigateToHistory(session.id)"
          class="cursor-pointer hover:bg-base-200">
        <!-- ... session data ... -->
      </tr>
    </tbody>
  </table>
</template>
```

#### 4. Views

**Dashboard.vue** - Add sessions table:
```vue
<template>
  <div>
    <!-- ... existing dashboard content ... -->
    <SessionsTable :sessions="recentSessions" />
  </div>
</template>

<script setup>
const recentSessions = ref([])

onMounted(async () => {
  const response = await api.getSessions(10)
  recentSessions.value = response.sessions
})

// Poll for updates every 30s
setInterval(async () => {
  const response = await api.getSessions(10)
  recentSessions.value = response.sessions
}, 30000)
</script>
```

**ServerDetail.vue** - Add annotations to tool cards:
```vue
<template>
  <div v-for="tool in tools" class="card">
    <div class="card-body">
      <h3>{{ tool.annotations?.title || tool.name }}</h3>
      <AnnotationBadges :annotations="tool.annotations" />
      <!-- ... existing tool info ... -->
    </div>
  </div>
</template>
```

**ToolCalls.vue** - Add session filter and compact annotations:
```vue
<template>
  <div>
    <SessionFilter v-model="selectedSessionId" />

    <table class="table">
      <tr v-for="call in toolCalls">
        <td>{{ call.tool_name }}</td>
        <td><AnnotationBadges :annotations="call.annotations" compact /></td>
        <!-- ... other columns ... -->
      </tr>
    </table>
  </div>
</template>

<script setup>
const route = useRoute()
const selectedSessionId = ref(route.query.sessionId || null)

watch(selectedSessionId, async (newId) => {
  // Update URL and fetch filtered data
  router.push({ query: { sessionId: newId } })
  await fetchToolCalls()
})
</script>
```

## Testing

### Backend Tests

```bash
# Unit tests for session storage
go test ./internal/storage -run TestSession -v

# API integration tests
go test ./internal/httpapi -run TestSession -v

# Full E2E test
./scripts/test-api-e2e.sh
```

### Frontend Tests

```bash
cd frontend

# Unit tests
pnpm test

# E2E tests (requires backend running)
pnpm test:e2e
```

### Manual Testing Checklist

1. **Tool Annotations**:
   - [ ] Annotations display on server detail page
   - [ ] Compact annotations in tool call history
   - [ ] Tooltips show on hover
   - [ ] Graceful handling of missing annotations

2. **Sessions Dashboard**:
   - [ ] Table shows 10 most recent sessions
   - [ ] Active sessions show "active" status
   - [ ] Closed sessions show calculated duration
   - [ ] Clicking row navigates to filtered history

3. **Session Filtering**:
   - [ ] Filter dropdown populated with sessions
   - [ ] Filter persists in URL
   - [ ] Clear filter shows all calls
   - [ ] Empty state for sessions with no calls

## API Reference

See [contracts/sessions-api.yaml](./contracts/sessions-api.yaml) for full OpenAPI specification.

## Troubleshooting

### Annotations Not Showing

1. Check upstream MCP server returns annotations in tool definitions
2. Verify `internal/contracts/types.go` has `Annotations` field
3. Check browser console for API response structure

### Sessions Not Persisting

1. Check `sessions` bucket exists in BBolt
2. Verify session lifecycle hooks in `session_store.go`
3. Check logs for storage errors

### Filter Not Working

1. Verify `session_id` query param in API request
2. Check tool call records have `mcp_session_id` populated
3. Verify storage query filters correctly
