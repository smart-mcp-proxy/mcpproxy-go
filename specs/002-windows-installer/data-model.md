# Data Model: Windows Installer Components

**Feature**: 002-windows-installer
**Phase**: Phase 1 - Design & Contracts
**Date**: 2025-11-13

---

## Overview

This document defines the data model for the Windows installer, including all components, relationships, and state transitions. The installer is a build-time artifact that packages the MCPProxy binaries and configures the Windows environment for immediate use.

**Note**: Based on research findings, this model applies to both Inno Setup (recommended) and WiX Toolset implementations with minor syntax differences.

---

## 1. Installer Package

The top-level entity representing the complete installer file.

### Attributes

| Attribute | Type | Description | Constraints |
|-----------|------|-------------|-------------|
| **Package Name** | String | Display name shown in installer UI | "MCPProxy" |
| **Version** | String | Semantic version | Format: `X.Y.Z` (e.g., "1.0.0") |
| **Product Code** | GUID | Unique identifier per version | Auto-generated per build (WiX) or N/A (Inno Setup) |
| **Upgrade Code** | GUID | Constant across all versions | `{A1B2C3D4-E5F6-7890-ABCD-EF1234567890}` (fixed) |
| **Architecture** | Enum | Target CPU architecture | `amd64` or `arm64` |
| **Minimum Windows Version** | String | OS version requirement | "10.0.19044" (Windows 10 21H2) |
| **Output Filename** | String | Installer file name | `mcpproxy-{version}-windows-{arch}-installer.{ext}` |
| **File Size** | Integer | Installer size in bytes | ~15-20 MB (estimated) |
| **Compression Level** | Enum | Compression algorithm | `high` (WiX) or `lzma2` (Inno Setup) |

### Relationships

- **Contains**: File Components (1:N)
- **Modifies**: Environment Variables (1:N)
- **Creates**: Shortcuts (1:N)
- **Writes**: Registry Entries (1:N)
- **Executes**: Custom Actions (1:N)

### Validation Rules

- Version MUST match Git tag format: `v{X}.{Y}.{Z}`
- Upgrade Code MUST remain constant across all versions
- Product Code MUST be unique per version-architecture combination
- Minimum Windows Version MUST be validated during installation
- Disk space check MUST ensure ≥100 MB available

---

## 2. File Components

Individual files installed to the target system.

### 2.1 Core Binary Component

| Attribute | Type | Description | Constraints |
|-----------|------|-------------|-------------|
| **File ID** | String | Unique component identifier | `CoreBinary` (WiX) or implicit (Inno Setup) |
| **Source Path** | Path | Build artifact location | `dist/windows-{arch}/mcpproxy.exe` |
| **Destination** | Path | Install location | `{InstallDir}\mcpproxy.exe` |
| **Architecture** | Enum | Binary architecture | Must match installer architecture |
| **File Size** | Integer | Binary size in bytes | ~10 MB (estimated, CGO disabled) |
| **Version** | String | Binary version | Embedded via `ldflags` |
| **KeyPath** | Boolean | Component detection file | `yes` (primary file) |

**Relationships**:
- **Associated**: Start Menu Shortcut (0:1, optional)
- **Enables**: CLI Command Access (via PATH)

### 2.2 Tray Application Component

| Attribute | Type | Description | Constraints |
|-----------|------|-------------|-------------|
| **File ID** | String | Unique component identifier | `TrayBinary` (WiX) or implicit (Inno Setup) |
| **Source Path** | Path | Build artifact location | `dist/windows-{arch}/mcpproxy-tray.exe` |
| **Destination** | Path | Install location | `{InstallDir}\mcpproxy-tray.exe` |
| **Architecture** | Enum | Binary architecture | Must match installer architecture |
| **File Size** | Integer | Binary size in bytes | ~8 MB (estimated, CGO enabled for GUI) |
| **Icon** | Icon Resource | Embedded application icon | Extracted from .exe for shortcuts |
| **KeyPath** | Boolean | Component detection file | `yes` (primary file) |

**Relationships**:
- **Associated**: Start Menu Shortcut (1:1, required)
- **Associated**: Desktop Shortcut (0:1, optional)
- **Target of**: Post-Install Launch Action (optional)

### 2.3 Installation Directory

| Attribute | Type | Description | Default Value |
|-----------|------|-------------|---------------|
| **Base Directory** | Path | Program Files location | `C:\Program Files\MCPProxy` (auto-resolves per arch) |
| **Permissions** | ACL | Directory access rights | Inherited from Program Files (admin write, user read) |
| **Persistence** | Enum | Survives uninstall? | `no` (removed on uninstall) |

**Contents**:
- `mcpproxy.exe` (Core Binary)
- `mcpproxy-tray.exe` (Tray Application)
- `unins000.exe` (Uninstaller, Inno Setup) or MSI cache (WiX)

