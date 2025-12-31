# Quickstart: Activity Log Web UI

**Branch**: `019-activity-webui` | **Date**: 2025-12-29

## Overview

This guide provides step-by-step instructions for implementing the Activity Log Web UI feature. Follow these steps in order to build the feature incrementally.

## Prerequisites

- MCPProxy backend running with activity logging enabled (spec 016)
- Node.js 18.18+ installed
- Frontend development server running (`npm run dev` in `frontend/`)

## Implementation Steps

### Step 1: Add TypeScript Types

**File**: `frontend/src/types/api.ts`

Add the activity-related interfaces to the existing types file:

```typescript
// Activity Types (add to existing file)

export type ActivityType =
  | 'tool_call'
  | 'policy_decision'
  | 'quarantine_change'
  | 'server_change'

export type ActivitySource = 'mcp' | 'cli' | 'api'

export type ActivityStatus = 'success' | 'error' | 'blocked'

export interface ActivityRecord {
  id: string
  type: ActivityType
  source?: ActivitySource
  server_name?: string
  tool_name?: string
  arguments?: Record<string, any>
  response?: string
  response_truncated?: boolean
  status: ActivityStatus
  error_message?: string
  duration_ms?: number
  timestamp: string
  session_id?: string
  request_id?: string
  metadata?: Record<string, any>
}

export interface ActivityListResponse {
  activities: ActivityRecord[]
  total: number
  limit: number
  offset: number
}

export interface ActivityDetailResponse {
  activity: ActivityRecord
}

export interface ActivitySummaryResponse {
  period: string
  total_count: number
  success_count: number
  error_count: number
  blocked_count: number
  top_servers?: { name: string; count: number }[]
  top_tools?: { server: string; tool: string; count: number }[]
  start_time: string
  end_time: string
}
```

### Step 2: Add API Methods

**File**: `frontend/src/services/api.ts`

Add activity API methods to the existing api service:

```typescript
// Activity API Methods (add to existing api object)

// Get paginated activity list
async getActivities(params?: {
  type?: string
  server?: string
  status?: string
  start_time?: string
  end_time?: string
  limit?: number
  offset?: number
}): Promise<APIResponse<ActivityListResponse>> {
  const queryParams = new URLSearchParams()
  if (params) {
    Object.entries(params).forEach(([key, value]) => {
      if (value !== undefined && value !== '') {
        queryParams.append(key, String(value))
      }
    })
  }
  return this.get(`/api/v1/activity?${queryParams.toString()}`)
}

// Get single activity detail
async getActivityDetail(id: string): Promise<APIResponse<ActivityDetailResponse>> {
  return this.get(`/api/v1/activity/${id}`)
}

// Get activity summary
async getActivitySummary(period: string = '24h'): Promise<APIResponse<ActivitySummaryResponse>> {
  return this.get(`/api/v1/activity/summary?period=${period}`)
}

// Get export URL (returns URL string for download)
getActivityExportUrl(params: {
  format: 'json' | 'csv'
  type?: string
  server?: string
  status?: string
  start_time?: string
  end_time?: string
}): string {
  const queryParams = new URLSearchParams()
  queryParams.append('format', params.format)
  if (this.apiKey) {
    queryParams.append('apikey', this.apiKey)
  }
  Object.entries(params).forEach(([key, value]) => {
    if (key !== 'format' && value !== undefined && value !== '') {
      queryParams.append(key, String(value))
    }
  })
  return `${this.baseUrl}/api/v1/activity/export?${queryParams.toString()}`
}
```

### Step 3: Add SSE Event Listeners

**File**: `frontend/src/stores/system.ts`

Add activity event listeners to the existing SSE setup in `connectEventSource()`:

```typescript
// Activity SSE Events (add inside connectEventSource function)

// Listen for activity.tool_call.started events
es.addEventListener('activity.tool_call.started', (event) => {
  try {
    const data = JSON.parse(event.data)
    console.log('SSE activity.tool_call.started:', data)
    window.dispatchEvent(new CustomEvent('mcpproxy:activity-started', { detail: data }))
  } catch (error) {
    console.error('Failed to parse activity.tool_call.started event:', error)
  }
})

// Listen for activity.tool_call.completed events
es.addEventListener('activity.tool_call.completed', (event) => {
  try {
    const data = JSON.parse(event.data)
    console.log('SSE activity.tool_call.completed:', data)
    window.dispatchEvent(new CustomEvent('mcpproxy:activity-completed', { detail: data }))
  } catch (error) {
    console.error('Failed to parse activity.tool_call.completed event:', error)
  }
})

// Listen for activity.policy_decision events
es.addEventListener('activity.policy_decision', (event) => {
  try {
    const data = JSON.parse(event.data)
    console.log('SSE activity.policy_decision:', data)
    window.dispatchEvent(new CustomEvent('mcpproxy:activity-blocked', { detail: data }))
  } catch (error) {
    console.error('Failed to parse activity.policy_decision event:', error)
  }
})
```

