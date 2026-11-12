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

Until step 3 is done mcpproxy remembers that compaction is pending (a marker in
`config.db`, set by any write that replaced a plaintext row with an encrypted one)
and says so in `mcpproxy doctor` and `mcpproxy auth tokens status`. A plain
`mcpproxy db compact` keeps the pre-compaction file as `config.db.bak`, which still
holds the old pages, so it does **not** clear the advice and prints a note naming
the backup. Only `db compact --keep-backup=false` clears it. A `config.db.bak` left
by an earlier compaction is not removed by that command: delete it yourself.
`doctor` reports it until you do.

## Checking the state

`mcpproxy doctor` (and `GET /api/v1/diagnostics`) include an **OAuth Token Storage**
section once a key is configured or any row is encrypted:

```text
🔑 OAuth Token Storage
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  Encryption: on (key id 0329a64b3f620111, source: ref_file)
  Rows:       sealed 2, plaintext 0, undecryptable 1
              undecryptable by reason: key_mismatch 1
    - notion: key_mismatch (row key id 132087b39d6d2462)
  Warning:    freed pages may still hold the old plaintext; with mcpproxy stopped, run: mcpproxy db compact --keep-backup=false
  Next:      Undecryptable rows: restore the key they were sealed with, or discard a row with 'mcpproxy auth logout --server=<name>' and sign in again.
```

| Field | Meaning |
|-------|---------|
| Encryption, key id, source | Whether a key is configured, its public key id (a short fingerprint) and how it was obtained: `literal`, `env`, `env_file`, `ref_file`, `ref_env`, `ref_keyring` |
| Rows | A strict partition of the stored rows: `sealed` (opens with the configured key), `plaintext`, `undecryptable` (broken down by reason: `no_key`, `key_mismatch`, `auth_failed`, `unsupported_version`, `malformed`) |
| Per-server list | Every undecryptable row with its server, reason and the key id in the row, so a `key_mismatch` names both ids |
| Warnings | `literal_key` (the key is in the config file), `key_file_in_data_dir`, `plaintext_rows_with_key` (a restored backup or a write by an older binary; sealed at the next start), `compaction_advised`, `backup_file_present` (`config.db.bak` in the data directory while encryption is on) |

Each undecryptable server also appears once in the doctor's runtime warnings, so the
issue count and the tray badge reflect it. The section never contains the key, its
bytes, the contents of the key source or the key reference; a token scoped to some
servers sees counts for those servers only and no key id.

With mcpproxy stopped, `mcpproxy auth tokens status` prints the same section from
the database, plus one line per server (`sealed`, `plaintext` or `undecryptable`
with its reason). It prints no token value.

## The `auth tokens` commands

Three offline commands work on the database directly, so mcpproxy must be stopped:
with a running core they fail with exit code 3 and change nothing. They resolve the
key exactly as the daemon does, so `MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE=<source>`
supplies a key that only exists inside the service (a systemd credential, a
container secret). Add `-o json` or `-o yaml` for machine-readable output.

All three open `config.db` the way every offline command does, which applies the
usual open-time maintenance (file permissions, session-record migration and
retention). `status` and `encrypt --dry-run` change nothing else: no OAuth row and
not the compaction marker.

| Command | What it does |
|---------|--------------|
| `mcpproxy auth tokens status` | Prints the section above plus one line per server. Changes no OAuth row. |
| `mcpproxy auth tokens encrypt [--dry-run]` | Encrypts every plaintext row under the configured key in one transaction, as the startup sweep does. Idempotent. Lists rows it had to leave (not a valid record, or over 1 MiB) and undecryptable rows; exits 1 when any remain. `--dry-run` reports the count and changes no OAuth row, nor the compaction marker. Needs a key (exit 4 without one). |
| `mcpproxy auth tokens decrypt --yes [--leave-undecryptable]` | Rewrites every row the configured key opens as plaintext JSON, in one transaction. Needs `--yes`. If a row cannot be opened it refuses and changes nothing, unless `--leave-undecryptable` says to rewrite the others and leave those as they are (it still exits 1). Needs the key. |

`decrypt` stores the tokens readable in `config.db`. Copies of the file or of the
data directory made until you encrypt again hold them in plaintext.

## Turning it off, downgrading and rotating the key

An older release cannot read encrypted rows: its servers show "login required", and
a new login there overwrites the encrypted row with a plaintext one. Decrypt first.

**Disable, or downgrade to a release without this feature**

