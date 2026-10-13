import { describe, it, expect } from 'vitest'
import {
  SERVER_EDITION_TAB_LABEL,
  SERVER_EDITION_SECTION_TITLE,
  SERVER_EDITION_FIELDS,
  SECURITY_FIELDS,
  hydrateConfigState,
  buildPartial,
  getPath,
} from '../../src/views/settings/fields'

// MCP-1087: the Settings server-edition surface must read "Server Edition",
// not the legacy "Teams" wording. MCP-1086: the backend config-key rename
// (`teams` -> `server_edition`) has landed, so the config *keys* are now on the
// canonical `server_edition.*` dot-paths. `Settings.vue` gates the tab on
// `server_edition` (with a `teams` fallback) and aliases a legacy `teams`-keyed
// config onto `server_edition` at load, so old configs still hydrate the form
// while edits always save under `server_edition`.
describe('Settings server-edition wording (MCP-1087)', () => {
  it('uses "Server Edition" wording with no "Teams" left in user-facing labels', () => {
    expect(SERVER_EDITION_TAB_LABEL).toBe('Server Edition')
    expect(SERVER_EDITION_TAB_LABEL).not.toMatch(/team/i)
    expect(SERVER_EDITION_SECTION_TITLE).not.toMatch(/team/i)
    expect(SERVER_EDITION_SECTION_TITLE).toMatch(/Server Edition/)
  })

  it('binds the config field keys to the canonical `server_edition.*` contract (MCP-1086)', () => {
    expect(SERVER_EDITION_FIELDS.length).toBeGreaterThan(0)
    for (const f of SERVER_EDITION_FIELDS) {
      expect(f.key).toMatch(/^server_edition\./)
    }
  })

  // Spec 107 (FR-035, US5): `max_user_servers` is a removed key. The backend
  // no longer reads it (it only emits a one-line startup warning when present),
  // so the Settings form must not offer a control that writes a dead key.
  it('does not expose the removed `server_edition.max_user_servers` key (Spec 107 T022)', () => {
    const keys = SERVER_EDITION_FIELDS.map((f) => f.key)
    expect(keys).not.toContain('server_edition.max_user_servers')
  })
})

// Spec 107 PR-B (T054, US2): the server-edition row set is asserted EXACTLY so
// an undecided key can never appear by accident. The `Settings` disposition
// paragraph of `specs/107-server-edition-sso-hardening/contracts/config-keys.md`
// is the authority: rows for the OIDC + front-door keys; Raw-JSON-only for
// `access.*`, `oauth.allow_insecure_issuer` (a loopback-only development
// toggle that must not look like a normal setting) and the retained keys with
// no row today; NEVER a row for the secrets (`oauth.client_secret`,
// `credential_encryption_key`) or the deprecated no-op `store_idp_tokens`.
describe('Settings server-edition row set (Spec 107 T054)', () => {
  const keys = () => SERVER_EDITION_FIELDS.map((f) => f.key)
  const byKey = (k: string) => SERVER_EDITION_FIELDS.find((f) => f.key === k)

  it('exposes exactly the contract row set, in catalogue order', () => {
    expect(keys()).toEqual([
      'server_edition.enabled',
      'server_edition.oauth.provider',
      'server_edition.oauth.display_name',
      'server_edition.oauth.issuer_url',
      'server_edition.oauth.scopes',
      'server_edition.oauth.groups_claim',
      'server_edition.oauth.email_verified_policy',
      'server_edition.public_url',
      'server_edition.session_cookie_secure',
    ])
  })

  it('never offers a row for a secret, the deprecated no-op, or a Raw-JSON-only key', () => {
    for (const k of [
      'server_edition.oauth.client_secret',
      'server_edition.credential_encryption_key',
      'server_edition.store_idp_tokens',
      'server_edition.oauth.allow_insecure_issuer',
      'server_edition.admin_emails',
      'server_edition.session_ttl',
      'server_edition.bearer_token_ttl',
      'server_edition.oauth.client_id',
      'server_edition.oauth.tenant_id',
      'server_edition.oauth.allowed_domains',
      'server_edition.access',
      'server_edition.access.group_servers',
      'server_edition.access.default_servers',
      'server_edition.max_user_servers',
      'server_edition.workspace_idle_timeout',
    ]) {
      expect(keys(), k).not.toContain(k)
    }
    // Belt and braces: no row's key mentions a secret by any spelling.
    for (const k of keys()) expect(k).not.toMatch(/secret|encryption_key/)
  })

  it('adds `oidc` to the provider family select', () => {
    const provider = byKey('server_edition.oauth.provider')
    expect(provider?.control).toBe('select')
    expect(provider?.options?.map((o) => o.value)).toEqual(['', 'google', 'github', 'microsoft', 'oidc'])
  })

  it('marks every server_edition row restart-pinned (all bound at login handler construction)', () => {
    for (const f of SERVER_EDITION_FIELDS) expect(f.restart, f.key).toBe(true)
  })

  it('types the OIDC rows per the contract', () => {
    const issuer = byKey('server_edition.oauth.issuer_url')
    expect(issuer?.control).toBe('text')
    expect(issuer?.valueKind).toBe('url')
    expect(issuer?.optional).toBe(true) // only required for provider=oidc — the backend refuses, not the form

    const scopes = byKey('server_edition.oauth.scopes')
    expect(scopes?.control).toBe('textarea')
    expect(scopes?.listKind).toBe('comma')

    expect(byKey('server_edition.oauth.groups_claim')?.control).toBe('text')
    expect(byKey('server_edition.oauth.display_name')?.control).toBe('text')

    const policy = byKey('server_edition.oauth.email_verified_policy')
    expect(policy?.control).toBe('select')
    expect(policy?.options?.map((o) => o.value)).toEqual(['refuse_false', 'require_true', 'ignore'])
  })

  it('types the front-door rows per the contract', () => {
    const pub = byKey('server_edition.public_url')
    expect(pub?.control).toBe('text')
    expect(pub?.valueKind).toBe('url')
    expect(pub?.optional).toBe(true)

    const cookie = byKey('server_edition.session_cookie_secure')
    expect(cookie?.control).toBe('select')
    expect(cookie?.options?.map((o) => o.value)).toEqual(['auto', 'true', 'false'])
  })
})

