---
id: tpa-bundle-signing
title: Signed TPA Bundles (Spec 101)
sidebar_label: Signed TPA Bundles
description: Sign, drop, verify, roll back and monitor an offline TPA signature corpus, with downgrade protection that works without a network, a daily refresh, and an opt-in signed fetch.
keywords: [security, tpa, signature bundle, ed25519, anti-downgrade, rollback, air-gapped, tpa-db, scanner, refresh, fetch, re-protection]
---

# Signed TPA Bundles (Spec 101)

The `scan` trust mode matches tool descriptions against an offline corpus of Tool Poisoning Attack (TPA) signatures, the tpa-db `scanner-bundle.json`. MCPProxy ships one inside the binary. This page is for operators who want to run a different corpus from disk and want the proxy to prove that the corpus is the one they signed, and that nobody has swapped in an older one.

Everything on this page works with no network. Dropping a file is the primary path, not a fallback. A daily refresh re-reads that file for you, and an **opt-in, default-off** fetch can also download a signed bundle from a channel you name (see [Daily refresh and optional fetch](#daily-refresh-and-optional-fetch)). Nothing is fetched unless you turn it on and give it a URL, and there is no default URL.

For the short version of how the corpus fits into quarantine, see [Signature bundle (offline TPA corpus)](/features/security-quarantine#signature-bundle-offline-tpa-corpus). For the detection engine the corpus feeds, see [Tool Scanner](/features/tool-scanner#signature-corpus-and-coverage).

## The operator lifecycle at a glance

1. Generate a signing key pair with `tpa-db keygen`. Keep the private key offline.
2. Put the public key in `security.tpa_operator_signers`, declare `tpa_active_root: "operator"`, and set a baseline and an epoch.
3. Author a bundle whose manifest carries `sequence`, `trust_epoch`, `key_id` and `generated_at`, then sign it with `tpa-db sign`.
4. Drop `scanner-bundle.json` and `scanner-bundle.json.sig` side by side at `tpa_bundle_path`.
5. Activate it (config reload, restart, `mcpproxy security bundle reload`, or just wait for the next daily refresh cycle) and read the status.
6. Optionally let MCPProxy fetch later releases itself (`tpa_fetch_enabled`, `tpa_fetch_url`).
7. If a release turns out to be bad, roll back by full digest. If a key leaks, bump `tpa_operator_epoch`.

## What is signed, and why

A bundle is a JSON file of detection rules. If an attacker can write to the place MCPProxy reads it from, they can weaken the scanner by removing rules, or by putting back an older copy that predates a rule you rely on. Both are quiet failures: the scanner keeps running and reports a clean result.

A signed bundle closes the first hole. The signature is a detached Ed25519 signature over the exact raw bytes of the bundle file. It lives in a small sidecar file, `<bundle>.sig`, beside the bundle. Change one byte of the bundle, even whitespace, and the signature no longer verifies. The sidecar's own formatting (whitespace, key order) is not signed, and does not need to be.

The anti-downgrade state closes the second hole. Each signed bundle carries a `sequence` number, and the proxy remembers the highest one it has activated. It refuses anything lower. See [Anti-downgrade](#anti-downgrade).

The sidecar is one JSON line with exactly four keys:

```json
{"sidecar_version":"1","algorithm":"ed25519","key_id":"3f0a…64 hex","signature":"<88 base64 chars>"}
```

You never write it by hand; `tpa-db sign` produces it. The full grammar is in `specs/101-tpa-db/contracts/sidecar-format.md`.

## Configuration keys

These live under `security` in `mcp_config.json`. The trust keys have no environment-variable aliases on purpose: the trust root is config-resident, so someone who can change the environment or the bundle file cannot also choose who is trusted. The only environment switch among the keys is `MCPPROXY_TPA_FETCH_ENABLED` (plus the existing `MCPPROXY_TPA_BUNDLE_PATH`); it can turn the fetch on or off, but it cannot choose the URL or the keys, and it is never written back to the config file. `GET /api/v1/config` shows them unmasked because they hold public keys and counters, not secrets.

| Key | Default | Meaning |
|---|---|---|
| `tpa_bundle_path` | `""` (embedded) | Where the bundle file is. The sidecar is always `<path>.sig`. `MCPPROXY_TPA_BUNDLE_PATH` overrides the path only. |
| `require_signed_bundle` | `false` | When `true`, a bundle without a valid sidecar is refused (`signature_required`). |
| `tpa_active_root` | `"publisher"` | Which authority may activate a bundle: `"publisher"` or `"operator"`. |
| `tpa_operator_signers` | `[]` | Operator public keys, each the standard base64 of the 32 raw key bytes. |
| `tpa_operator_baseline` | absent | The anti-downgrade floor for operator bundles. A bundle must have a sequence strictly above it. Absent means no floor and degraded coverage. An explicit `0` is different from absent. |
| `tpa_operator_epoch` | absent (`0`) | The operator trust epoch. Bundles must carry exactly this `trust_epoch`. |
| `tpa_refresh_interval` | absent (`24h`) | How often the refresh cycle runs. At least `1h`. See [Daily refresh and optional fetch](#daily-refresh-and-optional-fetch). |
| `tpa_fetch_enabled` | `false` | Opt in to downloading a signed bundle from `tpa_fetch_url`. Env override `MCPPROXY_TPA_FETCH_ENABLED`. |
| `tpa_fetch_url` | `""` | The channel to fetch from. Empty means no request is ever made. There is no compiled-in default. |
| `tpa_stale_after_days` | absent (`30`) | Age in days after which the active bundle is flagged stale. `0` disables the flag. |

The field-by-field reference is in the Security section of `docs/configuration.md` in the repository. Invalid values are rejected when you write the config (`PATCH /api/v1/config`, `POST /api/v1/config/apply`). A malformed or duplicate signer found in a file that was edited by hand is dropped with a log line instead of stopping the daemon, so a bad key can never brick startup and never widens what is trusted.

## Keys and key ids

`tpa-db` is the operator tool in this repository. Build it with `go build -o tpa-db ./cmd/tpa-db`.

```bash
tpa-db keygen --out ./keys
# public_key: <base64 of the 32-byte public key>
# key_id: <64 lowercase hex characters>
```

`keygen` writes `operator.key` (the private key, mode 0600) and `operator.pub`, and prints both the public key and the key id. It never overwrites existing files.

- The **public key** is what goes into `tpa_operator_signers`. Config holds public keys only, never the private key and never the key id.
- The **key id** is the SHA-256 of the raw public key. It is a fingerprint of the key, not a label you choose. You copy it into the bundle manifest as `key_id`.

Because the id is derived from the key, a manifest that names one key while the bytes are signed by another is detected as a mismatch, not shrugged off.

Keep `operator.key` off the machine that runs MCPProxy. Only the public key needs to be there.

## Signing

The operator authors four fields in the bundle's own manifest, because the signer never edits the bundle:

| Manifest field | Form | Rule |
|---|---|---|
| `sequence` | a JSON **string** of a decimal number, for example `"2"` | Matches `^[1-9][0-9]{0,19}$` and fits in 64 bits. A bare number, `"0"` and `"01"` are refused. It must be strictly above your `tpa_operator_baseline` and above the sequence already activated. |
| `trust_epoch` | a JSON integer, `0` or more | Must equal `tpa_operator_epoch` exactly. |
| `key_id` | 64 lowercase hex characters | The id printed by `keygen` for the key you sign with. |
| `generated_at` | RFC 3339 timestamp | The freshness stamp that drives `staleness_days`. |

One way to stamp them with `jq`, then sign:

```bash
KEY_ID=<key_id from keygen>
jq --arg id "$KEY_ID" --arg now "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
   '. + {sequence: "2", trust_epoch: 0, key_id: $id, generated_at: $now}' \
   corpus.json > scanner-bundle.json

tpa-db sign --bundle scanner-bundle.json --key ./keys/operator.key
# writes scanner-bundle.json.sig next to the bundle
```

Pass `--out PATH` to write the bundle copy to `PATH` and the sidecar to `PATH.sig` instead of signing in place.

`sign` refuses a bundle whose manifest `key_id` is not the id of the signing key (`key_id_mismatch`), because every loader would reject that pair. It signs whatever bytes it is given; it does not run the detection-quality gate in this release. Any edit to the bundle after signing, even reformatting, invalidates the signature, so sign last.

Check a pair offline before you move it:

```bash
tpa-db verify --bundle scanner-bundle.json --signer <public key>
# prints OK, or the rejection code and exits 1
```

## Dropping and activating

Put `scanner-bundle.json` and `scanner-bundle.json.sig` at `tpa_bundle_path`. Write the bundle and then the sidecar, or move both in together; a bundle seen without its sidecar is judged without one.

**There is no file watcher on the bundle.** Only `mcp_config.json` is watched. A dropped bundle becomes active when one of these happens:

- the configuration is reloaded (any hot-reload of `mcp_config.json`),
- the daemon restarts,
- the next [refresh cycle](#daily-refresh-and-optional-fetch) runs (within `tpa_refresh_interval`, 24 hours by default), with no restart,
- you run `mcpproxy security bundle reload`, or call `POST /api/v1/security/bundle/reload`,
- you run `mcpproxy security bundle refresh` (the same file re-read, plus the fetch when it is on).

All of them run the same single-flight pipeline and return the updated status. Reloading a file that is already active is a no-op: no log line, no history row.

Every candidate goes through the same checks in the same order, and the first failure wins. Whatever fails, the previously active corpus keeps serving. A rejection never leaves the scanner with an empty or partial rule set.

## Daily refresh and optional fetch

Spec 087 adds a refresh cycle on top of the file drop. It has two legs, run in this order every cycle:

1. **File leg.** Re-reads `tpa_bundle_path` and runs it through the pipeline. Network-free, always on, idempotent: an unchanged file costs nothing and writes no history. There is no key that turns the cycle off. This is what makes an air-gapped drop activate by itself within a day.
2. **Fetch leg.** Only when [all of its prerequisites hold](#when-the-fetch-may-make-a-request), downloads a signed pair from the channel and offers it to the same pipeline.

The first cycle runs 30 seconds to 5 minutes after startup. Later cycles run every `tpa_refresh_interval` (default `24h`, minimum `1h`) with about 10% jitter, so a fleet restarted together does not hit the channel in lockstep. The timer is re-armed after each cycle, so a suspended laptop produces one cycle when it wakes, not a burst. A manual `refresh` and a timer tick that arrive together join the cycle already running, so there is one download. A `reload` or a config reload runs through the same serialized pipeline and is idempotent, so a given bundle is activated once however many triggers see it.

### The fetch is opt-in, has no default URL, and needs an operator root today

- **Default off.** `tpa_fetch_enabled` is `false`. Until you set it (or `MCPPROXY_TPA_FETCH_ENABLED=true`), the fetch leg makes zero requests.
- **No default URL.** `tpa_fetch_url` is empty and no URL is compiled into the binary. An empty URL means no request, reported as `disabled` with reason `no_url`. The URL shapes below are examples of what you would configure, not something MCPProxy contacts on its own.
- **Works today only with operator-signed bundles.** The publisher key set embedded in the binary is still empty, so a publisher-signed bundle is refused as `key_not_trusted`. A fetched bundle is verified against the same trust set as a dropped one, so the fetch is only useful with `tpa_active_root: "operator"` and your own key in `tpa_operator_signers`. With the publisher root and the empty publisher set, the fetch leg stays silent (reason `no_trusted_signer`) and `mcpproxy doctor` says so.
- **`MCPPROXY_DISABLE_AUTO_UPDATE` does not apply.** It governs the MCPProxy release check. The bundle fetch has its own switch.

```json
{
  "security": {
    "tpa_active_root": "operator",
    "tpa_operator_signers": ["<public_key>"],
    "tpa_operator_baseline": 1,
    "tpa_fetch_enabled": true,
    "tpa_fetch_url": "https://api.github.com/repos/<owner>/<repo>/releases/latest",
    "tpa_refresh_interval": "24h"
  }
}
```

Writing the config (`PATCH /api/v1/config`, `POST /api/v1/config/apply`) validates the shape: the URL must be `https` (plain `http` only for `localhost` or a loopback address), must have no credentials, query string or fragment, and must not be empty while the fetch is on; fetch with `tpa_active_root: "operator"` and no signers is refused; the interval must be at least `1h`. The error messages never repeat the URL. A malformed value already on disk is logged and treated as "fetch off" instead of stopping the daemon.

### When the fetch may make a request

The fetch leg sends a request only when all of these hold. Otherwise it reports `disabled` and a reason, and nothing leaves the machine:

| Reason | Meaning |
|---|---|
| `fetch_off` | `tpa_fetch_enabled` is false. |
| `no_url` | `tpa_fetch_url` is empty. |
| `invalid_url` | The URL does not pass the shape rules above. |
| `no_trusted_signer` | The active root has no keys to verify against (the publisher set is empty, or the operator root has no `tpa_operator_signers`). |
| `state_unavailable` | The anti-downgrade state store could not be loaded, so nothing could be activated safely. |

### Channel shapes

The shape is chosen from the **path** of `tpa_fetch_url` alone.

- **Releases API, "latest".** A path of the form `/repos/<owner>/<repo>/releases/latest` (for example the GitHub API URL above). MCPProxy reads the release JSON (capped at 1 MiB) and needs exactly one asset named `scanner-bundle-<sequence>.json` with a sibling `scanner-bundle-<sequence>.json.sig`. Zero or several such assets, or a missing `.sig`, is `channel_invalid`. Both assets are downloaded through their `browser_download_url`. The sequence in the file name is an unsigned hint, used only to skip a download whose sequence is already at or below the watermark; it is never trusted for activation.
- **Direct.** Any other path. The URL is the bundle and `<url>.sig` is its sidecar. A `404` on the sidecar is treated as "unsigned", which a fetch always refuses as `signature_required`, even when `require_signed_bundle` is off. Any other sidecar failure is `fetch_unavailable` with reason `http_status`.

Both shapes send `If-None-Match` with the `ETag` of the previous answer (kept in memory, so it resets at restart), so an unchanged channel answers `304` (`not_modified`) with no download. The bundle and sidecar are held to the same size ceilings as a dropped pair (8 MiB, 4096 bytes). The file names match the file-drop convention (`<name>` plus `<name>.sig`), so a pair downloaded by hand from the same channel can be dropped at `tpa_bundle_path` as it is.

### Transport rules

- https is required, except for `localhost` and loopback literals (a local mirror or a test).
- A request has a 20 second total budget and a 10 second response-header timeout.
- Redirects: at most 5 hops, never from https to http. A redirect may cross hosts, because GitHub release assets are served from another host and integrity comes from the signature, not from the host.
- At connect time, link-local, cloud-metadata (169.254.0.0/16 and fe80::/10), unspecified and multicast addresses are always refused. Loopback and private ranges are refused unless the host you configured is itself in that range (a mirror on your LAN), so a public URL cannot redirect the fetch inward. With `HTTP_PROXY` or `HTTPS_PROXY` set, the same check is applied to the target host before the request.
- **Logs and status carry the host only.** The URL, its path and any redirect targets never appear in logs, in `GET /api/v1/security/bundle`, in `last_rejection` or in error text. `refresh.fetch_host` is the only place a name shows up.

### What a fetched bundle has to pass

Exactly what a dropped bundle passes, in the same order: Ed25519 signature over the raw bytes, key id, epoch and sequence agreement, the per-authority watermark and the equal-sequence-different-digest check, the deny-list, then the [activation self-check](/features/tool-scanner#activation-self-check). A substituted or replayed asset therefore fails as `tamper` or `downgrade`. A withheld asset (an attacker who just blocks new releases) cannot be detected from a single response, so it surfaces as staleness: see the `stale` field and the doctor warning below.

A fetched bundle is stored like any activated external bundle: its last-known-good copy is kept in the data directory and restored at the next start, and `source` reads `fetch`. It keeps serving across config reloads and restarts. **Turning the fetch off does not drop it.** Falling back silently to the embedded corpus would be a downgrade you did not ask for; use `mcpproxy security bundle rollback --embedded --digest <embedded digest>` to go back deliberately.

### Outcomes versus rejections

Two different things can go wrong, and the status keeps them apart.

- A **rejection** means a candidate was refused by the pipeline (`tamper`, `downgrade`, `gate_failure`, `signature_required`, and the other reasons in the [table above](#rejection-reasons)). It is recorded in `last_rejection`, and the previous bundle keeps serving.
- A **non-verification outcome** means nothing was verified at all: the network failed, the server rate-limited you, or the listing was malformed. It is reported in `refresh`, never in `last_rejection`, and it never touches the watermark.

`refresh.last_outcome` uses a closed vocabulary:

| `last_outcome` | Meaning |
|---|---|
| `activated` | A new bundle became active this cycle. |
| `unchanged` | Nothing newer than what is serving (or a refused pair that was already refused once). |
| `not_modified` | The channel answered `304`. |
| `rejected` | A candidate was refused; see `last_rejection`. |
| `fetch_unavailable` | Network error, timeout, a non-2xx status or a rate limit. `last_reason` is `network_error`, `timeout`, `http_status` or `rate_limited`. A rate limit pushes the next attempt to the reset time (at most 24 hours). |
| `channel_invalid` | The Releases listing did not have exactly one usable asset pair. |
| `superseded` | The fetch settings changed while a download was in flight, so the result was discarded. |
| `disabled` | The fetch leg did not run; `last_reason` is one of the table above. |

`refresh.last_trigger` is `startup`, `config` (a config reload), `timer` or `manual`. `consecutive_failures` counts `fetch_unavailable` and `channel_invalid` cycles in a row and resets on any other outcome. Persisting failures are logged once per change and then at most once a day.

### `refresh` versus `reload`

| Command | Reads the local file | Makes a network request |
|---|---|---|
| `mcpproxy security bundle reload` | yes | **never** |
| `mcpproxy security bundle refresh` | yes | at most one cycle, only if the fetch is on and allowed |

`reload` is the offline tool and stays offline even when the fetch is on. `refresh` runs one cycle now, joins a cycle already in flight, and returns the status with the outcome in `refresh`.

### Freshness, stale flag and doctor

The status adds these fields (all omitted when unknown):

| Field | Meaning |
|---|---|
| `last_checked_at` | The end of the most recent pipeline run, including runs that found nothing to do. |
| `last_activated_at` | When the serving external bundle was activated. Absent for the embedded corpus. |
| `stale`, `stale_after_days` | `true` when the bundle's `generated_at` is older than `tpa_stale_after_days` (default 30). Computed when you read the status, so editing the threshold takes effect immediately. A `generated_at` in the future is never stale. |
| `refresh` | `fetch_enabled`, `fetch_host`, `interval_seconds`, `last_trigger`, `last_outcome`, `last_reason`, `last_attempt_at`, `consecutive_failures`, `next_check_at`. |
| `reprotect` | The state of the [re-protection sweep](/features/security-quarantine#re-protection-after-a-bundle-activates). |

`mcpproxy doctor` adds at most three short warnings, none of which contains a digest, path, URL or host: the bundle is stale (only when you configured a way to refresh it), a newer candidate was refused or the refresh has failed three or more times in a row (so you are stuck on last-known-good), and the fetch is enabled but cannot run (`no_url`, `invalid_url` or `no_trusted_signer`). A stale embedded corpus on an install that never configured a refresh shows in the status only; updating MCPProxy is what refreshes it.

### Rolling back when you only fetch

A rollback by path needs the target file in the directory of `tpa_bundle_path`. On an install that fetches and has no `tpa_bundle_path`, only `rollback --embedded` is available. To keep the option of returning to an earlier fetched release, set `tpa_bundle_path` to a directory you control and keep the downloaded pairs of the releases you may want next to it. Rolling back deny-lists the release you rolled away from, so the channel offering it again is refused quietly (`deny_listed`) and does not reactivate it; a strictly newer signed release activates normally and clears the pin.

### Air-gapped use of a downloaded pair

You do not need the fetch to use the channel. Download `scanner-bundle-<n>.json` and `scanner-bundle-<n>.json.sig` on a connected machine, carry them across, and drop them as `tpa_bundle_path` and `<tpa_bundle_path>.sig` (any one name pair works, since the names are yours to choose). The file leg picks them up on the next cycle or on `reload`, and they go through the same verification.

## Reading the status

```bash
mcpproxy security bundle            # human table
mcpproxy security bundle -o json    # or -o yaml
```

The same object is returned by `GET /api/v1/security/bundle`.

| Field | Meaning |
|---|---|
| `source` | `embedded`, `file` or `fetch`. |
| `path` | The configured path, when `source` is `file`. For `fetch` it is the origin (`https://host`) only, never the full URL. |
| `bundle_version`, `schema_version` | The corpus's own declared versions. |
| `runnable_rules`, `skipped_rules`, `declared_skipped` | How many rules are live, and how many are not. Skipped rules are un-evaluated coverage, never clean coverage. |
| `generated_at`, `staleness_days` | The corpus freshness stamp and its age in whole days. |
| `fingerprint` | The first 12 hex characters of the digest. A human label only. |
| `full_digest` | The complete 64-hex SHA-256 of the bundle bytes. This is the only value rollback accepts. |
| `sequence`, `authority`, `trust_epoch` | The manifest sequence (decimal string), the root that verified it (`publisher` or `operator`; empty for an unsigned file), and its epoch. The embedded default reports `publisher`. |
| `signature_verified` | `true` when a detached signature verified. Always `false` for the embedded default, which is trusted because it is part of the binary. |
| `coverage_degraded`, `degraded_reason` | Whether scan-mode auto-approval is suspended, and why. |
| `pinned`, `rollback` | A rollback pin is in force; the active bundle came from a rollback. |
| `load_error` | `<reason>: <detail>` for the latest attempt to load the configured path, if it was refused. Empty after a successful load. |
| `last_rejection` | `{reason, detail, at}` for the most recent refused candidate. It survives a later successful load, so "what did it refuse, and when" is still there. `reason` is the only field to branch on; `detail` is for people. |

The human table always prints the full digest in full, because you will paste it into a rollback. When coverage is degraded it prints a line `DEGRADED: <reason>`.

### Rejection reasons

| `last_rejection.reason` | What it means |
|---|---|
| `tamper` | The signature did not verify over the bundle bytes, or the manifest, sidecar and verifying key disagree on the key id. `detail` says which (`signature_invalid` or `key_id_mismatch`). Re-fetch the pair; if the bytes are yours, re-sign. |
| `downgrade` | Sequence is at or below this authority's watermark for the current epoch, or equals it with different bytes. `detail` gives the sequence and the watermark. Sign a higher sequence. |
| `stale_epoch_replay` | The bundle's `trust_epoch` is below the current epoch. A validly signed but superseded artifact. Re-sign at the current epoch. |
| `epoch_ahead` | The bundle's `trust_epoch` is above the current epoch. Either `tpa_operator_epoch` was not raised yet, or (for publisher bundles) this binary is older than the publisher's recovery release; update MCPProxy. Does not degrade coverage. |
| `unsigned_refused_by_ratchet` | A signed bundle has activated on this install before, so unsigned drops are refused from then on. Add the sidecar. |
| `signature_required` | `require_signed_bundle` is on and there is no sidecar. |
| `authority_not_active` | Verified by a real key of the root that is not `tpa_active_root`. `detail` names both. Either the wrong key signed it, or the declared root is wrong. |
| `key_not_trusted` | The sidecar's key id is in neither trust set. Check `tpa_operator_signers`. Publisher-signed bundles land here today (see below). |
| `sidecar_invalid` | The sidecar breaks its grammar. `detail` carries the exact code (`sidecar_malformed`, `sidecar_missing_field`, `sidecar_unknown_field`, `sidecar_duplicate_field`, `sidecar_type`, `sidecar_version_unsupported`, `sidecar_algorithm_unsupported`, `sidecar_key_id_malformed`, `sidecar_signature_malformed`). A present but invalid sidecar is never treated as "no sidecar". |
| `invalid_bundle` | The bundle did not parse, has a bad `sequence` or `trust_epoch`, an unsupported version, a pattern that does not compile, or no runnable rule. |
| `deny_listed` | You rolled away from this exact release. It stays refused until you remove or replace the file. |
| `gate_failure` | The [activation self-check](/features/tool-scanner#activation-self-check) refused the candidate: it would have missed too many known attacks or flagged too many benign tools. `detail` carries counts and corpus ids, never the candidate's patterns. |
| `ceiling_exceeded` | Over a limit: bundle larger than 8 MiB, more than 2000 entries in `rules`, or sidecar larger than 4096 bytes. `detail` gives the limit and the observed value. A file exactly at a limit loads. |
| `not_regular_file` | The path (or its sidecar) is a FIFO, device, socket, directory or a symlink. Point `tpa_bundle_path` at the real file. |
| `read_timeout` | The read did not finish in 5 seconds. See [Platform notes](#platform-notes). |
| `read_error` | The file could not be opened or read: missing, permission denied, I/O error. |
| `rollback_target_mismatch` | A rollback named a digest that does not match the bytes found. |
| `state_commit_failed` | The anti-downgrade state could not be saved, so the candidate was not activated. Activating without a durable watermark would reopen the replay window. Check the data directory and disk space. |

### Degraded coverage

Degraded coverage means MCPProxy cannot vouch for the corpus that is scanning. **While coverage is degraded, `scan`-mode auto-approval is suspended: new and changed tools are held for human review (held reason `scan_coverage`) instead of being approved on a clean verdict.** Nothing else changes, and the scanner keeps scanning. The reason is always shown; it is never a silent flag.

| `degraded_reason` | When | What to do |
|---|---|---|
| `unsigned_external_active` | An unsigned file is the active corpus. | Sign it, or remove `tpa_bundle_path` to return to the embedded corpus. |
| `operator_authority_unbaselined` | An operator-signed bundle is active but `tpa_operator_baseline` is not set. Without a floor, a data-directory wipe leaves no downgrade protection. | Set `tpa_operator_baseline` (an explicit `0` counts). |
| `operator_bundle_missing` | The operator root is active and a bundle you activated before is gone or no longer valid, so the embedded corpus is serving. | Restore the file, or fix the rejection shown in `last_rejection`. |
| `embedded_below_watermark` | The embedded corpus is serving and its sequence is below the publisher watermark, meaning it is older than something that was activated before. | Update MCPProxy, or restore the newer bundle. Not reachable until a publisher key ships. |

These cases are deliberately not degraded: a fresh install on the embedded corpus, and an embedded corpus at or above the publisher watermark after an external file was removed. Degrading there would turn auto-approval off on every new install.

## Anti-downgrade

The proxy keeps a small amount of state per signing authority and decides every candidate against it.

- **Authorities.** There are two: `publisher` (keys embedded in the binary) and `operator` (keys in `tpa_operator_signers`). Each has its own watermark. Sequences from different authorities are never compared. A key listed under both is treated as publisher only.
- **Active root.** Only the authority named by `tpa_active_root` may activate a bundle. A bundle verified by the other authority is `authority_not_active`. Switching the root deactivates a bundle from the old root at the next load.
- **Watermark.** The highest sequence ever activated for this authority in this epoch. A candidate must have a sequence strictly above it. An equal sequence is accepted only if it is byte-identical to the release that set the watermark (for example after a restart).
- **Baseline.** The watermark never starts below the baseline. For the publisher it comes from the embedded bundle. For the operator it comes from `tpa_operator_baseline`, read from config on every start. This is what protects you after the data directory is wiped: the floor does not live there.
- **Epoch.** Every comparison is by (authority, epoch, sequence), epoch first. Raising the epoch retires everything signed under the old one, regardless of its sequence.
- **Ratchet.** Once any signature-verified external bundle has activated, unsigned drops are refused for good, even with `require_signed_bundle` off.
- **Unsigned drops.** An unsigned file cannot move the watermark and its manifest is never trusted. It activates only if no ratchet and no `require_signed_bundle` stop it, and then it runs with degraded coverage.

`require_signed_bundle` defaults to **off**, so unsigned drops keep working for people who have never heard of signing. That has two consequences. First, an unsigned bundle is accepted until the ratchet is set, and the ratchet is stored in the data directory, which a wipe resets. Second, nothing stops someone who can write the bundle path from dropping an unsigned corpus; the only visible sign is degraded coverage. If anyone other than you can write `tpa_bundle_path`, set `require_signed_bundle: true`.

## Rolling back

Rollback is the one operation that deliberately goes below the watermark, so it is authorized by the **full SHA-256 digest** of the target, never by a path and never by the 12-character fingerprint. The path only says where to look for the bytes. The digest is what proves they are the ones you meant. A 12-hex fingerprint is 48 bits, fine as a label and not strong enough to authorize a bypass when an attacker could choose the file. The CLI and the REST endpoint refuse it with a message that the full digest is required.

Find the digest in the history, which lists every activation, oldest first:

```bash
mcpproxy security bundle history -o json | jq '.[] | {sequence, full_digest, source, activated_at}'
```

The history is how you learn a known-good digest without reading it from the file the attacker may control. It makes a rollback possible; it does not authorize one.

Then roll back, naming exactly one target:

```bash
# to a bundle file (its sidecar is PATH.sig); it must sit directly in the
# directory of tpa_bundle_path, e.g. next to /var/lib/tpa/scanner-bundle.json
mcpproxy security bundle rollback --path /var/lib/tpa/scanner-bundle.v1.json --digest <64-hex>

# to the corpus compiled into the binary
mcpproxy security bundle rollback --embedded --digest <64-hex of the embedded corpus>
```

A `--path` outside the directory of `tpa_bundle_path` (another directory, a `..` escape, a relative path) is refused as a usage error before the file is opened. Rollback is an admin action, but it should not double as a way to learn whether, or with which digest, an arbitrary file exists on the host. Keep the releases you may roll back to next to the configured bundle.

The embedded corpus's digest is the `full_digest` shown by `mcpproxy security bundle` while the embedded corpus is serving.

The same operation over REST is `POST /api/v1/security/bundle/rollback` with `{"embedded": bool, "path": string, "digest": string}`. It returns 400 for a usage error (both or neither of `embedded` and `path`, or a digest that is not 64 lowercase hex) and 409 with the rejection when the rollback is refused.

What a successful rollback does:

- The target serves. It still has to pass signature, epoch and size checks; only the watermark is bypassed. A target below your operator baseline is refused (`downgrade`, "below operator baseline").
- The target is **pinned**. While the pin holds, only the pinned digest may activate at or below the watermark. A bundle strictly above the watermark still activates normally and clears the pin.
- The signed release you rolled away from is **deny-listed**, so the next reload cannot bring it back. If its file is still at `tpa_bundle_path` it is refused as `deny_listed`.
- The watermark is **not lowered**.
- The override is logged at WARN and a history row is written with `rollback: true`.

A wrong digest is refused as `rollback_target_mismatch`, and the corpus that was serving keeps serving.

To return to normal watermark governance:

```bash
mcpproxy security bundle unpin
```

Unpin clears the pin and re-checks the serving bundle without it. If the rolled-back bundle is at or below the watermark it no longer passes, and the install falls back to the embedded corpus under the degraded-coverage rules above. The deny-list is kept.

## Key compromise

If an operator private key may have leaked:

1. Generate a new key pair (`tpa-db keygen`).
2. In config, replace the old public key in `tpa_operator_signers` with the new one, raise `tpa_operator_epoch` by one, and set `tpa_operator_baseline` to a value below the sequences you are about to sign.
3. Author and sign a new bundle with the new `key_id`, `trust_epoch` equal to the new epoch, and a sequence above the new baseline.
4. Drop it and run `mcpproxy security bundle reload`.

After the epoch change, anything signed under the old epoch is `stale_epoch_replay`, and anything signed by the old key is `key_not_trusted`, so an attacker holding the old key can replay nothing. The operator watermark restarts from the baseline, and pins and deny-list entries from the earlier epoch are retired (logged at WARN). Until you activate a bundle signed in the new epoch, the old operator bundle counts as lost: the embedded default serves and status reports `operator_bundle_missing` (degraded coverage).

Publisher epoch bumps are different: they arrive only with a new MCPProxy binary. A bundle with a higher epoch than your binary knows is refused `epoch_ahead` until you update.

## Air-gapped walkthrough

On the signing machine (offline):

```bash
tpa-db keygen --out ./keys                       # note public_key and key_id
jq --arg id "$KEY_ID" --arg now "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
   '. + {sequence: "2", trust_epoch: 0, key_id: $id, generated_at: $now}' \
   corpus.json > scanner-bundle.json
tpa-db sign --bundle scanner-bundle.json --key ./keys/operator.key
tpa-db verify --bundle scanner-bundle.json --signer "$PUBLIC_KEY"   # OK
```

Carry `scanner-bundle.json` and `scanner-bundle.json.sig` to the target on removable media.

On the target, set the config once:

```json
{
  "security": {
    "tpa_bundle_path": "/var/lib/mcpproxy/tpa/scanner-bundle.json",
    "tpa_active_root": "operator",
    "tpa_operator_signers": ["<public_key>"],
    "tpa_operator_baseline": 1,
    "tpa_operator_epoch": 0,
    "require_signed_bundle": true
  }
}
```

Copy the pair to `tpa_bundle_path`, run `mcpproxy security bundle reload`, and check `mcpproxy security bundle`: `signature_verified: true`, `authority: operator`, `coverage_degraded: false`. Every later release is the same three steps with a higher sequence. The private key never leaves the signing machine, and nothing here needs a network.

## Threat model

**In scope:** an attacker who can write the bundle file or its sidecar, who can plant a symlink or FIFO there, who can replay an older but genuinely signed bundle, or who holds a key that has since been retired by an epoch bump.

**Out of scope:** an attacker who can write `mcp_config.json`. They choose the path, the trusted keys, the active root, `require_signed_bundle` and the baseline, so they choose the policy itself. Protect the config file the way you protect the rest of your trust decisions.

**The data directory is best effort.** The watermark above the baseline, the signature ratchet, the pin, the deny-list, the activation history and the last-known-good copies all live in `config.db` and the data directory. Someone who can wipe or substitute them can reset the ratchet, lower the watermark or forge a pin. Do not rely on them against that attacker.

What still holds after a data-directory wipe or a substituted `config.db`:

| Survives | Why |
|---|---|
| Signature verification | Keys come from the binary and from config, not from the data directory. |
| Publisher baseline | Re-read from the embedded bundle on every start. |
| Operator baseline, epoch, active root, `require_signed_bundle` | They are config values. |
| Size and file-type limits | Enforced on every read. |

What does not survive: the ratchet, the watermark between the baseline and your latest release, the pin, the deny-list, the history. After a wipe, an older operator-signed bundle with a sequence above your baseline can be activated again. To limit that, raise `tpa_operator_baseline` each time you retire older releases for good. A last-known-good file whose digest does not match its recorded name is treated as missing, and the embedded corpus serves instead.

## Platform notes

The bundle and sidecar are opened without following symlinks, and the checks are made on the open file descriptor, not on the path, so swapping the path between the check and the open gains an attacker nothing.

- **Linux and macOS:** the file is opened with `O_NOFOLLOW` and `O_NONBLOCK`, checked with `fstat` to be a regular file, then switched to blocking reads. A FIFO never blocks the loader; it is refused as `not_regular_file`.
- **Windows:** the file is opened by handle with `FILE_FLAG_OPEN_REPARSE_POINT`. Reparse points (symlinks, junctions) and directories are refused.
- **Network filesystems:** a read can stall in the kernel and cannot be cancelled. The loader gives each candidate a 5 second budget and then refuses it as `read_timeout`, releasing the pipeline so the serving corpus is untouched. The stuck read parks one goroutine on that path. While it is parked, further reads of the same path are refused immediately instead of piling up more goroutines. If you see repeated `read_timeout`, fix the mount; do not point `tpa_bundle_path` at a flaky share.
- A symlinked `tpa_bundle_path` is refused. Configure the real file path.

## REST and CLI reference

| CLI | REST | Auth |
|---|---|---|
| `mcpproxy security bundle` | `GET /api/v1/security/bundle` | admin read |
| `mcpproxy security bundle history` | `GET /api/v1/security/bundle/history` | admin read |
| `mcpproxy security bundle rollback (--embedded \| --path P) --digest D` | `POST /api/v1/security/bundle/rollback` | admin API key |
| `mcpproxy security bundle unpin` | `POST /api/v1/security/bundle/unpin` | admin API key |
| `mcpproxy security bundle reload` | `POST /api/v1/security/bundle/reload` | admin API key |
| `mcpproxy security bundle refresh` | `POST /api/v1/security/bundle/refresh` | admin API key |

All commands accept `-o json` and `-o yaml`. The mutating endpoints refuse agent tokens. Scoped tokens cannot read the status either.

## What is not in this release

- **No publisher key has been issued.** The publisher signer set embedded in the binary is empty, so a bundle signed by the publisher is refused as `key_not_trusted`. The operator authority is the only signed path today, which is also why the fetch works only with operator-signed bundles. A later release will add the key, and with it the publisher baseline.
- **No default fetch URL.** The first official channel is not published yet, so nothing is compiled in. You choose where to fetch from.
- **No signed release index.** The fetch trusts only what the signature, watermark and deny-list establish. A channel that just withholds new releases is visible as staleness, not as an error.
- **No seed corpus yet.** The corpus that ships in the binary does not carry `sequence`, `trust_epoch` or `key_id`, which is why the walkthrough stamps them with `jq`. The structured `signatures[]` section and the contributor workflow are part of Spec 101 US2.
- **No "check now" button in the Web UI.** The Security tab shows the refresh rows; `mcpproxy security bundle refresh` and the REST endpoint run a cycle.

## See also

- [Security Quarantine: Signature bundle](/features/security-quarantine#signature-bundle-offline-tpa-corpus)
- [Security Quarantine: Re-protection after a bundle activates](/features/security-quarantine#re-protection-after-a-bundle-activates)
- [Tool Scanner: Signature corpus and coverage](/features/tool-scanner#signature-corpus-and-coverage)
- Spec: `specs/101-tpa-db/spec.md`, walkthrough: `specs/101-tpa-db/quickstart.md`
