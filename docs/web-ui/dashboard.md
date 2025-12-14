---
id: dashboard
title: Web Dashboard
sidebar_label: Dashboard
sidebar_position: 1
description: MCPProxy web management interface
keywords: [web, ui, dashboard, management]
---

# Web Dashboard

MCPProxy includes a web-based management dashboard for monitoring and configuring your MCP servers.

## Accessing the Dashboard

The dashboard is available at:

```
http://127.0.0.1:8080/ui/
```

If API key authentication is enabled, append the key as a query parameter:

```
http://127.0.0.1:8080/ui/?apikey=your-api-key
```

The tray application opens the dashboard with the API key automatically.

## Features

### Server Overview

The main dashboard shows:
- Total connected servers
- Number of available tools
- Server health status
- Recent activity

### Server Management

#### Server List

View all configured upstream servers with:
- Connection status (connected, disconnected, error)
- Protocol type (stdio, HTTP)
- Enabled/disabled state
- Tool count
- Quarantine status

#### Add Server

Click "Add Server" to configure a new upstream:

1. Enter server name (unique identifier)
2. Select protocol (stdio or HTTP)
3. Configure connection details
4. Set optional parameters
5. Click Save

#### Server Details

Click a server to view:
- Connection logs
- Available tools
- Configuration details
- OAuth status (if applicable)

### Tool Search

Use the search bar to find tools across all servers:

1. Enter keywords (e.g., "create file")
2. View matching tools with descriptions
3. See which server provides each tool

### Quarantine Management

The quarantine panel shows:
- Servers pending approval
- Security analysis for each
- Approve/Reject buttons

New servers added via AI clients are automatically quarantined for security review.

### OAuth Status

For OAuth-enabled servers:
- Current authentication status
- Token expiration
- Re-authenticate button

## Real-time Updates

The dashboard updates automatically via Server-Sent Events (SSE):
- Server status changes
- Tool availability updates
- Configuration changes

No page refresh needed.

## Dark Mode

The dashboard supports both light and dark themes, matching your system preference.

## Mobile Support

The dashboard is responsive and works on mobile devices:
- Collapsible navigation
- Touch-friendly controls
- Optimized layouts
