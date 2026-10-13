import { describe, it, expect } from 'vitest'
import { activityAgentNames, matchesAuthFilter } from '@/utils/activity'

// Spec 028 Auth Type / Agent filters. They used to read
// `metadata._auth_auth_type` / `metadata._auth_agent_name`, which the REST
// payload never carries (the identity lives in internal `_auth_*` argument keys
// the server strips). The Agent dropdown was always empty and Auth Type = Agent
// hid every row while the KPI tiles still counted them. The API now surfaces
// the identity as top-level `auth_type` / `agent_name`.

const rows = [
  { id: '1', auth_type: 'agent', agent_name: 'ci-bot' },
  { id: '2', auth_type: 'agent', agent_name: 'alpha' },
  { id: '3', auth_type: 'admin' },
  { id: '4' }, // written without an auth context (CLI, internal)
  { id: '5', auth_type: 'agent', agent_name: 'ci-bot' },
  { id: '7', auth_type: 'admin_user' }, // server edition: OAuth-authenticated admin
  // The old, never-populated location must not be what the filter reads.
  { id: '6', metadata: { _auth_auth_type: 'agent', _auth_agent_name: 'ghost' } },
]

const ids = (authType: string, agentName: string) =>
  rows.filter(r => matchesAuthFilter(r, authType, agentName)).map(r => r.id)

describe('Activity auth filters (Spec 028)', () => {
  it('lists each agent once, sorted, from the top-level agent_name', () => {
    expect(activityAgentNames(rows)).toEqual(['alpha', 'ci-bot'])
  })

  it('Auth Type = Agent keeps the agent rows', () => {
    expect(ids('agent', '')).toEqual(['1', '2', '5'])
  })

  it('Auth Type = Admin keeps API-key and OAuth (server edition) admin rows', () => {
    expect(ids('admin', '')).toEqual(['3', '7'])
  })

  it('an agent name narrows to that agent', () => {
    expect(ids('agent', 'ci-bot')).toEqual(['1', '5'])
  })

  it('no auth filter keeps everything', () => {
    expect(ids('', '')).toEqual(rows.map(r => r.id))
  })
})
