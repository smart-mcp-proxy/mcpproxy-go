# Contract: `mcpproxy-rpm` R2 Bucket Layout

Custom domain: `rpm.mcpproxy.app`. Public read.

## Object keys

```
rpm.mcpproxy.app/
├── mcpproxy.gpg                                    # public key (binary OpenPGP)
├── mcpproxy.gpg.asc                                # public key (ASCII-armored)
├── mcpproxy.repo                                   # dnf source definition (see template below)
├── x86_64/
│   ├── mcpproxy-{VERSION}-1.x86_64.rpm             # up to 10 versions
│   └── repodata/
│       ├── repomd.xml                              # index
│       ├── repomd.xml.asc                          # detached armored signature
│       ├── *-primary.xml.gz                        # package metadata
│       ├── *-filelists.xml.gz                      # file lists
│       └── *-other.xml.gz                          # changelogs
└── aarch64/
    ├── mcpproxy-{VERSION}-1.aarch64.rpm
    └── repodata/
        ├── repomd.xml
        ├── repomd.xml.asc
        ├── *-primary.xml.gz
        ├── *-filelists.xml.gz
        └── *-other.xml.gz
```

## `mcpproxy.repo` template (served at the repo root)

```ini
[mcpproxy]
name=MCPProxy
baseurl=https://rpm.mcpproxy.app/$basearch
enabled=1
gpgcheck=1
gpgkey=https://rpm.mcpproxy.app/mcpproxy.gpg
repo_gpgcheck=1
```

`$basearch` is expanded by dnf to `x86_64` or `aarch64`. `repo_gpgcheck=1` forces dnf to verify the `repomd.xml` detached signature — belt-and-braces on top of the per-package RPM signature.

## Cache headers

| Path pattern | Cache-Control |
|---|---|
| `{x86_64,aarch64}/repodata/repomd.xml*` | `public, max-age=60, must-revalidate` |
| `{x86_64,aarch64}/repodata/*.xml.gz` | `public, max-age=60, must-revalidate` |
| `{x86_64,aarch64}/*.rpm` | `public, max-age=31536000, immutable` |
| `mcpproxy.gpg*` | `public, max-age=86400` |
| `mcpproxy.repo` | `public, max-age=3600` |

## User-facing install snippet (one-command)

```bash
sudo dnf config-manager --add-repo https://rpm.mcpproxy.app/mcpproxy.repo
sudo dnf install -y mcpproxy
```

## User-facing install snippet (explicit, for distros without `dnf config-manager`)

```bash
sudo tee /etc/yum.repos.d/mcpproxy.repo > /dev/null << 'EOF'
[mcpproxy]
name=MCPProxy
baseurl=https://rpm.mcpproxy.app/$basearch
enabled=1
gpgcheck=1
gpgkey=https://rpm.mcpproxy.app/mcpproxy.gpg
repo_gpgcheck=1
EOF
sudo dnf install -y mcpproxy
```
