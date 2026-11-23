// Self-contained MCP SDK OAuth client for the Spec 132 T044 check. Not a real client's
// config: it only runs against the scratch core given by BASE/KEY. The browser step is
// replaced by the admin REST approve call (no human here).
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StreamableHTTPClientTransport } from '@modelcontextprotocol/sdk/client/streamableHttp.js';
import { UnauthorizedError } from '@modelcontextprotocol/sdk/client/auth.js';

const BASE = process.env.BASE;
const KEY = process.env.KEY;
const ADDR = process.argv[2]; // e.g. /mcp/oauth/cursor
const REDIRECT = process.argv[3] || 'http://127.0.0.1:54321/callback';
const NAME = process.argv[4] || 'sdk-client';
const mask = (s) => String(s).replace(/mcp_o[ar]t_[A-Za-z0-9_-]+/g, '<token>').replace(/(code_verifier|code|refresh_token)=[^&\s"]+/g, '$1=<x>');

let info, tokens, verifier, authCode;
const log = (...a) => console.log(...a);

const loggedFetch = async (url, init = {}) => {
  const u = String(url);
  const body = init.body ? mask(init.body instanceof URLSearchParams ? init.body.toString() : init.body) : '';
  const res = await fetch(url, init);
  log(`  ${init.method || 'GET'} ${mask(u)} -> ${res.status}${body ? '  body: ' + body.slice(0, 300) : ''}`);
  return res;
};

const provider = {
  get redirectUrl() { return REDIRECT; },
  get clientMetadata() {
    return {
      client_name: NAME,
      redirect_uris: [REDIRECT],
      grant_types: ['authorization_code', 'refresh_token'],
      response_types: ['code'],
      token_endpoint_auth_method: 'none',
    };
  },
  clientInformation: () => info,
  saveClientInformation: (i) => { info = i; log('  saved client registration, client_id present:', !!i.client_id); },
  tokens: () => tokens,
  saveTokens: (t) => { tokens = t; log('  saved tokens: token_type=%s expires_in=%s refresh=%s', t.token_type, t.expires_in, !!t.refresh_token); },
  saveCodeVerifier: (v) => { verifier = v; },
  codeVerifier: () => verifier,
  redirectToAuthorization: async (authUrl) => {
    log('  authorize URL params:', mask(JSON.stringify(Object.fromEntries(authUrl.searchParams))));
    const r = await fetch(authUrl, { redirect: 'manual' });
    const loc = r.headers.get('location');
    log('  authorize ->', r.status, mask(loc));
    const rid = new URL(loc).searchParams.get('request');
    const a = await fetch(`${BASE}/api/v1/oauth/authorizations/${rid}`, { headers: { 'X-API-Key': KEY } });
    const aj = await a.json();
    log('  consent request: reported_name=%s address_kind=%s resource=%s', aj.data.reported_name, aj.data.address_kind, aj.data.resource);
    const body = aj.data.address_kind === 'generic'
      ? { client: { new: { id: 'sdk-client', display_name: 'SDK client' } }, mode: 'switchable', profile: '' }
      : { client: { id: aj.data.client.id }, mode: 'switchable', profile: '', ...(aj.data.client.binding ? { binding_revision: aj.data.client.binding.revision } : {}) };
    const p = await fetch(`${BASE}/api/v1/oauth/authorizations/${rid}/approve`, { method: 'POST', headers: { 'X-API-Key': KEY, 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    const pj = await p.json();
    log('  approve (admin REST, no browser) ->', p.status);
    authCode = new URL(pj.data.redirect_url).searchParams.get('code');
  },
};

const mk = () => new StreamableHTTPClientTransport(new URL(BASE + ADDR), { authProvider: provider, fetch: loggedFetch });
let transport = mk();
let client = new Client({ name: NAME, version: '1.0.0' });
try {
  await client.connect(transport);
  log('connected without auth?!');
} catch (e) {
  if (!(e instanceof UnauthorizedError)) { log('connect error:', e.message); process.exit(1); }
  log('UnauthorizedError -> finishing auth with code');
}
await transport.finishAuth(authCode);
transport = mk();
client = new Client({ name: NAME, version: '1.0.0' });
await client.connect(transport);
const tl = await client.listTools();
log('tools/list OK:', tl.tools.map((t) => t.name).join(','));
// refresh through the SDK path: expire the token locally and call again
tokens = { ...tokens, access_token: 'expired' };
transport = mk();
client = new Client({ name: NAME, version: '1.0.0' });
try {
  await client.connect(transport);
  const tl2 = await client.listTools();
  log('after bad access token, SDK refreshed and tools/list OK:', tl2.tools.length, 'tools');
} catch (e) {
  log('after bad access token: error', e.constructor.name, e.message);
}
await client.close();
process.exit(0);