```bash
sudo systemctl stop mcpproxy
MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE=/etc/mcpproxy/oauth-key \
  mcpproxy auth tokens decrypt --yes
# then remove every key source: oauth_token_encryption_key in the configuration
# and MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY[_FILE] in the service environment,
# and only then install the older binary or start mcpproxy again
```

With a key still configured, the next start would encrypt the rows again.

**Rotate the key**

1. Stop mcpproxy.
2. `mcpproxy auth tokens decrypt --yes` with the **old** key.
3. Change the key (configuration, credential or environment).
4. `mcpproxy auth tokens encrypt` with the **new** key.
5. `mcpproxy db compact --keep-backup=false`, then delete any `config.db.bak`.
6. Start mcpproxy.

Step 2 leaves the tokens in plaintext and step 4 leaves the plaintext in freed pages
that bbolt does not zero, which is what step 5 removes. Any copy or backup of
`config.db` or of the data directory made between steps 2 and 5 holds the tokens in
the clear: delete those too. Single-step rekeying is not offered.

**Packaged Linux service**

Run the offline commands as the service user with the service's paths, with the
service stopped, and supply the key explicitly. The examples below read the key
file directly, so they need the delivery where the service user can read it
(`chown mcpproxy /etc/mcpproxy/oauth-key`, mode `0600`):

```bash
sudo systemctl stop mcpproxy
sudo -u mcpproxy env MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE=/etc/mcpproxy/oauth-key \
  mcpproxy --config=/etc/mcpproxy/mcp_config.json --data-dir=/var/lib/mcpproxy \
  auth tokens status
sudo systemctl start mcpproxy
```

The write commands take the same prefix. To turn encryption off on the packaged
service:

```bash
sudo systemctl stop mcpproxy
sudo -u mcpproxy env MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE=/etc/mcpproxy/oauth-key \
  mcpproxy --config=/etc/mcpproxy/mcp_config.json --data-dir=/var/lib/mcpproxy \
  auth tokens decrypt --yes
# remove every key source before starting again: oauth_token_encryption_key in
# /etc/mcpproxy/mcp_config.json AND any MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY[_FILE]
# / LoadCredential= line in the unit or its drop-ins (then: systemctl daemon-reload)
sudo systemctl start mcpproxy
```

If a key source is left in place, the next start encrypts the rows again and the
plaintext that `decrypt` wrote stays in freed pages until the next
`db compact --keep-backup=false`. To rotate the key instead, follow
[Rotate the key](#turning-it-off-downgrading-and-rotating-the-key) with the same
prefix: `encrypt` with the new key comes before the compaction.

With `LoadCredential=` the key file is root-only and the service user cannot read
it, so the commands above exit 4. Let systemd hand the credential to a one-off
unit that runs as the service user instead (replace `status` with the command you
need; for `LoadCredentialEncrypted=` use that property and the encrypted file):

```bash
sudo systemd-run --pipe --wait --collect \
  -p User=mcpproxy -p Group=mcpproxy \
  -p LoadCredential=oauth-key:/etc/mcpproxy/oauth-key \
  /bin/sh -c 'MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE="$CREDENTIALS_DIRECTORY/oauth-key" \
    exec /usr/bin/mcpproxy --config=/etc/mcpproxy/mcp_config.json \
    --data-dir=/var/lib/mcpproxy auth tokens status'
```

In a container, `docker compose run --rm --entrypoint mcpproxy mcpproxy auth tokens ...`
(with the same secret, configuration and data volume mounted, and the service
stopped) gives the command the same view the service has. The image's entrypoint is
`mcpproxy serve`, so without `--entrypoint` the arguments would go to `serve`.

## When a login is refused

`mcpproxy auth login --server=<name>` reads the stored row first. When it cannot be
decrypted it opens no browser and contacts neither the authorization server nor
the discovery endpoint. It prints the
[`MCPX_OAUTH_TOKEN_UNDECRYPTABLE`](../errors/MCPX_OAUTH_TOKEN_UNDECRYPTABLE.md)
explanation with the reason, the key ids (never key material) and the two ways out,
and exits non-zero. It works the same through a running daemon and standalone.
`mcpproxy auth status` shows such a server as "cannot be decrypted", never as "log
in", and the Web UI offers the configure guidance instead of a sign-in button.

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
- Do not remove the key from a database that holds encrypted rows without running
  `mcpproxy auth tokens decrypt --yes` first, unless you accept the sign-in cost
  above (see "Turning it off, downgrading and rotating the key").
