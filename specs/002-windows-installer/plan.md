# Implementation Plan: Windows Installer for MCPProxy

**Branch**: `002-windows-installer` | **Date**: 2025-11-13 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/002-windows-installer/spec.md`

## Summary

Create a professional Windows installer for MCPProxy that bundles both the core server (`mcpproxy.exe`) and tray application (`mcpproxy-tray.exe`), automatically configures system PATH, creates Start Menu shortcuts, and integrates into the CI/CD pipeline for automated builds on both production releases and prerelease branches. The installer will use WiX Toolset 4.x to generate MSI packages with in-place upgrade support, silent installation mode, and Windows 10 version 21H2+ compatibility.

**Primary Deliverables**:
1. WiX 4.x installer definition (`.wxs` files) for both amd64 and arm64 architectures
2. Windows-adapted welcome and completion RTF screens
3. GitHub Actions workflow integration for automated installer builds
4. Local build script for developer testing
5. Documentation updates (CLAUDE.md, README.md)

## Technical Context

**Language/Version**: Go 1.25 (existing), WiX Toolset 4.x (new), PowerShell 7.x (scripting)
**Primary Dependencies**:
- WiX Toolset 4.x (open-source Windows installer framework)
- GitHub Actions runners (windows-latest)
- Existing Go build toolchain
- RTF formatting tools (existing macOS .rtf files as templates)

**Storage**: N/A (installer is build artifact, not runtime component)
**Testing**:
- Manual testing on Windows 10 21H2+ and Windows 11 (amd64/arm64 VMs)
- Automated smoke tests in GitHub Actions (installer creation validation)
- Local VM testing workflow for iterative development

**Target Platform**: Windows 10 version 21H2 (November 2021 Update) or later, Windows 11 (amd64 and arm64)
**Project Type**: Build tooling and CI/CD integration (installer generation)
**Performance Goals**:
- Installer build completes within 5 minutes in GitHub Actions
- Installation completes in under 3 minutes on target systems
- Local test builds complete within 2 minutes

**Constraints**:
- Unsigned binaries initially (no code signing certificates available)
- Must mirror existing macOS installer functionality
- Must integrate with existing release and prerelease workflows
- Must support silent/unattended installation for enterprise deployments

**Scale/Scope**:
- 2 installer variants (amd64 and arm64)
- 2 binaries bundled per installer (core + tray)
- 2 workflow integrations (release.yml and prerelease.yml)
- 1 local build script for developers

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

### I. Performance at Scale
✅ **PASS** - Installer is a build-time artifact, not a runtime component. Does not impact MCPProxy's runtime performance, tool indexing, or search capabilities.

### II. Actor-Based Concurrency
✅ **PASS** - Installer generation is a sequential build process. No concurrent operations or actor patterns required.

### III. Configuration-Driven Architecture
✅ **PASS** - Installer does not modify MCPProxy's configuration-driven architecture. It deploys the existing configuration system unchanged (mcp_config.json remains the source of truth).

### IV. Security by Default
✅ **PASS** - Installer preserves all existing security defaults:
- Binaries installed use localhost-only binding (127.0.0.1) by default
- No changes to quarantine, API key, or Docker isolation features
- Installer requires administrator privileges (system-level PATH modification)
- Unsigned binaries initially, but installer framework supports future code signing

**Note**: Windows Defender SmartScreen will warn about unsigned installers. This is expected and documented behavior. Users can bypass via "More Info" → "Run Anyway". Future enhancement: Add code signing with Microsoft-issued certificate.

### V. Test-Driven Development (TDD)
⚠️ **PARTIAL** - Installer testing is primarily manual due to nature of installation workflows:
- **Automated**: GitHub Actions validates installer creation (smoke test)
- **Manual**: Full installation testing on Windows VMs (developer-driven)
- **Future Enhancement**: Add automated E2E tests using PowerShell scripts to install/uninstall/verify in GitHub Actions Windows runners

**Justification**: Full E2E installer testing requires Windows GUI automation and elevated privileges, which adds significant complexity for initial release. Manual testing on VMs provides sufficient coverage for v1.

### VI. Documentation Hygiene
✅ **PASS** - Plan includes comprehensive documentation updates:
- CLAUDE.md: Add Windows installer build instructions
- README.md: Add Windows installation instructions
- Installer scripts: Inline comments for WiX definitions
- Workflow YAML: Comments explaining Windows-specific steps

### Post-Design Re-Check
To be completed after Phase 1 (data-model.md, contracts/, quickstart.md).

## Project Structure

### Documentation (this feature)

```text
specs/002-windows-installer/
├── plan.md              # This file (/speckit.plan command output)
├── research.md          # Phase 0 output: WiX vs NSIS comparison, best practices
├── data-model.md        # Phase 1 output: Installer components and relationships
├── quickstart.md        # Phase 1 output: Developer guide for building/testing installers
├── contracts/           # Phase 1 output: Windows installer schema (WiX component definitions)
└── tasks.md             # Phase 2 output (/speckit.tasks command - NOT created by /speckit.plan)
```

### Source Code (repository root)

```text
# Existing structure (unchanged)
cmd/
├── mcpproxy/           # Core server binary
└── mcpproxy-tray/      # Tray application binary

