// Post-build guard for the LLM bundles (Spec 121 FR-022/FR-023).
//
//   node scripts/check-llms.mjs [buildDir]
//
// docusaurus-plugin-llms walks the whole docs directory, so it once advertised
// ~70 internal pages whose URLs 404 on the live site. This check fails the docs
// build when
//   - llms.txt / llms-full.txt is missing,
//   - the agent install page is not the first entry of llms.txt,
//   - any llms.txt link does not resolve to a page that was actually built, or
//   - llms-full.txt exceeds the size budget agents can reasonably ingest.
import { existsSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const MAX_FULL_BYTES = 2 * 1024 * 1024; // 2 MiB (was ~3 MB with unpublished pages)
export const FIRST_PAGE = '/getting-started/agent-install';
const SITE = 'https://docs.mcpproxy.app';

/** @returns {string[]} problems; empty when the bundles are healthy. */
export function checkLlms(buildDir) {
  const problems = [];
  const txt = path.join(buildDir, 'llms.txt');
  const full = path.join(buildDir, 'llms-full.txt');
  if (!existsSync(txt)) problems.push('llms.txt was not generated');
  if (!existsSync(full)) problems.push('llms-full.txt was not generated');
  if (problems.length) return problems;

  const links = [...readFileSync(txt, 'utf8').matchAll(/^- \[[^\]]*\]\((https?:\/\/[^)\s]+)\)/gm)].map((m) => m[1]);
  if (links.length === 0) problems.push('llms.txt lists no pages');
  else if (new URL(links[0]).pathname.replace(/\/$/, '') !== FIRST_PAGE) {
    problems.push(`llms.txt must start with ${FIRST_PAGE}, starts with ${links[0]}`);
  }
  for (const link of links) {
    const u = new URL(link);
    if (`${u.protocol}//${u.host}` !== SITE) continue; // external links are not ours to verify
    const rel = u.pathname.replace(/^\/|\/$/g, '');
    const candidates = [path.join(buildDir, rel, 'index.html'), path.join(buildDir, `${rel}.html`)];
    if (!candidates.some((c) => existsSync(c))) {
      problems.push(`llms.txt links to ${link}, which is not a built page (404 on the live site)`);
    }
  }
  const size = statSync(full).size;
  if (size > MAX_FULL_BYTES) problems.push(`llms-full.txt is ${size} bytes, over the ${MAX_FULL_BYTES} byte budget`);
  return problems;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const dir = path.resolve(process.argv[2] ?? 'build');
  const problems = checkLlms(dir);
  if (problems.length) {
    console.error('check-llms: FAILED');
    for (const p of problems) console.error(`  - ${p}`);
    process.exit(1);
  }
  console.log(`check-llms: ok (${path.join(dir, 'llms.txt')})`);
}
