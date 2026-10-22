#!/usr/bin/env node
// Verify the actual generated artifacts, not just the source glob settings.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const siteDir = path.resolve(__dirname, '..');
const buildDir = path.join(siteDir, 'build');
const metadataDir = path.join(siteDir, '.docusaurus/docusaurus-plugin-content-docs/default');
const normalize = value => new URL(value, 'https://docs.mcpproxy.app').pathname.replace(/\/$/, '') || '/';
const read = file => fs.readFileSync(path.join(buildDir, file), 'utf8');
const metadata = fs.readdirSync(metadataDir)
  .filter(file => file.endsWith('.json') && !file.startsWith('__'))
  .map(file => JSON.parse(fs.readFileSync(path.join(metadataDir, file), 'utf8')))
  .filter(doc => doc.source && doc.permalink && !doc.draft && !doc.unlisted);
assert(metadata.length > 0, 'No published document metadata found');
const published = new Set(metadata.map(doc => normalize(doc.permalink)));
const index = read('llms.txt');
const links = [...index.matchAll(/^- \[[^\n]*\]\((https:\/\/docs\.mcpproxy\.app[^)\s]*)\)/gm)].map(match => match[1]);
assert(links.length > 0, 'LLM index is empty');
const exported = new Set();
for (const link of links) {
  const route = normalize(link);
  assert(published.has(route), `LLM export links to unpublished page: ${link}`);
  assert(!exported.has(route), `Duplicate LLM export route: ${link}`);
  exported.add(route);
  const relative = route.replace(/^\//, '');
  assert([path.join(buildDir, relative, 'index.html'), path.join(buildDir, `${relative}.html`)]
    .some(file => fs.existsSync(file)), `LLM export has no built HTML page: ${link}`);
}
assert.deepEqual([...exported].sort(), [...published].sort(), 'LLM index must cover every published document');
const full = read('llms-full.txt');
assert(full.length > index.length, 'Full LLM export is empty or incomplete');
// Regressions from the original export: excluded docs had no HTML routes but
// their bodies (including agent workflow instructions) appeared in llms-full.
for (const marker of [
  'CRITICAL SECURITY VULNERABILITY: Skipped API Key Tests',
  'REQUIRED SUB-SKILL:',
  'TPA Scanner: Inline Findings & Background Scanning — Authoritative Design',
]) {
  assert(!full.includes(marker), `Unpublished content in full LLM export: ${marker}`);
}
assert(!/<loc>https:\/\/docs\.mcpproxy\.app\/search\/?<\/loc>/.test(read('sitemap.xml')), 'Noindexed search page is in sitemap');
assert(read('robots.txt').includes('Sitemap: https://docs.mcpproxy.app/sitemap.xml'), 'robots.txt is missing sitemap declaration');
for (const category of ['getting-started', 'configuration', 'cli', 'api', 'web-ui', 'features', 'operations']) {
  const html = fs.existsSync(path.join(buildDir, category, 'index.html')) ? read(`${category}/index.html`) : read(`${category}.html`);
  assert(/<meta[^>]*name="description"[^>]*content="[^"]+"/.test(html), `Missing category description: ${category}`);
}
console.log(`Verified ${exported.size} published LLM links, full-export regression markers, category metadata, sitemap and robots.txt.`);