scripts/
├── create-installer-dmg.sh       # Existing macOS installer
├── create-windows-installer.ps1  # NEW: Windows installer build script
├── build.sh                      # Existing: May need Windows-specific additions
└── installer-resources/
    ├── windows/                  # NEW: Windows-specific resources
    │   ├── welcome.rtf           # NEW: Windows welcome screen
    │   ├── conclusion.rtf        # NEW: Windows completion screen
    │   ├── banner.bmp            # NEW: Installer banner image (493x58)
    │   └── dialog.bmp            # NEW: Installer dialog image (493x312)
    ├── welcome_en.rtf            # Existing macOS version (template)
    └── conclusion_en.rtf         # Existing macOS version (template)

wix/                              # NEW: WiX Toolset definitions
├── mcpproxy-amd64.wxs            # NEW: Main installer definition (amd64)
├── mcpproxy-arm64.wxs            # NEW: Main installer definition (arm64)
├── components.wxs                # NEW: Shared component definitions
└── ui.wxs                        # NEW: Custom UI definitions (optional)

.github/workflows/
├── release.yml                   # MODIFIED: Add Windows installer build steps
└── prerelease.yml                # MODIFIED: Add Windows installer build steps
```

**Structure Decision**: Windows installer generation is an extension of existing build tooling. New `wix/` directory at repository root contains WiX definitions. Windows-specific resources in `scripts/installer-resources/windows/` mirror macOS structure. Build scripts in `scripts/` directory follow existing naming conventions.

## Complexity Tracking

No constitution violations requiring justification.

**Rationale**: This feature is purely build tooling and CI/CD integration. It does not introduce new runtime components, concurrency patterns, or architectural layers. All constitution principles are satisfied.

---

## Phase 0: Research & Technology Selection

### Research Questions

1. **WiX Toolset vs NSIS vs Inno Setup**: Which installer framework best fits MCPProxy requirements?
   - **Evaluation Criteria**:
     - In-place upgrade support (FR-019)
     - Silent installation mode (FR-018)
     - System PATH modification (FR-003)
     - Start Menu shortcut creation (FR-004)
     - Version information embedding (FR-009)
     - Community support and documentation quality
     - GitHub Actions integration complexity

2. **WiX 4.x Best Practices**: How to structure `.wxs` files for maintainability?
   - Component vs Feature organization
   - Shared component definitions for multi-arch builds
   - Custom UI integration patterns
   - Upgrade code and product code management

3. **RTF Screen Conversion**: How to convert macOS RTF files to Windows-compatible format?
   - RTF dialect differences (macOS vs Windows)
   - Conversion tools and validation
   - Image embedding requirements

4. **GitHub Actions Windows Runners**: What are the capabilities and limitations?
   - Pre-installed tools on windows-latest runners
   - WiX Toolset installation methods
   - Artifact upload size limits
   - Build time constraints

5. **Local Testing Workflow**: How to enable fast iteration cycles for developers?
   - Minimal VM setup requirements
   - Uninstall/reinstall automation
   - Debugging MSI installation failures

### Research Tasks (to be dispatched to agents)

1. **Task**: Research WiX Toolset 4.x vs NSIS vs Inno Setup for MCPProxy Windows installer
   - **Context**: Need MSI-based installer for enterprise support, in-place upgrades, and Windows best practices
   - **Deliverable**: Comparison table with pros/cons, recommendation with rationale

2. **Task**: Find WiX Toolset 4.x best practices for multi-architecture Go applications
   - **Context**: Building both amd64 and arm64 installers with shared component definitions
   - **Deliverable**: Sample `.wxs` structure, component organization patterns, upgrade strategies

3. **Task**: Research RTF screen formatting for Windows Installer UI
   - **Context**: Adapting existing macOS RTF files (welcome_en.rtf, conclusion_en.rtf) to Windows
   - **Deliverable**: Conversion process, validation tools, formatting requirements

4. **Task**: Research GitHub Actions Windows runner capabilities for WiX builds
   - **Context**: Automating installer builds in release and prerelease workflows
   - **Deliverable**: WiX installation methods, runner pre-installed tools, artifact management

5. **Task**: Find best practices for Windows installer testing workflows
   - **Context**: Enabling developers to test installers on Windows VMs without full GitHub releases
   - **Deliverable**: Local build script patterns, VM testing automation, uninstall/reinstall strategies

**Output**: `research.md` consolidating all findings with decisions, rationales, and alternatives considered.

---

## Phase 1: Design & Contracts

### Prerequisites
- `research.md` complete with technology selections finalized

### Data Model

Extract installer components and relationships → `data-model.md`:

**Entities**:
1. **Installer Package (MSI)**
   - Product Code: GUID (unique per version)
   - Upgrade Code: GUID (same across versions for in-place upgrades)
   - Version: Matches Git tag (e.g., 1.0.0)
   - Architecture: amd64 or arm64
   - Minimum Windows Version: 10.0.19044 (Windows 10 21H2)

2. **File Components**
   - mcpproxy.exe: Core server binary
   - mcpproxy-tray.exe: Tray application binary
   - Installation Directory: `C:\Program Files\MCPProxy\`

3. **Environment Variables**
   - System PATH: Appends installation directory
   - Scope: Machine-level (all users)

4. **Start Menu Shortcuts**
   - Name: "MCPProxy Tray"
   - Target: `mcpproxy-tray.exe`
   - Working Directory: Installation directory
   - Icon: Embedded from mcpproxy-tray.exe

5. **Registry Entries**
   - Uninstall information: `HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{ProductCode}`
   - Product version, publisher, install location

6. **Custom Actions**
   - Post-install: Optional launch of mcpproxy-tray.exe (FR-005)
   - Pre-install: Check for running processes (mcpproxy.exe, mcpproxy-tray.exe)

**Relationships**:
- Package → Components (1:N): One installer contains multiple file components
- Component → File (1:1): Each component installs one primary file
- Package → Environment Variable (1:1): One installer modifies one PATH variable
- Package → Shortcuts (1:N): One installer creates one or more shortcuts
- Package → Registry Entries (1:N): One installer writes multiple registry keys

**State Transitions**:
- **New Installation**: No previous version → Full installation
- **In-Place Upgrade**: Previous version detected → Remove old binaries, install new binaries, preserve user data in `%USERPROFILE%\.mcpproxy`
- **Uninstallation**: Installer present → Remove binaries, shortcuts, and PATH entry, preserve user data

**Validation Rules**:
- Disk space ≥ 100 MB before installation (FR-006)
- Administrator privileges required (FR-020)
- Windows version ≥ 10.0.19044 (FR-006)
- Installation directory must be writable

### API Contracts

No REST API changes required. This feature is pure build tooling.

**Output**: `data-model.md` with detailed entity definitions and relationships.

### Contracts Directory

```text
contracts/
├── mcpproxy-amd64.wxs         # WiX installer schema for amd64
├── mcpproxy-arm64.wxs         # WiX installer schema for arm64
├── components-schema.md       # Documentation of WiX component structure
└── upgrade-logic.md           # Documentation of in-place upgrade strategy
```

**Output**: `/contracts/` directory with WiX schema definitions and upgrade logic documentation.

### Quickstart Guide

**Output**: `quickstart.md` with developer instructions for:
1. Installing WiX Toolset 4.x locally (Windows only)
2. Building Windows installer locally: `.\scripts\create-windows-installer.ps1 -Version v1.0.0 -Arch amd64`
3. Testing installer on Windows VM:
   - Install: `msiexec /i mcpproxy-v1.0.0-windows-amd64.msi /qn` (silent)
   - Uninstall: `msiexec /x {ProductCode} /qn` (silent)
   - Log output: `msiexec /i mcpproxy.msi /l*v install.log`
4. Triggering prerelease builds via `next` branch pushes
5. Downloading installer artifacts from GitHub Actions workflow runs

### Agent Context Update

Run `.specify/scripts/bash/update-agent-context.sh claude` to update `CLAUDE.md` with:
- Windows installer build instructions
- WiX Toolset 4.x as new dependency
- Local testing workflow for Windows VMs
- Prerelease workflow for testing without main branch releases

**Output**: Updated `CLAUDE.md` with Windows installer context.

---

## Phase 2: Task Generation (Deferred)

Task breakdown and dependency ordering will be handled by `/speckit.tasks` command (not part of `/speckit.plan`).

**Expected Task Categories**:
1. **Setup & Dependencies** (P1)
   - Install WiX Toolset in GitHub Actions runners
   - Create Windows installer resources directory structure

2. **WiX Definitions** (P1)
   - Write main installer `.wxs` files (amd64 and arm64)
   - Define components (binaries, PATH, shortcuts)
   - Configure in-place upgrade logic

3. **RTF Screens** (P2)
   - Convert macOS RTF files to Windows format
   - Create Windows-specific welcome and conclusion screens

4. **Build Scripts** (P1)
   - Write PowerShell script for local installer builds
   - Integrate into existing `scripts/` directory

5. **CI/CD Integration** (P1)
   - Modify `release.yml` workflow for Windows installers
   - Modify `prerelease.yml` workflow for Windows installers

6. **Documentation** (P2)
   - Update CLAUDE.md with Windows installer instructions
   - Update README.md with Windows installation guide
   - Create quickstart.md for developers

7. **Testing & Validation** (P2)
   - Manual testing on Windows 10 21H2 amd64 VM
   - Manual testing on Windows 11 arm64 VM (if available)
   - Validate silent installation mode
   - Validate in-place upgrade behavior

---

## Risks & Mitigations

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| WiX 4.x learning curve slows development | High | Medium | Research phase front-loads learning; leverage WiX documentation and community examples |
| Unsigned installers trigger Windows Defender warnings | Medium | High | Document workaround in README; plan for future code signing certificate acquisition |
| GitHub Actions Windows runners lack WiX Toolset | High | Low | Use `dotnet tool install` or Chocolatey to install WiX in workflow; validate in PR testing |
| RTF screen conversion introduces formatting issues | Low | Medium | Use RTF validation tools; manual review on Windows VM before merging |
| In-place upgrades fail to preserve user data | High | Low | Thoroughly test upgrade scenarios; WiX upgrade logic is well-documented pattern |
| ARM64 testing difficult without physical hardware | Medium | High | Use Windows 11 ARM64 VMs (Parallels on Apple Silicon); accept limited ARM64 testing for v1 |

---

## Success Metrics

- ✅ Windows installer artifacts appear on GitHub releases page for every tagged release
- ✅ Prerelease installer artifacts available from "next" branch workflow runs
- ✅ Developers can build and test installers locally within 5 minutes
- ✅ Installation completes in under 3 minutes on Windows 10/11
- ✅ `mcpproxy --help` command works immediately after installation (PATH configured)
- ✅ Tray application launches from Start Menu and starts core server within 10 seconds
- ✅ In-place upgrades preserve user configuration in `%USERPROFILE%\.mcpproxy`
- ✅ Uninstallation cleanly removes binaries and shortcuts, preserves user data

---

## Open Questions (to be resolved in research phase)

1. Should we use WiX 4.x (latest) or WiX 3.x (more mature ecosystem)?
   - **Lean**: WiX 4.x (modern, .NET-based, future-proof)
   - **Research needed**: Community adoption status, GitHub Actions support

2. Do we need custom UI or is the standard WiX UI sufficient?
   - **Lean**: Standard WiX UI (faster implementation)
   - **Consider**: Custom UI if branding is important

3. Should we bundle .NET runtime with installer or require pre-installation?
   - **Context**: WiX 4.x requires .NET 6+ runtime
   - **Research needed**: GitHub Actions runner capabilities

4. How to handle code signing for future releases?
   - **Current**: Unsigned (acceptable for v1)
   - **Future**: Investigate Microsoft Partner Network or EV code signing certificates

---

## Appendices

### Appendix A: Installer File Naming Convention

- **Pattern**: `mcpproxy-{version}-windows-{arch}-installer.msi`
- **Examples**:
  - `mcpproxy-v1.0.0-windows-amd64-installer.msi`
  - `mcpproxy-v1.0.0-next.abc123-windows-arm64-installer.msi`

### Appendix B: WiX Component IDs

- **Core Binary**: `mcpproxy.exe.Component`
- **Tray Binary**: `mcpproxy_tray.exe.Component`
- **PATH Environment Variable**: `SystemPath.Component`
- **Start Menu Shortcut**: `StartMenuShortcut.Component`

### Appendix C: Product and Upgrade Codes

- **Upgrade Code** (constant across versions): `{XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX}`
- **Product Code** (unique per version): `{YYYYYYYY-YYYY-YYYY-YYYY-YYYYYYYYYYYY}`
  - Generated dynamically per build using WiX tooling

### Appendix D: Silent Installation Examples

```powershell
# Silent install
msiexec /i mcpproxy-v1.0.0-windows-amd64-installer.msi /qn /l*v install.log

# Silent uninstall
msiexec /x {ProductCode} /qn /l*v uninstall.log

# Silent upgrade (auto-detected by WiX)
msiexec /i mcpproxy-v1.1.0-windows-amd64-installer.msi /qn /l*v upgrade.log
```
