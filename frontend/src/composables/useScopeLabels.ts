import { useClientsStore } from '@/stores/clients'
import { useProfilesStore } from '@/stores/profiles'

// Spec 108-j J4: the human words for a profile/client/token filter value, in one
// place so a chip, an empty state ("No sessions for Client: Cursor") and a
// tooltip never disagree. A profile shows its title, a client its display name,
// a token its name, `-` reads "unattributed", and an unknown id falls back to
// the id itself so nothing renders empty.
export type ScopeName = 'profile' | 'client' | 'token'

const NAME_LABEL: Record<ScopeName, string> = { profile: 'Profile', client: 'Client', token: 'Token' }

export function useScopeLabels() {
  const profiles = useProfilesStore()
  const clients = useClientsStore()

  function valueLabel(name: ScopeName, value: string): string {
    if (value === '-') return 'unattributed'
    if (name === 'profile') return profiles.titleFor(value)
    if (name === 'client') return clients.clients.find(client => client.id === value)?.display_name ?? value
    return value
  }

  /** "Client: Cursor". */
  function chipLabel(name: ScopeName, value: string): string {
    return `${NAME_LABEL[name]}: ${valueLabel(name, value)}`
  }

  return { valueLabel, chipLabel }
}
