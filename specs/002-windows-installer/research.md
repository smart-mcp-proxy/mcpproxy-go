# Research Report: Windows Installer for MCPProxy

**Date**: 2025-11-13
**Feature**: Windows Installer (002-windows-installer)
**Phase**: Phase 0 - Technology Selection and Best Practices

---

## Executive Summary

This research phase evaluated three Windows installer frameworks (WiX Toolset 4.x, NSIS, Inno Setup) and identified best practices for creating professional MSI installers for the MCPProxy Go application. The research also examined GitHub Actions Windows runner capabilities and RTF screen formatting requirements.

**Key Decisions**:
1. **Installer Framework**: **Inno Setup** (primary recommendation) or **WiX Toolset 4.x** (enterprise-focused alternative)
2. **Multi-Architecture Strategy**: Single installer with architecture detection (Inno Setup) or separate MSI files per architecture (WiX)
3. **GitHub Actions Integration**: .NET global tool installation for WiX, Chocolatey for Inno Setup
4. **RTF Screens**: Convert macOS RTF files using WordPad for simplicity and Windows compatibility

---

## 1. Installer Framework Comparison

### Evaluation Criteria

MCPProxy requires an installer that:
- Bundles two executables (mcpproxy.exe, mcpproxy-tray.exe)
- Supports in-place upgrades (FR-019)
- Modifies system PATH environment variable (FR-003)
- Creates Start Menu shortcuts (FR-004)
- Supports silent installation (FR-018)
- Works on Windows 10 21H2+ and Windows 11 (amd64 and arm64) (FR-008)
- Integrates with GitHub Actions workflows (FR-011)

### Comparison Table

| Criteria | WiX Toolset 4.x | NSIS | Inno Setup |
|----------|----------------|------|------------|
| **Output Format** | MSI (Windows Installer) | EXE | EXE |
| **MSI Support** | ✅ Native | ❌ EXE only | ❌ EXE only |
| **In-Place Upgrades** | ✅ Excellent (MajorUpgrade) | ⚠️ Manual scripting | ✅ Good (built-in) |
| **PATH Modification** | ⚠️ Environment element | ⚠️ EnVar plugin | ✅ Built-in |
| **Silent Installation** | ✅ Native (`/qn`) | ✅ Good (`/S`) | ✅ Good (`/VERYSILENT`) |
| **Multi-Arch (amd64/arm64)** | ⚠️ Separate installers | ⚠️ Separate installers | ✅ **Single installer** |
| **GitHub Actions** | ⚠️ Moderate complexity | ✅ Good (Linux/macOS builds) | ⚠️ Moderate (Windows only) |
| **Code Signing** | ✅ Excellent (signtool) | ✅ Good (jsign) | ✅ Good (SignTool) |
| **Community Support** | ⚠️ Smaller, steep curve | ✅ Large, extensive docs | ✅ Active, excellent docs |
| **Learning Curve** | 🔴 Steep (XML, MSI concepts) | 🟡 Moderate (scripting) | 🟢 **Easy** (INI-like) |
| **Enterprise Deployment** | ✅ **Best** (GPO, SCCM) | ❌ Limited | ❌ Limited |
| **GoReleaser Support** | ✅ Official | ❌ None | ❌ None |

### Decision: Inno Setup (Primary) with WiX as Alternative

**Recommendation**: **Inno Setup**

**Rationale**:
1. **Multi-Architecture Support**: Only framework that supports single installer for amd64/arm64
2. **Ease of Implementation**: Lowest learning curve enables faster development and easier maintenance
3. **PATH Modification**: Built-in support without plugins or workarounds
4. **Proven Track Record**: Used by Visual Studio Code (similar desktop/CLI tool ecosystem)
5. **Silent Installation**: Excellent enterprise support via `/VERYSILENT /SUPPRESSMSGBOXES`

**When to Use WiX Instead**:
- Enterprise customers **require MSI format** for Group Policy deployment
- GoReleaser integration is critical
- Accept: Separate amd64/arm64 installers and steeper learning curve

**NSIS Rejected**: While capable, NSIS offers no significant advantages over Inno Setup for MCPProxy's requirements. Its main benefit (Linux/macOS build capability) is not needed given Windows-only runner constraints for both frameworks.

---

## 2. WiX Toolset 4.x Best Practices (Alternative Path)

### Project Structure