**Relationships**:
- **Added to**: System PATH Environment Variable
- **Referenced by**: All File Components

---

## 3. Environment Variables

System-wide or user-level environment variables modified by the installer.

### 3.1 System PATH Component

| Attribute | Type | Description | Constraints |
|-----------|------|-------------|-------------|
| **Variable Name** | String | Environment variable key | `PATH` |
| **Scope** | Enum | Variable visibility | `System` (all users, FR-003 requirement) |
| **Value** | String | Appended path | `C:\Program Files\MCPProxy` |
| **Position** | Enum | Append or prepend | `Append` (add to end of PATH) |
| **Permanent** | Boolean | Survives uninstall? | `no` (removed on uninstall) |
| **Duplicate Check** | Boolean | Prevent duplicate entries | `yes` (check before adding) |

**Validation Rules**:
- PATH length MUST NOT exceed 2047 characters (Windows limit)
- Installation directory MUST exist before PATH modification
- Duplicate detection MUST use case-insensitive comparison
- PATH modification REQUIRES administrator privileges

**Relationships**:
- **References**: Installation Directory
- **Enables**: CLI Command Access (immediate terminal use)

**State Transitions**:
```
[Before Install] → PATH does not contain InstallDir
[After Install]  → PATH contains InstallDir at end
[After Uninstall] → PATH does not contain InstallDir (cleaned up)
```

---

## 4. Shortcuts

User interface shortcuts created in Start Menu and optionally Desktop.

### 4.1 Start Menu Shortcut (Tray Application)

| Attribute | Type | Description | Constraints |
|-----------|------|-------------|-------------|
| **Shortcut Name** | String | Display name | "MCPProxy" or "MCPProxy Tray" |
| **Target** | Path | Executable path | `{InstallDir}\mcpproxy-tray.exe` |
| **Working Directory** | Path | Launch context | `{InstallDir}` |
| **Icon** | Icon Resource | Shortcut icon | Extracted from `mcpproxy-tray.exe` |
| **Icon Index** | Integer | Icon resource index | `0` (first icon) |
| **Location** | Path | Shortcut directory | `C:\ProgramData\Microsoft\Windows\Start Menu\Programs\MCPProxy` |
| **Arguments** | String | Command-line args | Empty (no startup args) |

**Relationships**:
- **Targets**: Tray Application Binary
- **Located in**: Start Menu Programs Folder

### 4.2 Desktop Shortcut (Optional)

| Attribute | Type | Description | Constraints |
|-----------|------|-------------|-------------|
| **Enabled** | Boolean | User can opt-in during install | Default: `no` (FR-004: Start Menu only required) |
| **Shortcut Name** | String | Display name | "MCPProxy" |
| **Target** | Path | Executable path | `{InstallDir}\mcpproxy-tray.exe` |
| **Location** | Path | Shortcut directory | `C:\Users\{Username}\Desktop` |

**Note**: Desktop shortcut implementation optional for v1 (Start Menu is priority per FR-004).

---

## 5. Registry Entries

Windows Registry keys written for uninstallation metadata and application settings.

### 5.1 Uninstall Registry Key

| Attribute | Type | Description | Value |
|-----------|------|-------------|-------|
| **Root Key** | Registry Hive | Registry root | `HKEY_LOCAL_MACHINE` |
| **Subkey Path** | String | Uninstall metadata location | `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{ProductCode}` |
| **DisplayName** | String | Product name | "MCPProxy" |
| **DisplayVersion** | String | Product version | `{Version}` (e.g., "1.0.0") |
| **Publisher** | String | Manufacturer | "Smart MCP Proxy" |
| **InstallLocation** | Path | Install directory | `{InstallDir}` |
| **UninstallString** | String | Uninstaller command | Varies by framework (see below) |
| **DisplayIcon** | Path | Product icon | `{InstallDir}\mcpproxy-tray.exe,0` |
| **HelpLink** | URL | Support URL | `https://github.com/smart-mcp-proxy/mcpproxy-go` |
| **URLInfoAbout** | URL | Product homepage | `https://github.com/smart-mcp-proxy/mcpproxy-go` |
| **URLUpdateInfo** | URL | Update URL | `https://github.com/smart-mcp-proxy/mcpproxy-go/releases` |

**Framework-Specific Values**:

**Inno Setup**:
- `UninstallString`: `"{InstallDir}\unins000.exe"`
- `QuietUninstallString`: `"{InstallDir}\unins000.exe" /VERYSILENT`

**WiX**:
- `UninstallString`: `msiexec /x {ProductCode}`
- `QuietUninstallString`: `msiexec /qn /x {ProductCode}`

### 5.2 Application Settings (Optional)

