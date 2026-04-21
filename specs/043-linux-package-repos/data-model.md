# Phase 1 Data Model — Linux Package Repositories

No database, no runtime-generated state. The "data model" here is the layout of objects in R2 and the schemas of the metadata files that apt and dnf consume. It is the contract between the publish job and the package manager clients.

## Entities

### Package Artifact

A built `.deb` or `.rpm` file.

| Field | Type | Description |
|---|---|---|
| `name` | string | Package name, always `mcpproxy`. |
| `version` | semver | Matches the git tag minus the leading `v`. |
| `architecture` | enum | `amd64`, `arm64` for deb; `x86_64`, `aarch64` for rpm. |
| `format` | enum | `deb` or `rpm`. |
| `file_size_bytes` | int | File size. |
| `sha256` | hex | SHA-256 of the file contents. |
| `pool_key` | string | Object key in R2. See layout below. |

**Filename conventions (inherited from existing build job)**:
- `mcpproxy_{VERSION}_{amd64|arm64}.deb`
- `mcpproxy-{VERSION}-1.{x86_64|aarch64}.rpm`

**Storage**:
- apt artifacts → `s3://mcpproxy-apt/pool/main/m/mcpproxy/{filename}`
- rpm artifacts → `s3://mcpproxy-rpm/{x86_64|aarch64}/{filename}`

### Repository Metadata (apt)

Generated fresh by `apt-ftparchive` on every publish run.

| Object key | Purpose |
|---|---|
| `dists/stable/main/binary-amd64/Packages` | Plain-text index: one stanza per `.deb`, including `Filename:` (relative to repo base), SHA-256, size, description. |
| `dists/stable/main/binary-amd64/Packages.gz` | gzip-compressed; apt prefers it. |
| `dists/stable/main/binary-arm64/Packages` | Same, for arm64. |
| `dists/stable/main/binary-arm64/Packages.gz` | |
| `dists/stable/Release` | Top-level suite manifest: `Suite: stable`, `Codename: stable`, `Architectures: amd64 arm64`, `Components: main`, list of SHA-256 of the `Packages*` files. |
| `dists/stable/Release.gpg` | Detached GPG signature of `Release`. |
| `dists/stable/InRelease` | Clearsigned `Release` (content + signature in one file; modern apt prefers this). |
| `mcpproxy.gpg` | Public GPG key. Served as both binary and armored (armored version at `mcpproxy.gpg.asc`). |

### Repository Metadata (rpm)

Generated fresh by `createrepo_c` on every publish run, per architecture directory.

| Object key | Purpose |
|---|---|
| `{x86_64\|aarch64}/repodata/repomd.xml` | Index of the other metadata files, with SHA-256s. |
| `{x86_64\|aarch64}/repodata/repomd.xml.asc` | Detached armored GPG signature of `repomd.xml`. |
| `{x86_64\|aarch64}/repodata/*-primary.xml.gz` | Primary package metadata (name, version, dependencies, file size, checksum). |
| `{x86_64\|aarch64}/repodata/*-filelists.xml.gz` | File lists per package. |
| `{x86_64\|aarch64}/repodata/*-other.xml.gz` | Changelogs and other metadata. |
| `mcpproxy.repo` | Pre-canned dnf source definition, written at publish time. Uses `$basearch` so one file serves both arches. |
| `mcpproxy.gpg` | Same public key file as in the apt bucket, so `dnf` users can fetch from either URL. |

### Signing Key

| Field | Value |
|---|---|
| Type | RSA |
| Length | 4096 bits |
| UID | `MCPProxy Packages <mcpproxy-packages@mcpproxy.app>` |
| Expiry | 2031-04-21 (5 years from creation) |
| Subkey | One encryption subkey is created (GPG default); not used for signing. The primary key signs. |
| Fingerprint | Assigned at generation; recorded in spec quickstart and in `contrib/signing/mcpproxy-packages.asc` comments. |

### Repository Channel

Only one channel exists today: `stable`. The apt suite name `stable` and the URL structure `dists/stable/...` intentionally leaves room for a future `prerelease` suite as a sibling, without restructuring.

## Retention Rule

- Let N = number of distinct versions currently present in the pool (apt) or per-arch directory (rpm).
- If N > 10, delete artifacts for the oldest `N - 10` versions (comparing by semver).
- Rule applies independently per bucket and, for rpm, per architecture directory.
- After pruning, regenerate metadata from the remaining pool contents.

## Integrity Invariants

- Every artifact listed in `Packages` or `*-primary.xml.gz` **must** exist at its listed path.
- `Release` file **must** be signed; unsigned or mis-signed `Release` means clients refuse to install.
- `mcpproxy.gpg` file served on the repo **must** be the public half of the key that signed the metadata.
- The 10-release retention window **must** be enforced after every publish, not just when crossing the boundary.

## Derived State (computed per publish run)

- List of existing pool files (from `aws s3 ls`).
- Parsed `(version, architecture, format)` tuple per filename via regex.
- Sorted version list, top 10 kept.
- Delta between "current pool" and "pool after adding new artifacts + pruning to top 10" determines the subsequent sync actions.

No state is persisted across runs other than what is already in the R2 buckets.
