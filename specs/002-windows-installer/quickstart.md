# Quickstart: Building and Testing Windows Installers

**Feature**: 002-windows-installer
**Audience**: Developers working on MCPProxy Windows installers
**Date**: 2025-11-13

---

## Overview

This guide provides step-by-step instructions for building, testing, and debugging Windows installers for MCPProxy on your local development machine and in CI/CD pipelines.

**Prerequisites**:
- Windows 10 version 21H2+ or Windows 11 (VM acceptable)
- Administrator privileges
- Go 1.25+ installed
- Git installed

---

## Part 1: Local Development Setup

### Step 1: Install Build Tools

**Option A: Inno Setup (Recommended)**

```powershell
# Install via Chocolatey
choco install innosetup -y

# Verify installation
& "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" /?
```

**Option B: WiX Toolset 4.x**

```powershell
# Install .NET SDK (if not already installed)
winget install Microsoft.DotNet.SDK.8

# Install WiX as global tool
dotnet tool install --global wix

# Verify installation
wix --version
```

### Step 2: Build Go Binaries

```powershell
# Navigate to repository root
cd C:\path\to\mcpproxy-go

# Build amd64 binaries
$env:GOOS = "windows"
$env:GOARCH = "amd64"

# Core binary (CGO disabled for portability)
$env:CGO_ENABLED = "0"
go build -ldflags "-s -w -X main.version=1.0.0-dev" `
    -o dist\windows-amd64\mcpproxy.exe `
    ./cmd/mcpproxy

# Tray binary (CGO enabled for GUI)
$env:CGO_ENABLED = "1"
go build -ldflags "-s -w -X main.version=1.0.0-dev" `
    -o dist\windows-amd64\mcpproxy-tray.exe `
    ./cmd/mcpproxy-tray

Write-Host "✅ Binaries built successfully"
ls dist\windows-amd64\
```

**For ARM64** (cross-compilation):
```powershell
$env:GOARCH = "arm64"
# Repeat build commands with output to dist\windows-arm64\
```

---

## Part 2: Building Installers Locally

### Option A: Inno Setup Installer

**Step 1: Create Installer Script** (already provided as `scripts/installer.iss`)

**Step 2: Build Installer**

```powershell
# Build for amd64
& "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" `
    /DVersion=1.0.0-dev `
    /DArch=amd64 `
    /DBinPath=dist\windows-amd64 `
    scripts\installer.iss

# Output: dist\mcpproxy-setup-1.0.0-dev-amd64.exe

Write-Host "✅ Installer created: dist\mcpproxy-setup-1.0.0-dev-amd64.exe"
```

**Expected Output**:
```
Inno Setup 6.x Compiler
Processing: scripts\installer.iss
Compiling [Setup] section
Compiling [Files] section
Creating setup.exe
Successful compile (X.X sec).
```

### Option B: WiX Toolset Installer

**Step 1: Create WiX Project** (already provided in `wix/Package.wxs`)

**Step 2: Build MSI**

```powershell
# Build for amd64
wix build -arch x64 `
    -d Version=1.0.0.0 `
    -d BinPath=dist\windows-amd64 `
    -ext WixToolset.Util.wixext `
    -ext WixToolset.UI.wixext `
    -o dist\mcpproxy-1.0.0-dev-windows-amd64.msi `
    wix\Package.wxs

Write-Host "✅ MSI created: dist\mcpproxy-1.0.0-dev-windows-amd64.msi"
```

**Expected Output**:
```
Windows Installer XML Toolset Build version 4.x
Building: wix\Package.wxs
Creating cabinet files
Linking: mcpproxy-1.0.0-dev-windows-amd64.msi
Build succeeded (X.X sec).
```

---

## Part 3: Testing Installers

### Test Scenario 1: Fresh Installation

**Step 1: Install**

