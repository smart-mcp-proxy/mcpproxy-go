#!/usr/bin/env node
// Zero-dependency stdio MCP upstream for the Spec 098 preflight sabotage matrix
// (T026/T027). It exists because every cell of the matrix needs an upstream the
// test can SABOTAGE deterministically, and no third-party server offers that:
//
//   FIXTURE_TOOLS_FILE   path to a JSON array of tool definitions. It is re-read
//                        on EVERY tools/list, which is what makes tool-definition
//                        drift (rug-pull) and post-baseline tool additions
//                        reproducible: rewrite the file, restart the server, and
//                        the proxy sees a changed/new definition.
//   FIXTURE_INIT_DELAY_MS  milliseconds to stall before answering `initialize`,
//                        which parks the proxy in its connecting/discovering
//                        state for the server_initializing cell.
//   FIXTURE_FAIL_FILE    when this path exists AT STARTUP the process exits
//                        immediately with a non-zero status. Creating the file
//                        and then killing the child makes the failure STICK
//                        across the proxy's automatic reconnects, so the
//                        server_unhealthy cell has a stable state to assert
//                        instead of a flapping one.
//
// Deliberately hand-rolled: MCP's stdio transport is newline-delimited JSON-RPC,
// so a dependency-free implementation keeps the E2E runnable with nothing but a
// `node` binary — no npm install, no network, no lockfile drift.
'use strict';

const fs = require('fs');

const toolsFile = process.env.FIXTURE_TOOLS_FILE || '';
const failFile = process.env.FIXTURE_FAIL_FILE || '';
const initDelayMs = Number.parseInt(process.env.FIXTURE_INIT_DELAY_MS || '0', 10) || 0;

if (failFile && fs.existsSync(failFile)) {
  process.stderr.write('[preflight-fixture] fail switch present, exiting\n');
  process.exit(1);
}

function loadTools() {
  if (!toolsFile) {
    return [];
  }
  try {
    const parsed = JSON.parse(fs.readFileSync(toolsFile, 'utf8'));
    return Array.isArray(parsed) ? parsed : [];
  } catch (err) {
    process.stderr.write(`[preflight-fixture] tools file unreadable: ${err}\n`);
    return [];
  }
}

function send(message) {
  process.stdout.write(`${JSON.stringify(message)}\n`);
}

function reply(id, result) {
  send({ jsonrpc: '2.0', id, result });
}

function handle(message) {
  // Notifications carry no id and expect no response.
  if (message.id === undefined || message.id === null) {
    return;
  }

  switch (message.method) {
    case 'initialize': {
      const version = (message.params && message.params.protocolVersion) || '2024-11-05';
      const answer = () =>
        reply(message.id, {
          protocolVersion: version,
          capabilities: { tools: { listChanged: true } },
          serverInfo: { name: 'preflight-fixture', version: '1.0.0' },
        });
      if (initDelayMs > 0) {
        setTimeout(answer, initDelayMs);
      } else {
        answer();
      }
      break;
    }
    case 'ping':
      reply(message.id, {});
      break;
    case 'tools/list':
      reply(message.id, { tools: loadTools() });
      break;
    case 'tools/call':
      reply(message.id, {
        content: [
          {
            type: 'text',
            text: JSON.stringify({ tool: message.params && message.params.name, ok: true }),
          },
        ],
      });
      break;
    case 'resources/list':
      reply(message.id, { resources: [] });
      break;
    case 'prompts/list':
      reply(message.id, { prompts: [] });
      break;
    default:
      send({
        jsonrpc: '2.0',
        id: message.id,
        error: { code: -32601, message: `Method not found: ${message.method}` },
      });
  }
}

let buffer = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', (chunk) => {
  buffer += chunk;
  let newline = buffer.indexOf('\n');
  while (newline >= 0) {
    const line = buffer.slice(0, newline).trim();
    buffer = buffer.slice(newline + 1);
    if (line) {
      try {
        handle(JSON.parse(line));
      } catch (err) {
        process.stderr.write(`[preflight-fixture] bad frame: ${err}\n`);
      }
    }
    newline = buffer.indexOf('\n');
  }
});
process.stdin.on('end', () => process.exit(0));

process.stderr.write(`[preflight-fixture] ready pid=${process.pid} tools=${toolsFile}\n`);
