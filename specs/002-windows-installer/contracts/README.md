# Installer Contracts

This directory contains **contract/schema files** that document the Windows installer structure and configuration. These are **not** the actual implementation files used by the build system.

## Files

### `installer-inno-setup.iss`
Schema for Inno Setup installer (recommended implementation). Shows:
- Multi-architecture support (single installer for amd64/arm64)
- System PATH modification
- Start Menu shortcut creation
- In-place upgrade logic via AppId
- Post-install launch option

**Actual implementation**: `scripts/installer.iss` (to be created)

### `components-schema.md` (this file, coming next)
Documentation of installer components, relationships, and data flow.

## Usage

These contract files serve as:
1. **Design documentation** - What the installer should do
2. **Reference templates** - Starting point for implementation
3. **Validation spec** - What to test after implementation

## Next Steps

1. Copy `installer-inno-setup.iss` to `scripts/installer.iss`
2. Adapt paths and parameters for actual repository structure
3. Test locally on Windows VM
4. Integrate into GitHub Actions workflows

See [quickstart.md](../quickstart.md) for build and test instructions.