```powershell
# Option A: Interactive installation (double-click installer.exe or installer.msi)
Start-Process dist\mcpproxy-setup-1.0.0-dev-amd64.exe

# Option B: Silent installation
Start-Process dist\mcpproxy-setup-1.0.0-dev-amd64.exe `
    -ArgumentList "/VERYSILENT", "/SUPPRESSMSGBOXES", "/LOG=install.log" `
    -Wait

# For MSI:
Start-Process msiexec.exe `
    -ArgumentList "/i", "dist\mcpproxy-1.0.0-dev-windows-amd64.msi", "/qn", "/l*v", "install.log" `
    -Wait
```

**Step 2: Verify Installation**

```powershell
# Check files exist
Test-Path "C:\Program Files\MCPProxy\mcpproxy.exe"  # Should return True
Test-Path "C:\Program Files\MCPProxy\mcpproxy-tray.exe"  # Should return True

# Check PATH configuration
$env:Path -split ';' | Select-String "MCPProxy"
# Should output: C:\Program Files\MCPProxy

# Test CLI (requires new PowerShell session or refreshenv)
# Open NEW PowerShell window:
mcpproxy --version
# Should output: MCPProxy version 1.0.0-dev

# Check Start Menu shortcut
Test-Path "$env:ProgramData\Microsoft\Windows\Start Menu\Programs\MCPProxy\MCPProxy.lnk"

# Check uninstall registry
Get-ItemProperty "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*" | `
    Where-Object DisplayName -like "MCPProxy*" | `
    Select-Object DisplayName, DisplayVersion, InstallLocation
```

**Expected Output**:
```
DisplayName      : MCPProxy
DisplayVersion   : 1.0.0-dev
InstallLocation  : C:\Program Files\MCPProxy
```

### Test Scenario 2: Upgrade Installation

**Step 1: Install v1.0.0**
```powershell
# Install first version
Start-Process dist\mcpproxy-setup-1.0.0-windows-amd64.exe -Wait
```

**Step 2: Create User Data**
```powershell
# Simulate user configuration
New-Item -ItemType Directory -Path "$env:USERPROFILE\.mcpproxy" -Force
Set-Content "$env:USERPROFILE\.mcpproxy\test-config.json" `
    '{"test": "data to preserve"}'
```

**Step 3: Install v1.1.0**
```powershell
# Build v1.1.0 installer (update version in build command)
# ... build steps ...

# Install upgrade
Start-Process dist\mcpproxy-setup-1.1.0-windows-amd64.exe -Wait
```

**Step 4: Verify Upgrade**
```powershell
# Check new version
mcpproxy --version
# Should output: MCPProxy version 1.1.0

# Verify user data preserved
Get-Content "$env:USERPROFILE\.mcpproxy\test-config.json"
# Should output: {"test": "data to preserve"}

# Check no duplicate PATH entries
($env:Path -split ';' | Select-String "MCPProxy").Count
# Should output: 1 (exactly one entry)
```

### Test Scenario 3: Uninstallation

**Step 1: Uninstall**

```powershell
# Option A: Via Add/Remove Programs GUI
control appwiz.cpl
# Find "MCPProxy" → Click "Uninstall"

# Option B: Silent uninstall (Inno Setup)
$uninstaller = Get-ChildItem "C:\Program Files\MCPProxy" -Filter "unins*.exe"
Start-Process $uninstaller.FullName `
    -ArgumentList "/VERYSILENT", "/SUPPRESSMSGBOXES" `
    -Wait