| Attribute | Type | Description | Value |
|-----------|------|-------------|-------|
| **Root Key** | Registry Hive | Application settings root | `HKEY_CURRENT_USER` |
| **Subkey Path** | String | Settings location | `SOFTWARE\MCPProxy` |
| **InstallDate** | DateTime | Installation timestamp | ISO 8601 format |
| **Architecture** | String | Installed architecture | "amd64" or "arm64" |

**Purpose**: Track installation metadata for analytics (optional, not required for v1).

---

## 6. Custom Actions

Installer-triggered operations that execute during installation/uninstallation.

### 6.1 Process Detection Custom Action

**Trigger**: Before installation begins (during `InstallValidate` phase)

| Attribute | Type | Description | Constraints |
|-----------|------|-------------|-------------|
| **Action ID** | String | Unique identifier | `CheckRunningProcesses` |
| **Execution** | Enum | Timing | `immediate` (early in sequence) |
| **Return Behavior** | Enum | Failure handling | `check` (prompt user to close apps) |
| **Processes Monitored** | List<String> | Process names | `["mcpproxy.exe", "mcpproxy-tray.exe"]` |
| **User Prompt** | String | Close request message | "MCPProxy must be closed before installation." |
| **Retry Allowed** | Boolean | Can user retry after closing | `yes` |

**Behavior**:
```
1. Scan running processes for mcpproxy.exe and mcpproxy-tray.exe
2. If found:
   a. Display prompt: "MCPProxy must be closed. Please close it and try again."
   b. Provide buttons: [Close App] [Cancel Installation]
3. If [Close App]: Terminate processes gracefully (30s timeout)
4. If [Cancel Installation]: Exit installer
5. If no processes found: Continue installation
```

**Framework Implementation**:
- **Inno Setup**: Pascal script with `FindWindowByClassName` / `PostMessage(WM_CLOSE)`
- **WiX**: `util:CloseApplication` element with `PromptToContinue="yes"`

### 6.2 Post-Install Launch Custom Action

**Trigger**: After installation completes (during `InstallFinalize` phase)

