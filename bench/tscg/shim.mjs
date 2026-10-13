// TSCG encoding shim for the mcpproxy discovery bench (spec 083, research D3).
//
// Protocol (JSONL, one JSON object per line, both directions):
//   stdin:  {"tool_id": "<id>", "tool": {"name": ..., "description": ..., "input_schema": {...}}}
//   stdout: {"tool_id": "<id>", "encoded": "<compressed text>"}
//        or {"tool_id": "<id>", "error": "<message>"}
//
// The encoded text travels as an escaped JSON string, so embedded newlines
// cannot split or merge records. The shim reads stdin to EOF, emits one output
// record per input record (in input order), then exits 0. Any record-level
// failure is reported per-record; only protocol-level failures (unparseable
// stdin) exit non-zero.
//
// Encoding is @tscg/core@1.4.3 `compressToolSchema` with the balanced profile
// and a pinned model target — TSCG is a pure deterministic compiler, and the
// options are fixed here so identical input bytes always produce identical
// output bytes (FR-010).

import { compressToolSchema } from '@tscg/core';
import { createInterface } from 'node:readline';

const OPTIONS = Object.freeze({ model: 'claude-sonnet', profile: 'balanced' });

const rl = createInterface({ input: process.stdin, terminal: false });

const out = (obj) => process.stdout.write(JSON.stringify(obj) + '\n');

rl.on('line', (line) => {
  const trimmed = line.trim();
  if (trimmed === '') return;

  let record;
  try {
    record = JSON.parse(trimmed);
  } catch (err) {
    // Unparseable input line is a protocol failure: without a tool_id there is
    // no record to attribute the error to.
    process.stderr.write(`tscg shim: unparseable input line: ${err.message}\n`);
    process.exitCode = 1;
    return;
  }

  const toolID = record.tool_id;
  if (typeof toolID !== 'string' || toolID === '') {
    process.stderr.write('tscg shim: input record missing tool_id\n');
    process.exitCode = 1;
    return;
  }

  try {
    if (record.tool === null || typeof record.tool !== 'object') {
      throw new Error('record has no tool object');
    }
    const result = compressToolSchema(record.tool, OPTIONS);
    if (typeof result.compressed !== 'string' || result.compressed === '') {
      throw new Error('compressToolSchema returned empty output');
    }
    out({ tool_id: toolID, encoded: result.compressed });
  } catch (err) {
    out({ tool_id: toolID, error: String(err && err.message ? err.message : err) });
  }
});
