import { describe, expect, it } from 'vitest'
import { credentialLifecycleLinks, credentialLifecycleSummary, profileChangeActor, profileChangeLabel } from '@/utils/activity'

// Spec 115 UI-006/T066: profile_change issue/revoke records read as prose
// with the actor and surface, and link the identity and the profile.
const issueClient = { change: 'issue', client_id: 'delegated-worker', token_name: 'client-delegated-worker', profile: 'daily-research', actor_kind: 'api_key', surface: 'mcp', diff: { credential_kind: 'client' } }
const revokeToken = { change: 'revoke', token_name: 'research-task-42', profile: 'daily-research', actor_kind: 'api_key', surface: 'mcp', diff: { credential_kind: 'token' } }

describe('credential lifecycle activity', () => {
  it('labels', () => {
    expect(profileChangeLabel(issueClient)).toBe('Issued client credential')
    expect(profileChangeLabel(revokeToken)).toBe('Revoked token')
    expect(profileChangeLabel({ change: 'issue', token_name: 't', diff: { credential_kind: 'token' } })).toBe('Issued token')
    expect(profileChangeLabel({ change: 'revoke', client_id: 'w1' })).toBe('Revoked client credential')
  })
  it('actor and summary', () => {
    expect(profileChangeActor(issueClient)).toBe('via MCP · api_key')
    expect(credentialLifecycleSummary(issueClient)).toBe('Issued client credential delegated-worker → daily-research (via MCP · api_key)')
    expect(credentialLifecycleSummary({ change: 'assign', client_id: 'x' })).toBe('')
  })
  it('links go to the filtered identity view and the profile', () => {
    expect(credentialLifecycleLinks(issueClient)).toEqual({ identity: { path: '/clients', query: { client: 'delegated-worker' } }, profile: 'daily-research' })
    expect(credentialLifecycleLinks(revokeToken).identity).toEqual({ path: '/clients', query: { tab: 'tokens', token: 'research-task-42' } })
  })
  it('never renders a secret-looking value from metadata it does not read', () => {
    const withExtra = { ...issueClient, diff: { ...issueClient.diff, token_prefix: 'mcp_cli_ab12' } }
    expect(credentialLifecycleSummary(withExtra)).not.toContain('mcp_cli_')
  })
})