# Option C: Silent uninstall (WiX MSI)
$productCode = (Get-ItemProperty "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*" | `
    Where-Object DisplayName -eq "MCPProxy").PSChildName
Start-Process msiexec.exe `
    -ArgumentList "/x", $productCode, "/qn", "/l*v", "uninstall.log" `
    -Wait
```

**Step 2: Verify Clean Removal**

```powershell
# Check files removed
Test-Path "C:\Program Files\MCPProxy"  # Should return False

# Check PATH cleaned
$env:Path -split ';' | Select-String "MCPProxy"
# Should output: (empty)

# Check Start Menu shortcut removed
Test-Path "$env:ProgramData\Microsoft\Windows\Start Menu\Programs\MCPProxy"
# Should return: False

# Verify user data preserved
Test-Path "$env:USERPROFILE\.mcpproxy\test-config.json"
# Should return: True (user data NOT deleted)

# Check uninstall registry removed
Get-ItemProperty "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*" | `
    Where-Object DisplayName -like "MCPProxy*"
# Should output: (empty)
```

---

## Part 4: Debugging Installation Issues

### Enable MSI Logging (WiX)

```powershell
# Install with verbose logging
msiexec /i mcpproxy.msi /l*v install.log

# Common log entries to search for:
Select-String -Path install.log -Pattern "Return value 3"  # Error
Select-String -Path install.log -Pattern "CustomAction"    # Custom action execution
Select-String -Path install.log -Pattern "Error"           # General errors
```

### Enable Inno Setup Debugging

```powershell
# Install with log file
installer.exe /LOG=install.log

# View log
Get-Content install.log | Select-String "Error"
Get-Content install.log | Select-String "Failed"
```

### Common Issues and Solutions

| Issue | Symptoms | Solution |
|-------|----------|----------|
| **PATH not updated** | `mcpproxy` command not found | 1. Check admin privileges<br>2. Restart PowerShell session<br>3. Manually verify: `$env:Path -split ';'` |
| **"Another installer running"** | Error 1618 (MSI) | Wait for other installer to finish, reboot if needed |
| **"Access Denied"** | Installation fails | Run installer as Administrator (right-click → Run as administrator) |
| **Shortcut missing icon** | Blank icon in Start Menu | Verify mcpproxy-tray.exe contains embedded icon resource |
| **Process detection fails** | "App is running" error | Use Task Manager to force-close mcpproxy.exe and mcpproxy-tray.exe |

---

## Part 5: CI/CD Integration (GitHub Actions)

### Trigger Prerelease Build

```bash
# Commit changes to feature branch
git add .
git commit -m "feat: update Windows installer"

# Push to 'next' branch (triggers prerelease workflow)
git push origin 002-windows-installer:next
```

### Download Prerelease Artifacts

```bash
# Option 1: GitHub Web UI
# 1. Go to Actions tab
# 2. Click latest "Prerelease" workflow run
# 3. Scroll to "Artifacts" section
# 4. Download "windows-installer-amd64" or "windows-installer-arm64"

# Option 2: GitHub CLI
gh run list --workflow=prerelease --limit=1
gh run download <run-id> --name windows-installer-amd64
```

### Test Prerelease Installer

```powershell
# Unzip artifact
Expand-Archive windows-installer-amd64.zip -Destination .

# Install
Start-Process mcpproxy-setup-vX.Y.Z-next.abcdef-amd64.exe

# Verify prerelease version
mcpproxy --version
# Should output: MCPProxy version vX.Y.Z-next.abcdef
```

---

## Part 6: Release Workflow

### Create Production Release

```bash
# Ensure you're on main branch
git checkout main
git pull origin main

# Create version tag
git tag v1.0.0 -m "Release version 1.0.0"
git push origin v1.0.0

# GitHub Actions will automatically:
# 1. Build Windows installers (amd64 + arm64)
# 2. Upload to GitHub Releases
# 3. Create release notes
```

### Download Release Artifacts

```bash
# Option 1: GitHub Releases Page
# https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest

# Option 2: GitHub CLI
gh release download v1.0.0 --pattern "*.exe"
gh release download v1.0.0 --pattern "*.msi"
```

---

## Part 7: Advanced Topics

### Code Signing (Future)

**Once code signing certificate acquired:**

```powershell
# Sign installer (Inno Setup)
& "C:\Program Files (x86)\Windows Kits\10\bin\10.0.22621.0\x64\signtool.exe" `
    sign `
    /f certificate.pfx `
    /p $env:CERT_PASSWORD `
    /tr http://timestamp.digicert.com `
    /td sha256 `
    /fd sha256 `
    dist\mcpproxy-setup.exe

# Verify signature
signtool verify /pa /v dist\mcpproxy-setup.exe
```

### Multi-Architecture Build Script

**Batch build both architectures:**

```powershell
# scripts\build-all-installers.ps1
param(
    [string]$Version = "1.0.0-dev"
)

$architectures = @("amd64", "arm64")

foreach ($arch in $architectures) {
    Write-Host "Building for $arch..."

    # Build Go binaries
    $env:GOOS = "windows"
    $env:GOARCH = $arch

    $env:CGO_ENABLED = "0"
    go build -ldflags "-s -w -X main.version=$Version" `
        -o "dist\windows-$arch\mcpproxy.exe" `
        ./cmd/mcpproxy

    $env:CGO_ENABLED = "1"
    go build -ldflags "-s -w -X main.version=$Version" `
        -o "dist\windows-$arch\mcpproxy-tray.exe" `
        ./cmd/mcpproxy-tray

    # Build installer (Inno Setup example)
    & "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" `
        /DVersion=$Version `
        /DArch=$arch `
        /DBinPath="dist\windows-$arch" `
        scripts\installer.iss

    Write-Host "✅ $arch installer created"
}

Write-Host "`n✅ All installers built successfully"
ls dist\*.exe
```

**Usage**:
```powershell
.\scripts\build-all-installers.ps1 -Version "1.2.0"
```

---

## Part 8: Troubleshooting Checklist

### Pre-Installation Checks

- [ ] Go binaries built successfully (`mcpproxy.exe`, `mcpproxy-tray.exe` exist)
- [ ] Binaries are correct architecture (x64 or ARM64)
- [ ] Version information embedded in binaries (`mcpproxy.exe --version` works)
- [ ] RTF files formatted correctly (no complex Word formatting)
- [ ] Installer build tool installed (Inno Setup or WiX)
- [ ] Administrator privileges available

### Post-Installation Checks

- [ ] Files installed to `C:\Program Files\MCPProxy`
- [ ] PATH contains `C:\Program Files\MCPProxy`
- [ ] Start Menu shortcut exists and works
- [ ] `mcpproxy --version` command works in new terminal
- [ ] Tray application launches from Start Menu
- [ ] Uninstall entry visible in "Add or Remove Programs"

### CI/CD Checks

- [ ] Workflow triggered (check Actions tab)
- [ ] Windows runner selected (`windows-latest`)
- [ ] Build tools installed (dotnet, wix, or choco, innosetup)
- [ ] Artifacts uploaded successfully
- [ ] Artifact naming follows convention (`mcpproxy-{version}-windows-{arch}-installer.{ext}`)

---

## Quick Reference

### File Locations

| Component | Path |
|-----------|------|
| **Installation Directory** | `C:\Program Files\MCPProxy` |
| **User Data** | `%USERPROFILE%\.mcpproxy` |
| **Start Menu Shortcut** | `%ProgramData%\Microsoft\Windows\Start Menu\Programs\MCPProxy` |
| **Uninstall Registry** | `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{ProductCode}` |

### Key Commands

| Action | Command |
|--------|---------|
| **Build Go binaries** | `go build -o dist\windows-amd64\mcpproxy.exe ./cmd/mcpproxy` |
| **Build Inno Setup installer** | `ISCC.exe /DVersion=1.0.0 scripts\installer.iss` |
| **Build WiX MSI** | `wix build -arch x64 -d Version=1.0.0.0 wix\Package.wxs` |
| **Silent install (Inno)** | `installer.exe /VERYSILENT /SUPPRESSMSGBOXES` |
| **Silent install (MSI)** | `msiexec /i installer.msi /qn /l*v install.log` |
| **Check PATH** | `$env:Path -split ';' \| Select-String "MCPProxy"` |
| **Verify install** | `mcpproxy --version` |

---

## Next Steps

1. ✅ Complete this quickstart guide
2. ⏭️ Implement installer scripts (`installer.iss` or `Package.wxs`)
3. ⏭️ Test locally on Windows VM
4. ⏭️ Integrate into GitHub Actions workflows
5. ⏭️ Run `/speckit.tasks` to generate task breakdown

**Questions?** Refer to [research.md](./research.md) for detailed framework comparisons and best practices.