**Recommended File Organization**:
```
wix/
├── Package.wxs              # Main package definition
├── Folders.wxs              # Directory structure
├── Components.wxs           # Component definitions
├── UI.wxs                   # Custom UI (optional)
├── Package.en-us.wxl        # Localization strings
└── installer-resources/
    ├── License.rtf          # License agreement
    ├── banner.bmp           # Top banner (493x58)
    └── dialog.bmp           # Dialog background (493x312)
```

### Multi-Architecture Strategy

**Decision**: Separate MSI files for amd64 and arm64

**Rationale**:
- WiX 4.x requires separate builds per architecture
- Use `-arch` switch: `wix build -arch x64` or `wix build -arch arm64`
- `ProgramFiles6432Folder` auto-resolves to correct directory based on architecture

### Component Organization

**Best Practices**:
1. One Component Per Resource (executable, shortcut, PATH entry)
2. Explicit GUIDs for predictable upgrades
3. ComponentGroups for related resources
4. Clear KeyPath specification

**Example Component**:
```xml
<Component Id="CoreBinary" Guid="12345678-1234-1234-1234-123456789ABC">
  <File Id="CoreExe"
        Source="$(var.BinPath)\mcpproxy.exe"
        KeyPath="yes">
    <Shortcut Id="StartMenuShortcut"
              Name="MCPProxy Server"
              Directory="ProgramMenuFolder"
              WorkingDirectory="INSTALLFOLDER"
              Icon="AppIcon.ico"
              Advertise="no" />
  </File>
</Component>
```

### Upgrade Logic

**MajorUpgrade Element**:
```xml
<MajorUpgrade DowngradeErrorMessage="A newer version is already installed."
              Schedule="afterInstallInitialize"
              AllowSameVersionUpgrades="no" />
```

**Key Points**:
- **UpgradeCode**: Constant GUID across all versions (product family identifier)
- **Product Code**: Auto-generated (wildcard `*`), changes per build
- **Version**: Format `X.Y.Z.0` (4-digit Windows Installer version)

### Custom Actions

**Process Detection** (using WixUtilExtension):
```xml
<util:CloseApplication Id="CloseMCPProxyCore"
                       Target="mcpproxy.exe"
                       CloseMessage="yes"
                       Description="MCPProxy Core Server must be closed."
                       PromptToContinue="yes" />
```

**Post-Install Launch**:
```xml
<Property Id="WIXUI_EXITDIALOGOPTIONALCHECKBOXTEXT"
          Value="Launch MCPProxy System Tray" />

<CustomAction Id="LaunchTrayApplication"
              Directory="INSTALLFOLDER"
              ExeCommand="[INSTALLFOLDER]mcpproxy-tray.exe"
              Execute="immediate"
              Return="asyncNoWait" />
```

### UI Customization

**RTF License Requirements**:
- Encoding: ANSI with `\ansicpg1252`
- Simplicity: Use WordPad (not Microsoft Word) to avoid complex formatting
- Common Issue: Blank screen until scrolling (caused by complex RTF)

**Image Requirements**:
- Banner: 493x58 pixels, BMP or PNG
- Dialog Background: 493x312 pixels, BMP or PNG
- Color Depth: 24-bit RGB recommended

---

## 3. Inno Setup Best Practices (Recommended Path)

### Project Structure

**Single File Approach**:
```
scripts/
├── installer.iss            # Main Inno Setup script
└── installer-resources/
    ├── windows/
    │   ├── welcome.rtf      # Welcome screen
    │   ├── conclusion.rtf   # Completion screen
    │   └── license.txt      # License file
    └── icon.ico             # Application icon
```

### Multi-Architecture Support

**Single Installer for Both Architectures**:
```pascal
[Setup]
ArchitecturesAllowed=x64compatible arm64compatible
ArchitecturesInstallIn64BitMode=x64compatible arm64compatible

[Files]
; x64 binaries
Source: "dist\windows-amd64\mcpproxy.exe"; DestDir: "{app}"; \
  Flags: ignoreversion; Check: IsX64

Source: "dist\windows-amd64\mcpproxy-tray.exe"; DestDir: "{app}"; \
  Flags: ignoreversion; Check: IsX64

; ARM64 binaries
Source: "dist\windows-arm64\mcpproxy.exe"; DestDir: "{app}"; \
  Flags: ignoreversion; Check: IsARM64

Source: "dist\windows-arm64\mcpproxy-tray.exe"; DestDir: "{app}"; \
  Flags: ignoreversion; Check: IsARM64
```