// `trusted_proxies` is edition-neutral (top-level, FR-027) and LIVE, so it is a
// Security & Access row — one CIDR or IP per line — not a server-edition row.
describe('Settings trusted_proxies row (Spec 107 T054)', () => {
  it('lives in the Security section as a live one-per-line textarea', () => {
    const f = SECURITY_FIELDS.find((x) => x.key === 'trusted_proxies')
    expect(f).toBeDefined()
    expect(f?.control).toBe('textarea')
    expect(f?.listKind).toBe('lines')
    expect(f?.restart).toBeFalsy()
    expect(SERVER_EDITION_FIELDS.map((x) => x.key)).not.toContain('trusted_proxies')
  })
})

// A `[]string` config key edited through a textarea must round-trip as a real
// JSON array: the form shows a joined string, the PATCH carries the split list.
// Without this the textarea would PATCH `"openid, profile"` (a string) into a
// `[]string` key and the server-edition decoder would refuse it.
describe('list-kind textarea round trip (Spec 107 T054)', () => {
  it('hydrates arrays as joined text on both working and original, leaving raw untouched', () => {
    const cfg = {
      trusted_proxies: ['10.0.0.0/8', '192.168.1.1'],
      server_edition: { oauth: { scopes: ['openid', 'email'] } },
    }
    const { working, original, raw } = hydrateConfigState(cfg)
    expect(getPath(working, 'trusted_proxies')).toBe('10.0.0.0/8\n192.168.1.1')
    expect(getPath(original, 'trusted_proxies')).toBe('10.0.0.0/8\n192.168.1.1')
    expect(getPath(working, 'server_edition.oauth.scopes')).toBe('openid, email')
    expect(getPath(original, 'server_edition.oauth.scopes')).toBe('openid, email')
    expect(raw.trusted_proxies).toEqual(['10.0.0.0/8', '192.168.1.1'])
    expect(raw.server_edition.oauth.scopes).toEqual(['openid', 'email'])
  })

  it('splits joined text back into a trimmed array in the PATCH partial', () => {
    const working = {
      trusted_proxies: ' 10.0.0.0/8 \n\n192.168.1.1\n',
      server_edition: { oauth: { scopes: 'openid, profile,email' } },
    }
    const partial = buildPartial(working, ['trusted_proxies', 'server_edition.oauth.scopes'])
    expect(partial.trusted_proxies).toEqual(['10.0.0.0/8', '192.168.1.1'])
    expect(partial.server_edition.oauth.scopes).toEqual(['openid', 'profile', 'email'])
  })

  it('sends an empty list, not a string, when the textarea is cleared', () => {
    const partial = buildPartial({ trusted_proxies: '' }, ['trusted_proxies'])
    expect(partial.trusted_proxies).toEqual([])
  })
})
