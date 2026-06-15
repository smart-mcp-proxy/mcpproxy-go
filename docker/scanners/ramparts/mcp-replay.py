#!/usr/bin/env python3
"""Static MCP stdio "replay" server for the Ramparts scanner.

Ramparts v0.8.x is a *dynamic* scanner: it connects to a live MCP endpoint
(HTTP URL or `stdio:` subprocess), performs the MCP handshake, enumerates the
server's tools/resources/prompts, and runs its YARA rules + (optional) LLM
analysis on what the server advertises. It no longer scans a source directory.

MCPProxy's scanner framework, by contrast, mounts a read-only *source tree* at
`/scan/source` and — critically — also exports the tool definitions it already
captured from the running upstream into `/scan/source/tools.json` (the same file
the Cisco scanner consumes). We must NOT execute the untrusted upstream a second
time just to give Ramparts something to connect to (that would violate the
"never execute scanned package source" invariant, MCP-2206/#658).

This shim bridges the gap safely: it speaks just enough of the MCP stdio
protocol (newline-delimited JSON-RPC 2.0) to *replay* the already-captured tool
definitions to Ramparts. No upstream code runs; we only re-serve static JSON.

Ramparts launches us as `stdio:python3:/usr/local/bin/mcp-replay.py`, so this
file is invoked with the captured-tools path defaulting to /scan/source/tools.json
(overridable via $RAMPARTS_REPLAY_TOOLS for tests).
"""
import json
import os
import sys

# MCP protocol revision Ramparts' rmcp client initializes with (src/mcp_client.rs).
PROTOCOL_VERSION = "2025-06-18"
TOOLS_PATH = os.environ.get("RAMPARTS_REPLAY_TOOLS", "/scan/source/tools.json")


def log(msg):
    """Diagnostics go to stderr; stdout is reserved for JSON-RPC frames only."""
    print(f"[mcp-replay] {msg}", file=sys.stderr, flush=True)


class ToolsLoadError(Exception):
    """Raised when tools.json cannot be loaded into a usable tool list.

    This is the fail-CLOSED signal: rather than serve 0 tools (which makes
    Ramparts emit a spurious "clean" report and defeats the security gate —
    MCP-2443), the shim aborts so Ramparts sees a dead endpoint and the
    entrypoint marks the scan as failed.
    """


def load_tools():
    """Read the captured tool definitions. Returns a non-empty list of MCP
    tool objects.

    Fails CLOSED (raises ToolsLoadError) when the captured definitions are
    missing, unreadable, empty, not valid JSON, the wrong shape, or yield zero
    usable tools. mcpproxy only writes /scan/source/tools.json when it captured
    at least one named tool (Service.exportToolDefinitions), so any of these
    conditions means a broken/garbled input — NOT a legitimate clean scan.
    """
    try:
        with open(TOOLS_PATH, "r", encoding="utf-8") as fh:
            raw_bytes = fh.read()
    except OSError as exc:
        raise ToolsLoadError(f"could not read {TOOLS_PATH}: {exc}")

    if not raw_bytes.strip():
        raise ToolsLoadError(f"{TOOLS_PATH} is empty")

    try:
        data = json.loads(raw_bytes)
    except ValueError as exc:
        raise ToolsLoadError(f"{TOOLS_PATH} is not valid JSON: {exc}")

    if not isinstance(data, dict):
        raise ToolsLoadError(f"{TOOLS_PATH} root is {type(data).__name__}, expected an object")
    raw = data.get("tools")
    if not isinstance(raw, list):
        raise ToolsLoadError(f"{TOOLS_PATH} has no \"tools\" array (got {type(raw).__name__})")

    tools = []
    for entry in raw:
        if not isinstance(entry, dict) or not entry.get("name"):
            continue
        tool = {
            "name": entry["name"],
            "description": entry.get("description", "") or "",
            # MCP requires an inputSchema object; synthesize a permissive one
            # when the captured definition omitted it.
            "inputSchema": entry.get("inputSchema") or {"type": "object"},
        }
        if entry.get("annotations"):
            tool["annotations"] = entry["annotations"]
        tools.append(tool)

    if not tools:
        raise ToolsLoadError(f"{TOOLS_PATH} yielded 0 usable tools (none had a name)")
    return tools


def write_frame(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()


def reply(req_id, result):
    write_frame({"jsonrpc": "2.0", "id": req_id, "result": result})


def reply_error(req_id, code, message):
    write_frame({"jsonrpc": "2.0", "id": req_id, "error": {"code": code, "message": message}})


def handle(msg, tools):
    """Dispatch one decoded JSON-RPC message. Returns nothing; writes replies."""
    method = msg.get("method")
    req_id = msg.get("id")

    # Notifications (no id) never get a response.
    if req_id is None:
        return

    if method == "initialize":
        reply(req_id, {
            "protocolVersion": PROTOCOL_VERSION,
            "capabilities": {"tools": {}, "resources": {}, "prompts": {}},
            "serverInfo": {"name": "mcpproxy-ramparts-replay", "version": "1.0.0"},
        })
    elif method == "tools/list":
        reply(req_id, {"tools": tools})
    elif method == "resources/list":
        reply(req_id, {"resources": []})
    elif method == "resources/templates/list":
        reply(req_id, {"resourceTemplates": []})
    elif method == "prompts/list":
        reply(req_id, {"prompts": []})
    elif method == "ping":
        reply(req_id, {})
    else:
        # Unknown request: respond per JSON-RPC so the client doesn't hang.
        reply_error(req_id, -32601, f"method not found: {method}")


def main():
    try:
        tools = load_tools()
    except ToolsLoadError as exc:
        # Fail CLOSED: abort before the handshake so Ramparts cannot complete a
        # scan and report it clean. Exit code 2 distinguishes a load failure
        # from a normal exit; the entrypoint propagates this as a scan failure.
        log(f"FATAL: {exc}; aborting (fail-closed)")
        return 2
    log(f"serving {len(tools)} captured tool definition(s) from {TOOLS_PATH}")
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except ValueError as exc:
            log(f"skipping non-JSON line: {exc}")
            continue
        if isinstance(msg, list):  # JSON-RPC batch
            for item in msg:
                handle(item, tools)
        else:
            handle(msg, tools)
    log("stdin closed; exiting")
    return 0


if __name__ == "__main__":
    sys.exit(main())