**Architecture Detection Functions**:
```pascal
[Code]
function IsX64: Boolean;
begin
  Result := ProcessorArchitecture = paX64;
end;

function IsARM64: Boolean;
begin
  Result := ProcessorArchitecture = paARM64;
end;
```

### PATH Modification

**Built-in Environment Variable Support**:
```pascal
[Setup]
ChangesEnvironment=yes

[Registry]
Root: HKLM; Subkey: "SYSTEM\CurrentControlSet\Control\Session Manager\Environment"; \
    ValueType: expandsz; ValueName: "Path"; ValueData: "{olddata};{app}"; \
    Check: NeedsAddPath('{app}')

[Code]
function NeedsAddPath(Param: string): boolean;
var
  OrigPath: string;
begin
  if not RegQueryStringValue(HKEY_LOCAL_MACHINE,
    'SYSTEM\CurrentControlSet\Control\Session Manager\Environment',
    'Path', OrigPath)
  then begin
    Result := True;
    exit;
  end;
  Result := Pos(';' + Param + ';', ';' + OrigPath + ';') = 0;
end;
```

### Silent Installation

**Command-Line Flags**:
```bash
# Silent install
installer.exe /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /DIR="C:\MCPProxy"

# Silent uninstall
unins000.exe /VERYSILENT /SUPPRESSMSGBOXES /NORESTART
```

### Upgrade Handling

**In-Place Upgrade Configuration**:
```pascal
[Setup]
AppId={{A1B2C3D4-E5F6-7890-ABCD-EF1234567890}
DefaultDirName={autopf}\MCPProxy
DisableDirPage=no
DisableProgramGroupPage=yes
UninstallDisplayIcon={app}\mcpproxy-tray.exe
```

**Key Points**:
- `AppId` remains constant across versions (like WiX UpgradeCode)
- Inno Setup automatically detects previous installations
- User data in `%USERPROFILE%\.mcpproxy` preserved automatically

---

## 4. GitHub Actions Integration

### Pre-installed Tools (windows-latest)

**Available on Windows Server 2022 (current windows-latest)**:
- .NET SDK: 8.0.x, 9.0.x
- PowerShell: 7.4.x
- Visual Studio 2022: 17.14.x with MSBuild
- Windows SDKs: 10.0.19041, 10.0.22621, 10.0.26100

**Migration Notice**: September 2, 2025 → Windows Server 2025

### WiX Installation Method

**Recommended Approach** (.NET Global Tool):
```yaml
- name: Install WiX Toolset
  run: dotnet tool install --global wix

- name: Build MSI
  run: |
    wix build -arch x64 \
      -d Version=${{ github.ref_name }} \
      -d BinPath=dist/windows-amd64 \
      Package.wxs
```

**Installation Time**: < 10 seconds

### Inno Setup Installation Method

**Chocolatey Package**:
```yaml
- name: Install Inno Setup
  run: choco install innosetup -y

- name: Build Installer
  run: |
    & "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" \
      /DVersion=${{ github.ref_name }} \
      scripts\installer.iss
```

**Installation Time**: ~30 seconds

### Artifact Management

**Best Practices**:
```yaml
- name: Upload Installer
  uses: actions/upload-artifact@v4
  with:
    name: windows-installer-${{ matrix.arch }}
    path: dist/*.exe
    compression-level: 0  # Installer already compressed
    retention-days: 90
```

**Size Limits**:
- GitHub Free: 500 MB storage
- GitHub Pro: 2 GB storage
- Individual file: Practically ~10 GB

**Retention Policies**:
- Release builds: 90 days
- Prerelease builds: 30 days (configurable)

### Performance Optimization

**Caching Strategy**:
```yaml
- name: Cache Go modules
  uses: actions/cache@v4
  with:
    path: |
      ~/.cache/go-build
      ~/go/pkg/mod
    key: ${{ runner.os }}-go-${{ hashFiles('**/go.sum') }}

- name: Cache .NET tools
  uses: actions/cache@v4
  with:
    path: ~/.dotnet/tools
    key: ${{ runner.os }}-dotnet-tools-wix-4.0.0
```

**Expected Build Times**:
- Single architecture: 2-3 minutes
- Multi-architecture: 4-6 minutes
- With caching: 1-2 minutes

---

## 5. RTF Screen Conversion

### macOS to Windows RTF Conversion