| Attribute | Type | Description | Constraints |
|-----------|------|-------------|-------------|
| **Action ID** | String | Unique identifier | `LaunchTrayApplication` |
| **Execution** | Enum | Timing | `immediate` (user context) |
| **Return Behavior** | Enum | Failure handling | `asyncNoWait` (don't block installer exit) |
| **Condition** | Boolean Expression | When to run | `LaunchCheckbox=1 AND NOT Installed` (fresh install only) |
| **Command** | String | Executable path | `{InstallDir}\mcpproxy-tray.exe` |
| **Arguments** | String | Command-line args | Empty |

**Behavior**:
```
1. Check if "Launch MCPProxy Tray" checkbox is checked (default: checked)
2. Check if this is a fresh install (not an upgrade)
3. If both true:
   a. Launch mcpproxy-tray.exe in background
   b. Exit installer immediately (don't wait for app to close)
4. Else: Skip launch
```

**UI Element**:
- Checkbox on final installer screen: "Launch MCPProxy Tray"
- Default state: Checked
- Label: "Launch MCPProxy System Tray"

---

## 7. State Transitions

### 7.1 Installation States

```
[No Installation]
    ↓
[Disk Space Check: ≥100 MB?]
    ↓ yes
[Administrator Check: Elevated?]
    ↓ yes
[Process Check: Apps running?]
    ↓ no (or closed)
[Extract Files to {InstallDir}]
    ↓
[Add {InstallDir} to System PATH]
    ↓
[Create Start Menu Shortcut]
    ↓
[Write Uninstall Registry Entries]
    ↓
[Fresh Install Complete]
```

### 7.2 Upgrade States

```
[Previous Version Detected]
    ↓
[Check Upgrade Code: Matches?]
    ↓ yes
[Process Check: Apps running?]
    ↓ no (or closed)
[Remove Old Binaries from {InstallDir}]
    ↓
[Preserve User Data in %USERPROFILE%\.mcpproxy]
    ↓
[Extract New Files to {InstallDir}]
    ↓
[Update System PATH (if location changed)]
    ↓
[Update Start Menu Shortcut (if needed)]
    ↓
[Update Uninstall Registry Entries]
    ↓
[Upgrade Complete]
```

**Key Points**:
- Upgrade Code matches → In-place upgrade (FR-019)
- User data in `%USERPROFILE%\.mcpproxy` NEVER deleted
- Old binaries replaced, configuration preserved

### 7.3 Uninstallation States

```
[Uninstall Triggered]
    ↓
[Process Check: Apps running?]
    ↓ no (or closed)
[Remove {InstallDir} from System PATH]
    ↓
[Delete Start Menu Shortcut]
    ↓
[Delete {InstallDir} (binaries only)]
    ↓
[Remove Uninstall Registry Entries]
    ↓
[Preserve %USERPROFILE%\.mcpproxy]
    ↓
[Uninstallation Complete]
```

**Key Points**:
- User data in `%USERPROFILE%\.mcpproxy` preserved (FR-019)
- No prompt to delete user data (standard practice)
- Clean removal from "Add or Remove Programs" (FR-010)

---

## 8. Data Persistence

### 8.1 User Data Directory

**Location**: `%USERPROFILE%\.mcpproxy` (e.g., `C:\Users\johndoe\.mcpproxy`)

**Contents** (not managed by installer):
- `mcp_config.json` - User configuration
- `config.db` - BBolt database
- `index.bleve/` - Search index
- `logs/` - Application logs
- `certs/` - Self-signed certificates (optional HTTPS)

**Lifecycle**:
- Created by mcpproxy.exe on first run (NOT by installer)
- Preserved during upgrades (FR-019)
- Preserved during uninstallation (standard practice)

**Installer Interaction**: NONE (installer never touches this directory)

### 8.2 Installation Files

**Location**: `C:\Program Files\MCPProxy`

**Contents** (managed by installer):
- `mcpproxy.exe` - Core server binary
- `mcpproxy-tray.exe` - Tray application binary
- `unins000.exe` (Inno Setup) - Uninstaller executable
- `unins000.dat` (Inno Setup) - Uninstaller data

**Lifecycle**:
- Created during installation
- Replaced during upgrades
- Deleted during uninstallation

---

## 9. Validation Rules

### 9.1 Pre-Installation Validation

```
1. Windows Version Check:
   - MUST be ≥ Windows 10 version 21H2 (build 19044)
   - Error: "MCPProxy requires Windows 10 version 21H2 or later."

2. Architecture Compatibility:
   - amd64 installer MUST run on x64-compatible systems
   - arm64 installer MUST run on ARM64 systems
   - Error: "This installer is for {arch} systems only."

3. Disk Space Check:
   - Available space MUST be ≥ 100 MB
   - Error: "Insufficient disk space. 100 MB required."

4. Administrator Privileges:
   - User MUST have admin rights (system PATH modification)
   - Error: "Administrator privileges required."

5. Process Detection:
   - mcpproxy.exe and mcpproxy-tray.exe MUST NOT be running
   - Prompt: "Please close MCPProxy applications to continue."
```

### 9.2 Post-Installation Validation

```
1. File Existence:
   - {InstallDir}\mcpproxy.exe MUST exist
   - {InstallDir}\mcpproxy-tray.exe MUST exist

2. PATH Verification:
   - System PATH MUST contain {InstallDir}
   - Duplicate check: {InstallDir} appears exactly once

3. Shortcut Creation:
   - Start Menu shortcut MUST exist
   - Start Menu shortcut target MUST be valid

4. Registry Entries:
   - Uninstall key MUST exist in HKLM
   - DisplayName, Version, Publisher MUST be populated

5. Executable Test (optional):
   - mcpproxy.exe --version MUST execute successfully
   - Output MUST match installed version
```

---

## 10. Error Handling

### Common Installation Errors

| Error Code | Description | User Action | Installer Action |
|------------|-------------|-------------|------------------|
| **1603** (WiX) | General installation error | Check install.log | Rollback changes |
| **1618** (WiX) | Another installation in progress | Wait and retry | Exit installer |
| **1622** (WiX) | Cannot open log file | Check permissions | Continue without logging |
| **Access Denied** | Insufficient privileges | Run as administrator | Display UAC prompt |
| **Path Too Long** | PATH exceeds 2047 chars | Shorten PATH manually | Skip PATH modification, warn user |
| **Port Conflict** | Port 8080 in use (runtime) | Release port or configure | N/A (runtime issue, not install) |

### Rollback Strategy

**WiX (Transactional)**:
- Automatic rollback on failure (Windows Installer built-in)
- All changes reverted: files, registry, shortcuts, PATH

**Inno Setup (Best Effort)**:
- Manual rollback in `[Code]` section
- Removes installed files and shortcuts
- Restores PATH to pre-install state

---

## 11. Framework-Specific Mappings

### Entity Mapping: Inno Setup → WiX

| Entity | Inno Setup Section | WiX Element |
|--------|-------------------|-------------|
| Package | `[Setup]` | `<Package>` |
| Files | `[Files]` | `<File>` |
| Shortcuts | `[Icons]` | `<Shortcut>` |
| PATH | `[Registry]` + `[Code]` | `<Environment>` |
| Custom Actions | `[Code]` | `<CustomAction>` |
| UI Dialogs | `[Messages]` | `<ui:WixUI>` |
| Uninstall | `[UninstallDelete]` | `<RemoveFile>` |

---

**Data Model Complete**. Ready for contracts generation (WiX .wxs files or Inno Setup .iss script).
