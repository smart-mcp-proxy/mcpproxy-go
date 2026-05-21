# Tray capture shot-list (mcpproxy-ui-test MCP)

Goal: ~12 still frames of the macOS tray menu showing upstream servers + health,
to be assembled into a ~5s montage (beat 1 of the demo GIF).

Prereq: MCPProxy tray .app running with 3-4 healthy demo servers configured;
`mcpproxy-ui-test` MCP connected (bundle id com.smartmcpproxy.mcpproxy.dev).

Capture into /tmp/demo-tray/ as frame-01.png, frame-02.png, ... in this order:

1. frame-01..03: `screenshot_status_bar_menu` — closed → menu opening (3 shots)
2. frame-04..06: `list_menu_items` then `screenshot_status_bar_menu` with the
   "Upstream Servers" submenu expanded (servers + green health dots)
3. frame-07..09: hover/select a single server submenu (its tools count, status)
4. frame-10..12: `screenshot_status_bar_menu` returning to the top menu (close)

Naming MUST be zero-padded frame-NN.png so ffmpeg globbing is ordered.
mkdir -p /tmp/demo-tray before capturing.