### Step 4: Add Route

**File**: `frontend/src/router/index.ts`

Add the activity route to the router:

```typescript
// Add after the tool-calls route
{
  path: '/activity',
  name: 'activity',
  component: () => import('@/views/Activity.vue'),
  meta: {
    title: 'Activity Log',
  },
},
```

### Step 5: Add Navigation Link

**File**: `frontend/src/components/SidebarNav.vue`

Add the Activity Log link to the menu:

```typescript
// Add to menuItems array after Tool Call History
{ name: 'Activity Log', path: '/activity' },
```

### Step 6: Create Activity Page

**File**: `frontend/src/views/Activity.vue`

Create the main Activity Log page (see full implementation in tasks.md).

Key sections:
1. Page header with title
2. Filter bar (type, server, status, date range)
3. Activity table with columns: Time, Type, Server, Details, Status, Duration
4. Expandable detail rows
5. Pagination controls
6. Export button
7. Auto-refresh toggle

### Step 7: Create Dashboard Widget

**File**: `frontend/src/components/ActivityWidget.vue`

Create a compact activity summary widget for the dashboard.

Key sections:
1. Header with "Activity" title and "View All" link
2. Stats row: Total today, Success, Errors
3. List of 3 most recent activities

### Step 8: Add Widget to Dashboard

**File**: `frontend/src/views/Dashboard.vue`

Import and add the ActivityWidget component:

```vue
<script setup>
import ActivityWidget from '@/components/ActivityWidget.vue'
</script>

<template>
  <!-- Add after Token Distribution section -->
  <ActivityWidget />
</template>
```

### Step 9: Add Unit Tests

**File**: `frontend/tests/unit/activity.spec.ts`

Create tests for:
1. Activity type rendering
2. Status badge colors
3. Filter application
4. Pagination logic
5. Export URL generation
6. SSE event handling

### Step 10: Add Docusaurus Documentation

**File**: `docs/docs/web-ui/activity-log.md`

Document:
1. How to access the Activity Log page
2. Available filters and their usage
3. Real-time update behavior
4. Export functionality
5. Dashboard widget overview

## Verification Checklist

After implementation, verify:

- [ ] Activity Log page loads at `/ui/activity`
- [ ] Table displays activity records with all columns
- [ ] Filters work correctly (type, server, status, date range)
- [ ] Click on row opens detail panel
- [ ] Real-time updates appear via SSE
- [ ] Pagination works correctly
- [ ] Export downloads file in correct format
- [ ] Auto-refresh toggle controls SSE updates
- [ ] Dashboard widget shows summary and recent activities
- [ ] Navigation link appears in sidebar
- [ ] All unit tests pass
- [ ] Documentation is complete

## Testing with Playwriter

Use the `mcp__playwriter__execute` tool to verify the UI:

```javascript
// Navigate to Activity Log page
await page.goto('http://localhost:8080/ui/activity?apikey=YOUR_API_KEY')

// Verify page loaded
const title = await page.textContent('h1')
console.log('Page title:', title)

// Check table exists
const table = await page.locator('table')
console.log('Table found:', await table.isVisible())

// Test filter dropdown
await page.selectOption('select[name="type"]', 'tool_call')

// Verify filter applied
const rows = await page.locator('tbody tr').count()
console.log('Rows after filter:', rows)
```

## Common Issues

### SSE events not received

1. Check browser console for connection errors
2. Verify API key is passed to SSE endpoint
3. Check backend is emitting activity events

### Filters not working

1. Verify API params are correctly formatted
2. Check RFC3339 date format for time filters
3. Ensure server/tool values match backend data

### Export fails

1. Check API key is included in export URL
2. Verify Content-Disposition header is set
3. Test with smaller date range first