**Existing Files**:
- `scripts/installer-resources/welcome_en.rtf` (macOS)
- `scripts/installer-resources/conclusion_en.rtf` (macOS)

**Conversion Strategy**: Manual editing for Windows-specific paths and terminology

**Format Requirements**:
- Encoding: ANSI with `\ansicpg1252`
- Font: Common Windows fonts (Arial, Helvetica)
- Simplicity: Avoid complex RTF from Microsoft Word

**Changes Required**:

1. **Path Conventions**:
   - macOS: `~/.mcpproxy` → Windows: `%USERPROFILE%\.mcpproxy`
   - macOS: `/usr/local/bin` → Windows: `C:\Program Files\MCPProxy`

2. **Command Conventions**:
   - macOS: Terminal → Windows: Command Prompt or PowerShell
   - macOS: `mcpproxy serve` → Windows: `mcpproxy.exe serve` (optional .exe)

3. **System Requirements**:
   - macOS: "macOS 10.15 (Catalina) or later" → Windows: "Windows 10 version 21H2 or Windows 11"
   - macOS: "~50 MB disk space" → Windows: "100 MB disk space"

**Conversion Tool**: WordPad (built-in Windows tool for simplified RTF editing)

---

## 6. Local Testing Workflow

### Developer Testing Setup

**Minimal Requirements**:
- Windows 10 version 21H2+ or Windows 11 VM
- Administrator privileges
- 100 MB free disk space

### Build-Test Cycle

**Local Build Script** (PowerShell):
```powershell
# scripts/build-windows-installer.ps1
param(
    [string]$Version = "1.0.0",
    [string]$Arch = "amd64"
)

# Build Go binaries
$env:GOOS = "windows"
$env:GOARCH = $Arch
$env:CGO_ENABLED = "0"
go build -o "dist\windows-$Arch\mcpproxy.exe" ./cmd/mcpproxy

$env:CGO_ENABLED = "1"
go build -o "dist\windows-$Arch\mcpproxy-tray.exe" ./cmd/mcpproxy-tray

# Build installer (Inno Setup example)
& "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" `
    /DVersion=$Version `
    /DArch=$Arch `
    scripts\installer.iss

Write-Host "Installer created: dist\mcpproxy-setup-$Version-$Arch.exe"
```

### Testing Commands

**Installation**:
```bash
# Silent install
installer.exe /VERYSILENT /SUPPRESSMSGBOXES /LOG="install.log"

# Interactive install
installer.exe
```

**Verification**:
```bash
# Check PATH
$env:Path -split ';' | Select-String "MCPProxy"

# Test CLI
mcpproxy --version

# Test tray (launches GUI)
Start-Process "C:\Program Files\MCPProxy\mcpproxy-tray.exe"
```

**Uninstallation**:
```bash
# Find uninstaller
$uninstaller = Get-ChildItem "C:\Program Files\MCPProxy" -Filter "unins*.exe"

# Silent uninstall
& $uninstaller.FullName /VERYSILENT /SUPPRESSMSGBOXES /LOG="uninstall.log"
```

### Debugging MSI Installation Failures (WiX)

**Enable MSI Logging**:
```bash
msiexec /i mcpproxy.msi /l*v install.log
```

**Common Issues**:
- Exit Code 1603: General installation error (check custom actions)
- Exit Code 1618: Another installation in progress
- Exit Code 1622: Error opening installation log file

---

## 7. Code Signing (Future Enhancement)

### Unsigned Installers (Current State)

**Expected Behavior**:
- Windows Defender SmartScreen warning: "Windows protected your PC"
- User action: Click "More Info" → "Run Anyway"
- Documented in README.md as expected behavior

### Code Signing Options (Future)

**Option 1: Microsoft Partner Network** (Free for qualified developers):
- Application: https://partner.microsoft.com/
- Certificate: Free for MPN members
- SmartScreen Reputation: Builds over time (requires downloads/usage)

**Option 2: EV Code Signing Certificate** (Paid):
- Cost: $300-500/year
- Instant SmartScreen reputation
- USB token required (hardware security module)
- Providers: DigiCert, Sectigo, GlobalSign

**Option 3: SignPath.io** (Free for Open Source):
- Free tier: 3 signing operations/month
- Integrated with GitHub Actions
- Community certificate shared across OSS projects

**Implementation** (when certificates available):

WiX:
```yaml
- name: Sign MSI
  run: |
    & "C:\Program Files (x86)\Windows Kits\10\bin\10.0.22621.0\x64\signtool.exe" `
      sign /f cert.pfx /p ${{ secrets.CERT_PASSWORD }} `
      /tr http://timestamp.digicert.com /td sha256 /fd sha256 `
      mcpproxy.msi
