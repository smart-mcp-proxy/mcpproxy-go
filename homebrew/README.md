# Homebrew Distribution for MCPProxy

MCPProxy is distributed via Homebrew in two ways:

| Package | Type | What it installs |
|---------|------|-----------------|
| `mcpproxy` (formula) | CLI | Headless server binary, built from source |
| `mcpproxy` (cask) | GUI | macOS tray app + CLI via signed PKG installer |

## Quick Start

### Install the CLI (formula, builds from source)

```bash
brew tap smart-mcp-proxy/mcpproxy
brew install mcpproxy
```

### Install the tray app (cask, prebuilt DMG)

```bash
brew tap smart-mcp-proxy/mcpproxy
brew install --cask mcpproxy
```

## Setting Up the Tap Repository

The tap repository must be created at `github.com/smart-mcp-proxy/homebrew-mcpproxy` with
this directory structure:

```
homebrew-mcpproxy/
  Formula/
    mcpproxy.rb      # copy from homebrew/Formula/mcpproxy.rb
  Casks/
    mcpproxy.rb      # copy from homebrew/Casks/mcpproxy.rb
```

### Steps

1. Create the repo:
   ```bash
   gh repo create smart-mcp-proxy/homebrew-mcpproxy --public \
     --description "Homebrew tap for MCPProxy"
   ```

2. Copy the formula and cask files:
   ```bash
   git clone git@github.com:smart-mcp-proxy/homebrew-mcpproxy.git
   cd homebrew-mcpproxy
   mkdir -p Formula Casks
   cp /path/to/mcpproxy-go/homebrew/Formula/mcpproxy.rb Formula/
   cp /path/to/mcpproxy-go/homebrew/Casks/mcpproxy.rb Casks/
   git add -A && git commit -m "Add mcpproxy formula and cask"
   git push
   ```

3. Test:
   ```bash
   brew tap smart-mcp-proxy/mcpproxy
   brew install mcpproxy              # formula (CLI)
   brew install --cask mcpproxy       # cask (tray app)
   ```

## Updating to a New Release

Run the update script from the mcpproxy-go repo:

```bash
./homebrew/update-formula.sh v0.21.0    # specific version
./homebrew/update-formula.sh            # auto-detect latest
```

This downloads the source tarball and DMG assets, computes SHA256 hashes, and updates
both files in place. Then copy the updated files to the tap repo and push.

## Submitting to homebrew-core

The `mcpproxy` formula (CLI) can be submitted to homebrew-core once the project meets
the acceptance criteria:

### Requirements

- **75+ GitHub stars** (currently 155+, met)
- **30+ forks OR 30+ watchers** (check current count)
- **No vendored dependencies** (uses Go modules, OK)
- **MIT license** (met)
- **Stable versioning** (met, uses semver tags)
- **Working test block** (included)

### Submission Steps

1. Fork `Homebrew/homebrew-core`
2. Add `Formula/m/mcpproxy.rb` with the formula content
3. Run local checks:
   ```bash
   brew audit --new --formula Formula/m/mcpproxy.rb
   brew install --build-from-source Formula/m/mcpproxy.rb
   brew test Formula/m/mcpproxy.rb
   ```
4. Open a PR to `Homebrew/homebrew-core`

### Notes for homebrew-core

- Remove the `head` stanza (not allowed in homebrew-core)
- Remove the `typed` and `frozen_string_literal` comments
- The `service` block is allowed in homebrew-core
- The `node` build dependency is acceptable (many Go projects embed web UIs)

## Submitting the Cask to homebrew-cask

Casks have a higher bar for acceptance:

### Requirements

- **225+ GitHub stars** (not yet met, currently ~155)
- Signed and notarized macOS binary (met)
- Stable download URLs (met, GitHub Releases)

### When ready

1. Fork `Homebrew/homebrew-cask`
2. Add `Casks/m/mcpproxy.rb` with the cask content
3. Run:
   ```bash
   brew audit --new --cask Casks/m/mcpproxy.rb
   brew install --cask Casks/m/mcpproxy.rb
   ```
4. Open a PR to `Homebrew/homebrew-cask`

Until then, the cask is available through the tap (`brew tap smart-mcp-proxy/mcpproxy`).
