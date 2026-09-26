import Foundation

// MARK: - Secret field resolution (Spec 109 FR-065)
//
// Shared by the Add Server sheet's Catalog and Paste tabs: a field the user
// can leave as a plain Value or toggle to Secret. A Secret-mode field gets
// written to the OS keyring under its computed `SecretRefName` and replaced
// with `${keyring:<ref>}`; a Value-mode field is passed through unchanged.
// Swift mirror of the Web UI's `resolveSecretFields`
// (frontend/src/composables/useSecretFields.ts) — same two-phase shape (pure
// value-map assembly, then the keyring calls) so the assembly half is
// unit-testable without a running app or API client, matching this codebase's
// existing pattern (`ManualServerForm`'s pure `makeServerConfig` seam).

/// One field the Catalog/Paste tab lets the user fill in and toggle between
/// Value and Secret. `kind` picks which `POST /api/v1/servers` field it lands
/// in (`env` or `headers`) and which `SecretRefName.Kind` computes its
/// keyring ref name, so an env var and a header of the same name never
/// collide on one keyring entry (FR-065).
struct SecretFieldInput: Equatable, Identifiable {
    enum Kind: Equatable {
        case env
        case header
    }

    enum Mode: Equatable {
        case value
        case secret
    }

    /// Distinguishes an env field from a header field of the same name —
    /// `name` alone would collide.
    var id: String { "\(kind)-\(name)" }
    let name: String
    var kind: Kind = .env
    var value: String
    var mode: Mode
}

/// Result of resolving a set of `SecretFieldInput`s: the env/header maps
/// ready to send to `POST /api/v1/servers` (or the registry add endpoint),
/// plus the refs this call itself wrote to the keyring — the only ones a
/// failed add is allowed to roll back.
struct ResolvedSecretFields: Equatable {
    let env: [String: String]
    let headers: [String: String]
    let writtenRefs: [String]
}

enum SecretFieldResolver {
    /// Pure assembly of the env/header maps: a Value-mode field's literal
    /// value, or a `${keyring:<ref>}` placeholder for a field this call
    /// already wrote (`written` maps `field.id` -> the ref name `resolve`
    /// computed and stored for it). Extracted so the mapping logic — which
    /// field becomes a placeholder vs. a literal value, and which map it
    /// lands in — is testable without any network call.
    static func buildValues(fields: [SecretFieldInput], written: [String: String]) -> (env: [String: String], headers: [String: String]) {
        var env: [String: String] = [:]
        var headers: [String: String] = [:]
        for field in fields {
            let value = written[field.id].map { "${keyring:\($0)}" } ?? field.value
            switch field.kind {
            case .env: env[field.name] = value
            case .header: headers[field.name] = value
            }
        }
        return (env, headers)
    }

    /// Writes every `.secret`-mode field to the OS keyring (skipping the
    /// network round trip entirely when there are none) and returns the
    /// resolved env/header maps. On a keyring-write failure, rolls back only
    /// the refs this call itself wrote — never a pre-existing entry — and
    /// rethrows.
    static func resolve(client: APIClient, serverName: String, fields: [SecretFieldInput]) async throws -> ResolvedSecretFields {
        let secretFields = fields.filter { $0.mode == .secret }
        guard !secretFields.isEmpty else {
            let (env, headers) = buildValues(fields: fields, written: [:])
            return ResolvedSecretFields(env: env, headers: headers, writtenRefs: [])
        }

        // One taken-name check up front, then tracked locally as this call
        // writes its own refs, so two fields in the same call that would
        // otherwise compute the same name get -2, not a silent collision.
        var taken: Set<String> = []
        if let refs = try? await client.getSecretRefs() {
            taken = Set(refs.filter { $0.type == "keyring" }.map(\.name))
        }

        var written: [String: String] = [:] // field.id -> ref
        var writtenRefs: [String] = []

        do {
            for field in secretFields {
                let refKind: SecretRefName.Kind = field.kind == .header ? .header : .env
                let ref = SecretRefName.compute(server: serverName, kind: refKind, key: field.name, taken: { taken.contains($0) })
                _ = try await client.storeSecret(name: ref, value: field.value)
                taken.insert(ref)
                written[field.id] = ref
                writtenRefs.append(ref)
            }
        } catch {
            await rollback(client: client, refs: writtenRefs)
            throw error
        }

        let (env, headers) = buildValues(fields: fields, written: written)
        return ResolvedSecretFields(env: env, headers: headers, writtenRefs: writtenRefs)
    }

    /// Deletes only the refs THIS add wrote — never a pre-existing entry.
    /// Best-effort: a rollback failure is swallowed rather than compounding
    /// the original error the caller is already reporting.
    static func rollback(client: APIClient, refs: [String]) async {
        for ref in refs {
            _ = try? await client.deleteSecret(name: ref)
        }
    }
}
