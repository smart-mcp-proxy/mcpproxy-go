// Shared secret-field resolution for Paste/Manual/Catalog add surfaces (Spec
// 109 FR-065): a field toggled to Secret gets written to the OS keyring under
// its refName and replaced with ${keyring:<ref>}; a field left as Value is
// passed through unchanged. On failure, only the secrets THIS call itself
// wrote are rolled back — an existing keyring entry from a previous add is
// never touched.
import api from '@/services/api'
import { refName } from '@/utils/secretRef'

export interface SecretField {
  kind: 'env' | 'header'
  name: string
  value: string
  mode: 'value' | 'secret'
}

export interface ResolvedSecretFields {
  env: Record<string, string>
  headers: Record<string, string>
  writtenRefs: string[]
}

/**
 * resolveSecretFields writes each 'secret'-mode field to the OS keyring and
 * returns env/headers maps ready to send to POST /servers (or PATCH), with
 * ${keyring:<ref>} in place of the raw value for secret fields.
 *
 * Throws (and rolls back everything it wrote so far) if any keyring write
 * fails, so the caller never ends up with a server config that references a
 * secret that was never actually stored.
 */
export async function resolveSecretFields(serverName: string, fields: SecretField[]): Promise<ResolvedSecretFields> {
  const env: Record<string, string> = {}
  const headers: Record<string, string> = {}
  const writtenRefs: string[] = []

  const valueFields = fields.filter((f) => f.mode === 'value')
  const secretFields = fields.filter((f) => f.mode === 'secret')

  for (const f of valueFields) {
    if (f.kind === 'env') env[f.name] = f.value
    else headers[f.name] = f.value
  }

  if (secretFields.length === 0) {
    return { env, headers, writtenRefs }
  }

  // One taken-name check up front (GET /secrets/refs), then tracked locally
  // as this call writes its own refs, so two fields in the same call that
  // would otherwise compute the same name get -2, not a silent collision.
  const refsResp = await api.getSecretRefs()
  const taken = new Set<string>()
  if (refsResp.success && refsResp.data) {
    for (const r of refsResp.data.refs) {
      if (r.type === 'keyring') taken.add(r.name)
    }
  }

  try {
    for (const f of secretFields) {
      const ref = refName(serverName, f.kind, f.name, (n) => taken.has(n))
      const stored = await api.setSecret(ref, f.value)
      if (!stored.success) {
        throw new Error(stored.error || `Failed to store secret for ${f.kind} ${f.name}`)
      }
      taken.add(ref)
      writtenRefs.push(ref)
      const placeholder = `\${keyring:${ref}}`
      if (f.kind === 'env') env[f.name] = placeholder
      else headers[f.name] = placeholder
    }
  } catch (err) {
    await rollbackSecrets(writtenRefs)
    throw err
  }

  return { env, headers, writtenRefs }
}

/** rollbackSecrets deletes only the refs THIS add wrote — never a pre-existing entry. */
export async function rollbackSecrets(refs: string[]): Promise<void> {
  await Promise.all(refs.map((ref) => api.deleteSecret(ref).catch(() => undefined)))
}