```

Inno Setup:
```pascal
[Setup]
SignTool=signtool /f cert.pfx /p ${{ secrets.CERT_PASSWORD }} /tr http://timestamp.digicert.com /td sha256 /fd sha256 $f
SignedUninstaller=yes
```

---

## 8. Alternatives Considered and Rejected

### GoReleaser

**Pros**:
- Official support for WiX MSI builds
- Automated multi-platform releases
- Well-documented for Go projects

**Cons**:
- Requires WiX 3.x (not 4.x)
- Less control over installer customization
- Learning curve for GoReleaser configuration

**Decision**: Manual installer builds provide more control over UI customization and upgrade logic

### MSIX/AppX (Windows Store)

**Pros**:
- Modern Windows packaging format
- Automatic updates via Microsoft Store
- Sandboxed execution

**Cons**:
- Requires Microsoft Store account ($19 one-time fee)
- Limited enterprise deployment (SCCM/GPO)
- Complexity for system-level PATH modification

**Decision**: Traditional installer (MSI/EXE) provides better enterprise support and simpler PATH configuration

---

## 9. Decision Summary

### Final Technology Selections

| Component | Decision | Rationale |
|-----------|----------|-----------|
| **Primary Framework** | **Inno Setup** | Single multi-arch installer, easy learning curve, Visual Studio Code precedent |
| **Alternative Framework** | WiX Toolset 4.x | MSI format for enterprise deployments (separate installers per arch) |
| **GitHub Actions** | .NET global tool (WiX) or Chocolatey (Inno Setup) | Fast installation, reliable tooling |
| **RTF Screens** | Manual conversion via WordPad | Simple, Windows-compatible formatting |
| **Multi-Arch Strategy** | Single installer (Inno Setup) or separate MSIs (WiX) | User experience vs. enterprise requirements |
| **Code Signing** | Unsigned initially, SignPath.io future | Free OSS option balances cost and reputation |

### Tradeoffs Accepted

1. **EXE vs MSI**: Accepting EXE format (Inno Setup) for ease of use; MSI available via WiX if enterprise requirements emerge
2. **Unsigned Binaries**: SmartScreen warnings acceptable for v1; code signing planned for v2
3. **Manual RTF Conversion**: One-time effort more reliable than automated conversion tools
4. **Windows-Only CI**: Build must run on Windows runners; no Linux/macOS cross-compilation

---

## 10. Implementation Roadmap

### Phase 1: Inno Setup Implementation (Recommended)

1. Create `scripts/installer.iss` with multi-architecture support
2. Convert RTF screens to Windows conventions
3. Integrate into GitHub Actions (release.yml, prerelease.yml)
4. Create local build script for developer testing
5. Test on Windows 10 21H2 and Windows 11 VMs

**Estimated Effort**: 2-3 days

### Phase 2: WiX Alternative (If Needed)

1. Create `wix/Package.wxs` and component definitions
2. Generate banner.bmp and dialog.bmp images
3. Create simplified License.rtf
4. Integrate into GitHub Actions with .NET global tool
5. Test MSI builds on both architectures

**Estimated Effort**: 4-5 days (steeper learning curve)

### Phase 3: Code Signing (Future)

1. Apply to SignPath.io free tier
2. Integrate signing into GitHub Actions
3. Test signed installers on Windows VMs
4. Monitor SmartScreen reputation

**Estimated Effort**: 1-2 days (after certificate acquisition)

---

## References

### Official Documentation

- WiX Toolset 4.x: https://wixtoolset.org/docs/
- Inno Setup: https://jrsoftware.org/isinfo.php
- GitHub Actions Windows Runners: https://github.com/actions/runner-images/blob/main/images/windows/Windows2022-Readme.md

### Code Examples

- kurtanr/WiXInstallerExamples: https://github.com/kurtanr/WiXInstallerExamples
- Visual Studio Code (Inno Setup): https://github.com/microsoft/vscode/tree/main/build/win32

### Tools

- WordPad: Built-in Windows RTF editor
- SignPath.io: https://signpath.io/ (free for OSS)
- Chocolatey: https://chocolatey.org/ (package manager)

---

**Research Phase Complete**. Ready for Phase 1 (Design & Contracts).
