---
id: oauth-token-encryption
title: OAuth Token Encryption
sidebar_label: OAuth Token Encryption
sidebar_position: 6
description: Encrypt the upstream OAuth tokens stored in config.db with a key you supply, and which key delivery protects against what.
keywords: [oauth, token, encryption, config.db, secrets, systemd, docker secrets, kubernetes]
---

# OAuth Token Encryption

MCPProxy keeps the OAuth tokens of your upstream servers (access token, refresh
token and the dynamic client registration secret) in `config.db` in the data
directory. By default they are stored as readable JSON and are protected only by
the file permissions (`0600` file, `0700` directory). Anyone who can read a copy
of `config.db` can read the tokens.

Set `oauth_token_encryption_key` and the token rows are encrypted with
AES-256-GCM before they are written. The feature is off until you set a key.

## Threat model

| Protects against | Does not protect against |
|------------------|--------------------------|
| A copy of `config.db` or of the data directory (backups, support bundles, a synced folder, a disk image) when the key is not stored in it | A process that runs as the same user as mcpproxy. It can read the key from the same place mcpproxy does, or from `/proc/<pid>/environ` of the daemon. A stdio server started under `none` or `sandbox` isolation is such a process (see [What the sandbox does not hide](sandbox-isolation.md#what-the-sandbox-does-not-hide)) |
| A copy of the disk or a VM snapshot, **only when the key is not on that disk in readable form** (see the table below) | A snapshot that includes memory: the running process holds the key |
| | Old backups made before encryption was switched on: they stay plaintext |

The encryption is a file-at-rest control. It does not change which processes can
call mcpproxy's API or its admin socket.

## Which key delivery protects against what

Where the key lives decides what the encryption buys you.

Protection against a copy of `config.db` or of the data directory holds only when
the key source is **not part of that copy**. A key file stored inside the data
directory, or any copy that includes the key, gives no protection at all.

| Delivery | Copy of `config.db` / data dir (key source not in the copy) | Disk or VM disk snapshot |
|----------|:------------------------------:|:------------------------:|
| `LoadCredentialEncrypted=` bound to a **TPM2** | yes | yes |
| `LoadCredentialEncrypted=` wrapped with the **host key only** (`/var/lib/systemd/credential.secret`) | yes | **no**: the wrapping secret is on the same disk, so a whole-disk copy yields both |
| tmpfs-backed Kubernetes or Swarm secret, cloud secret store, a key supplied by an external supervisor | yes | yes, as long as the key is not written to the captured storage |
| `LoadCredential=` from a plain file outside the data directory, a Docker Compose `file:` secret bind-mounted from the host | yes | **no**: the key file is in the snapshot |
| A key file stored inside the data directory, or next to it in a copy that includes it | **no**: the copy holds the database and the key | **no** |
| `${keyring:NAME}` on a desktop | yes | depends: only if the captured disk does not also hold the keyring and the material that unlocks it (for example the login keychain and the user's login password) |
| Key as a literal in `mcp_config.json` | **no** when the config file is copied together with the database; yes when only `config.db` is copied | no |

A snapshot taken with memory also contains the running key, whatever the delivery.

## Generating a key

The key is 32 random bytes, base64-encoded (standard alphabet).

```bash
# Create /etc/mcpproxy only if it is missing; an existing one is left alone
# (the Linux package sets it root:mcpproxy 2770 and the daemon writes there).
[ -d /etc/mcpproxy ] || sudo install -d -m 0755 /etc/mcpproxy
# Only the key file gets its own ownership and mode.
( umask 077; openssl rand -base64 32 | sudo tee /etc/mcpproxy/oauth-key >/dev/null )
sudo chmod 600 /etc/mcpproxy/oauth-key
```

Who must be able to read the file depends on the delivery. With systemd
`LoadCredential=` or `LoadCredentialEncrypted=` systemd (root) reads it and hands
the daemon a private copy, so a root-owned `0600` file is right. When you point
`${file:...}` or `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE` at the file directly,
the service user that runs mcpproxy must be able to read it, and nobody else:
`sudo chown <service user> /etc/mcpproxy/oauth-key` with mode `0600`, or
`sudo chown root:<group> ...` with mode `0640` for a group that contains only that
user. For the Linux package that is `sudo chown mcpproxy /etc/mcpproxy/oauth-key`.
Change the key file only, never the mode or owner of `/etc/mcpproxy` itself.

Keep the file **outside** `data_dir`. A key stored next to `config.db` protects
nothing if the directory is copied.

## Configuring the key

The key can come from three places. The environment wins over the config field.

1. `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY`: the key itself.
2. `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE`: an absolute path of a file that
   holds the key. A symlink is followed (Kubernetes secret volumes are symlinks).
   Set one of the two variables, never both: mcpproxy exits with code 4.
3. The config field `oauth_token_encryption_key`: a literal key, or one
   reference of the form `${file:/absolute/path}`, `${env:NAME}` or
   `${keyring:NAME}`. Prefer a reference. A literal is accepted but ends up in the
   config file.

```json
{
  "oauth_token_encryption_key": "${file:/etc/mcpproxy/oauth-key}"
}
```

`GET /api/v1/config`, the Web UI and every other config output always mask a
literal key, whatever `reveal_secret_headers` says. A reference
(`${file:...}`, `${env:...}`, `${keyring:...}`) is shown exactly as written,
because it is operator-authored and not secret. When the masked document is sent
back (raw-JSON editor, `PATCH`), the stored value is restored, never the mask.

### systemd

```ini
[Service]
# plain file: protects copies of config.db, not a disk snapshot
LoadCredential=oauth-key:/etc/mcpproxy/oauth-key
Environment=MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE=%d/oauth-key
```

For a snapshot-resistant setup bind the credential to the TPM
(`systemd-creds encrypt --with-key=tpm2 ...`) and use
`LoadCredentialEncrypted=oauth-key:/etc/mcpproxy/oauth-key.cred`. Once the
encrypted credential works (start the service and check the log shows the key id),
**delete the plaintext key file** (`shred -u /etc/mcpproxy/oauth-key`, or `rm` on a
filesystem where shredding does not apply) and remove it from any backup or copy
you made: a plaintext key left on the disk makes a snapshot as readable as before.
A credential encrypted with the host key only does not help against a whole-disk copy. The
config form `"oauth_token_encryption_key": "${file:/run/credentials/mcpproxy.service/oauth-key}"`
works as well.

### Docker and Docker Compose

```yaml
services:
  mcpproxy:
    environment:
      MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE: /run/secrets/oauth_key
    secrets: [oauth_key]
    volumes:
      - mcpproxy-data:/data
secrets:
  oauth_key:
    file: ./oauth-key.txt   # on the host, not in the data volume
```

A Compose `file:` secret is bind-mounted from the host, so it protects a copy of
the data volume but not a snapshot of the host disk.

### Kubernetes

Mount a `Secret` as a volume and point `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE`
at the file. Secret volumes are tmpfs-backed and symlinked; both are handled.

### Environment and desktop

`MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY=<key>` works as a fallback, with the usual
caveat that the environment is readable by the same user. On a desktop store the
key in the OS keyring and use `"${keyring:oauth_token_key}"` (see
[Keyring integration](keyring-integration.md)).

## Switching it on

1. Generate the key and configure it as above.
2. Restart mcpproxy. At start it either encrypts every valid plaintext OAuth row
   in one transaction and logs the key id and the number of rows, or it refuses to
   start (the transaction is rolled back and every row stays as it was). Nothing
   needs signing in again. A row that cannot be sealed for a reason of its own
   (not a valid token record, or larger than 1 MiB) is logged and left unchanged;
   it does not stop startup, and it stays plaintext until it is removed with
   `mcpproxy auth logout --server=<server>`.
3. The old plaintext pages stay inside the database file until it is rewritten.
   Stop mcpproxy, run `mcpproxy db compact --keep-backup=false`, start it again.
   For the Linux package, run it as the service user with the service's paths:

   ```bash
   sudo systemctl stop mcpproxy
   sudo -u mcpproxy mcpproxy --config=/etc/mcpproxy/mcp_config.json \
     --data-dir=/var/lib/mcpproxy db compact --keep-backup=false
   sudo systemctl start mcpproxy
   ```
4. Delete any `config.db.bak` and any older copy of `config.db`. They still hold
   the tokens in the clear.

After step 3 the raw file contains no token text from the rows that were
encrypted. Check the startup log for rows reported as left unchanged. Rows written
while a key is set are encrypted from the first write.

## What happens when the key is wrong or missing

| Situation | Result |
|-----------|--------|
| The key reference cannot be read (missing mount, unreadable file, empty value, not 32 bytes) | mcpproxy does not start, and every command that opens `config.db` through the key-aware storage helpers stops, with exit code 4 and a message naming the field or variable. `config.db` is not opened and not changed. Commands that only talk to a running daemon over the socket or HTTP are unaffected. Standalone `auth logout` proceeds without the key only when the key source is missing or unreadable; a source that is present but unusable (empty, oversized, malformed, both variables set, or a bad reference) still exits 4 (see below). `mcpproxy db compact` and `mcpproxy db stats` open the file directly with bbolt and do not resolve the key: they copy or count bytes and never decode token rows |
| The key is removed from the configuration of a database that has encrypted rows | mcpproxy starts. The affected servers show `unhealthy` with [`MCPX_OAUTH_TOKEN_UNDECRYPTABLE`](../errors/MCPX_OAUTH_TOKEN_UNDECRYPTABLE.md). The rows are kept byte for byte and no sign-in is offered |
| A different key is configured | Same as above; the message gives the key ids (a short fingerprint, never key material) |
| An encrypted entry was changed or copied from another server | It fails authentication and is refused. Restoring the key does not repair it: restore the original value from a backup, or run `mcpproxy auth logout --server=<server>` and sign in again |

Restoring the original key and restarting recovers the missing-key and wrong-key
cases only: those servers are authenticated again, with no browser. A changed or
swapped entry stays refused whatever the key; restore its original value or log it
out and sign in again. If the key is lost for good, run `mcpproxy auth logout --server=<server>`
for each affected server and sign in again. With mcpproxy stopped, `auth logout`
works when the key source is missing or unreadable (it deletes the stored entry and
needs no key); a source that is present but unusable (empty, oversized, malformed,
both variables set, or a bad reference) still stops it with exit 4. For the Linux
package run it as the service user with the service's paths:

```bash
sudo systemctl stop mcpproxy
sudo -u mcpproxy mcpproxy --config=/etc/mcpproxy/mcp_config.json \
  --data-dir=/var/lib/mcpproxy auth logout --server=<server>
sudo systemctl start mcpproxy
```

`auth login` and starting mcpproxy still need a readable key, so
before signing in, configure a new key (see "Generating a key" above, in the config
field or through `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY[_FILE]`). Any entry still
encrypted under the lost key then shows as undecryptable until you log it out.

Rows that cannot be read are never overwritten by a login and are never removed by
the orphan cleanup, so a wrong key cannot destroy the tokens.

## Notes

- Changing the key takes effect at the next start. It is a restart-gated field.
- Disabling encryption and rotating the key are not covered here yet; both need
  an offline, key-aware database command. Until then do not remove the key from a
  database that holds encrypted rows unless you accept the sign-in cost above.
