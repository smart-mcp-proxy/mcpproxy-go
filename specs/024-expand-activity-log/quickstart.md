# Quickstart: Expand Activity Log (Spec 024)

This guide provides the implementation order and key files for each component.

## Implementation Order

1. **Backend: New Event Types** (P1)
2. **Backend: Multi-Type Filter** (P2)
3. **Web UI: Enable Activity Log Menu** (P2)
4. **Web UI: Multi-Select Filter** (P2)
5. **Web UI: Intent Column & Sorting** (P2)
6. **CLI: Multi-Type Filter** (P3)
7. **Documentation** (P3)
8. **Testing & Validation**

---

## 1. Backend: New Event Types

### 1.1 Add ActivityType Constants

**File**: `internal/storage/activity_models.go`

```go
const (
    // Existing types...
    ActivityTypeToolCall         ActivityType = "tool_call"
    ActivityTypePolicyDecision   ActivityType = "policy_decision"
    ActivityTypeQuarantineChange ActivityType = "quarantine_change"
    ActivityTypeServerChange     ActivityType = "server_change"

    // NEW: Spec 024
    ActivityTypeSystemStart      ActivityType = "system_start"
    ActivityTypeSystemStop       ActivityType = "system_stop"
    ActivityTypeInternalToolCall ActivityType = "internal_tool_call"
    ActivityTypeConfigChange     ActivityType = "config_change"
)
```

### 1.2 Add EventType Constants

**File**: `internal/runtime/events.go`

```go
const (
    // Existing...

    // NEW: Spec 024
    EventTypeActivitySystemStart        EventType = "activity.system.start"
    EventTypeActivitySystemStop         EventType = "activity.system.stop"
    EventTypeActivityInternalToolCall   EventType = "activity.internal_tool_call.completed"
    EventTypeActivityConfigChange       EventType = "activity.config_change"
)
```

### 1.3 Emit System Start/Stop Events

**File**: `internal/server/server.go`

In `StartServer()` method after HTTP listener bind:
```go
s.runtime.EmitActivitySystemStart(version, listenAddr, startupDurationMs, configPath)
```

In `Shutdown()` method at the beginning:
```go
s.runtime.EmitActivitySystemStop(reason, uptimeSeconds, errorMsg)
```

### 1.4 Emit Internal Tool Call Events

**File**: `internal/server/mcp.go`

In each internal tool handler (`handleRetrieveTools`, `handleCallToolRead`, etc.):
```go
p.emitActivityInternalToolCall(toolName, targetServer, targetTool, args, status, durationMs, intent)
```

### 1.5 Emit Config Change Events

**File**: `internal/runtime/activity_service.go`

Subscribe to `EventTypeServersChanged` and create `config_change` activity records.

---

## 2. Backend: Multi-Type Filter

### 2.1 Update ActivityFilter

**File**: `internal/storage/activity_models.go`

```go
type ActivityFilter struct {
    Types     []string  // Changed from Type string
    Server    string
    // ... rest unchanged
}
```

Update `Matches()` method for OR logic.

### 2.2 Update API Handler

**File**: `internal/httpapi/activity.go`

Parse comma-separated `type` parameter:
```go
typeParam := r.URL.Query().Get("type")
if typeParam != "" {
    filter.Types = strings.Split(typeParam, ",")
}
```

---

## 3. Web UI: Enable Activity Log Menu

**File**: `frontend/src/components/SidebarNav.vue`

Uncomment or add Activity Log menu item:
```vue
<router-link to="/activity" class="...">
  <span>Activity Log</span>
</router-link>
```

---

## 4. Web UI: Multi-Select Filter

**File**: `frontend/src/views/Activity.vue`

Create multi-select dropdown component:
```vue
<div class="dropdown">
  <label tabindex="0" class="btn btn-sm">
    Event Type {{ selectedTypes.length > 0 ? `(${selectedTypes.length})` : '' }}
  </label>
  <ul class="dropdown-content menu p-2 shadow bg-base-100 rounded-box w-52">
    <li v-for="type in eventTypes" :key="type">
      <label class="label cursor-pointer">
        <input type="checkbox" :value="type" v-model="selectedTypes" class="checkbox checkbox-sm" />
        <span class="label-text">{{ type }}</span>
      </label>
    </li>
  </ul>
</div>
```

---

## 5. Web UI: Intent Column & Sorting

**File**: `frontend/src/views/Activity.vue`

### Intent Column

Add column to table:
```vue
<th>Intent</th>
<!-- In row -->
<td>
  <span v-if="activity.metadata?.intent" :title="activity.metadata.intent.reason">
    {{ getIntentIcon(activity.metadata.intent.operation_type) }}
    {{ truncate(activity.metadata.intent.reason, 30) }}
  </span>
  <span v-else>-</span>
</td>
```

### Sortable Columns

Add sort state and click handlers:
```vue
<th @click="sortBy('timestamp')" class="cursor-pointer">
  Time {{ getSortIndicator('timestamp') }}
</th>
```

---

## 6. CLI: Multi-Type Filter

**File**: `cmd/mcpproxy/activity_cmd.go`

Update type flag handling:
```go
typeFlag, _ := cmd.Flags().GetString("type")
if typeFlag != "" {
    types := strings.Split(typeFlag, ",")
    for _, t := range types {
        if !isValidActivityType(t) {
            return fmt.Errorf("invalid type: %s. Valid types: %s", t, validTypesString)
        }
    }
    params.Set("type", typeFlag)
}
```

---

## 7. Documentation

### Files to Update

1. **`docs/features/activity-log.md`**
   - Add new event types to table
   - Add examples for system_start, system_stop, internal_tool_call, config_change

2. **`docs/cli/activity-commands.md`**
   - Add multi-type filter examples
   - Document valid event types

3. **`docs/web-ui/activity-log.md`** (create if needed)
   - Document multi-select filter
   - Document Intent column
   - Document sortable columns

---

## 8. Testing & Validation

### Unit Tests

```bash
go test ./internal/storage/... -v -run TestActivity
go test ./internal/runtime/... -v -run TestActivity
go test ./internal/httpapi/... -v -run TestActivity
```

### E2E Tests

```bash
./scripts/test-api-e2e.sh
```

### Manual Testing

1. **System Events**: Start/stop MCPProxy and check activity log
2. **Internal Tools**: Call retrieve_tools via MCP client
3. **Config Changes**: Add/remove server via CLI
4. **Multi-Type Filter**: Test API with `?type=tool_call,config_change`
5. **Web UI**: Verify multi-select, Intent column, sorting

---

## Key Files Summary

| Component | Files |
|-----------|-------|
| Activity Types | `internal/storage/activity_models.go` |
| Event Types | `internal/runtime/events.go` |
| Event Emission | `internal/server/server.go`, `internal/server/mcp.go` |
| Event Handling | `internal/runtime/activity_service.go` |
| REST API | `internal/httpapi/activity.go` |
| CLI | `cmd/mcpproxy/activity_cmd.go` |
| Web UI | `frontend/src/views/Activity.vue`, `frontend/src/components/SidebarNav.vue` |
| Docs | `docs/features/activity-log.md`, `docs/cli/activity-commands.md` |
