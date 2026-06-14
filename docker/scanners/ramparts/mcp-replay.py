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


def load_tools():
    """Read the captured tool definitions. Returns a list of MCP tool objects.

    Tolerant of a missing/empty/garbled file: an empty tool list still lets the
    handshake complete so Ramparts emits a (clean) report instead of a connect
    error.
    """
    try:
        with open(TOOLS_PATH, "r", encoding="utf-8") as fh:
            data = json.load(fh)
    except (OSError, ValueError) as exc:
        log(f"could not read {TOOLS_PATH}: {exc}; serving 0 tools")
        return []

    raw = data.get("tools", []) if isinstance(data, dict) else []
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
    tools = load_tools()
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
