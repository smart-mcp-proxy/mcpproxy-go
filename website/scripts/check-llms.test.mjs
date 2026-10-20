// node --test scripts/check-llms.test.mjs   (Spec 121 T043)
import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { checkLlms, MAX_FULL_BYTES } from './check-llms.mjs';

function fixture({ links, full = 'x', pages = [] }) {
  const dir = mkdtempSync(path.join(tmpdir(), 'llms-'));
  writeFileSync(path.join(dir, 'llms.txt'), links.map((l) => `- [t](${l}): d`).join('\n'));
  writeFileSync(path.join(dir, 'llms-full.txt'), full);
  for (const p of pages) {
    mkdirSync(path.join(dir, p), { recursive: true });
    writeFileSync(path.join(dir, p, 'index.html'), '<html></html>');
  }
  return dir;
}
const S = 'https://docs.mcpproxy.app';

test('healthy bundle passes', () => {
  const dir = fixture({ links: [`${S}/getting-started/agent-install`, `${S}/cli/x`], pages: ['getting-started/agent-install', 'cli/x'] });
  assert.deepEqual(checkLlms(dir), []);
});
test('install page must be first', () => {
  const dir = fixture({ links: [`${S}/cli/x`, `${S}/getting-started/agent-install`], pages: ['cli/x', 'getting-started/agent-install'] });
  assert.match(checkLlms(dir).join('\n'), /must start with/);
});
test('a link to an unpublished page fails (the 404 class)', () => {
  const dir = fixture({ links: [`${S}/getting-started/agent-install`, `${S}/docs/superpowers/plans/x`], pages: ['getting-started/agent-install'] });
  assert.match(checkLlms(dir).join('\n'), /not a built page/);
});
test('oversized llms-full fails', () => {
  const dir = fixture({ links: [`${S}/getting-started/agent-install`], pages: ['getting-started/agent-install'], full: 'x'.repeat(MAX_FULL_BYTES + 1) });
  assert.match(checkLlms(dir).join('\n'), /budget/);
});
test('missing files fail', () => {
  const dir = mkdtempSync(path.join(tmpdir(), 'llms-'));
  assert.match(checkLlms(dir).join('\n'), /not generated/);
});
