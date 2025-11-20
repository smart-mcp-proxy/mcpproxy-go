# GitHub Actions Windows Runners Research: WiX Toolset MSI Installers

**Research Date**: 2025-11-13
**Target**: Building WiX Toolset 4.x installers for Go applications on GitHub Actions

---

## Table of Contents

1. [Pre-installed Tools on Windows Runners](#1-pre-installed-tools-on-windows-runners)
2. [WiX Toolset Installation Methods](#2-wix-toolset-installation-methods)
3. [Artifact Management Best Practices](#3-artifact-management-best-practices)
4. [Windows-Specific Performance Considerations](#4-windows-specific-performance-considerations)
5. [Real-World Workflow Examples](#5-real-world-workflow-examples)
6. [Go Application + WiX Integration](#6-go-application--wix-integration)

---

## 1. Pre-installed Tools on Windows Runners

### Runner Image: `windows-latest` (Windows Server 2022)

**Important Migration Note**: Starting September 2, 2025, `windows-latest` will migrate from Windows Server 2022 to Windows Server 2025, rolling out progressively until September 30, 2025.

### .NET SDK Versions (Current as of Nov 2025)

**Pre-installed .NET Core SDK versions**:
- 8.0.121, 8.0.206, 8.0.318, 8.0.415
- 9.0.111, 9.0.205, 9.0.306

**Note**: Historical versions included .NET 6.x and 7.x, but these are being phased out. Always check the [official Windows2022-Readme.md](https://github.com/actions/runner-images/blob/main/images/windows/Windows2022-Readme.md) for current versions.

### PowerShell

**PowerShell Version**: 7.4.13

**Pre-installed PowerShell Modules**:
- Az: 12.5.0
- AWSPowershell: 5.0.88
- PowerShellGet: 1.0.0.1, 2.2.5
- PSScriptAnalyzer: 1.24.0

### Visual Studio and Build Tools

**Visual Studio Enterprise 2022**:
- Version: 17.14.36623.8
- Path: `C:\Program Files\Microsoft Visual Studio\2022\Enterprise`

**MSBuild**:
- Component: Microsoft.Component.MSBuild
- Version: 17.14.36510.44
- Included with Visual Studio Enterprise 2022

**Windows SDK Versions**:
- 10.0.19041
- 10.0.22621
- 10.0.26100

### Finding Current Pre-installed Tools

**Method 1: Workflow Logs**
```yaml
- name: Show runner image
  run: |
    echo "Runner Image: $env:ImageVersion"
    Get-ChildItem Env:
```

Check the "Set up job" section in your workflow logs, then expand the "Runner Image" section. The "Included Software" link shows all pre-installed tools.

**Method 2: Official Repository**
- Repository: [actions/runner-images](https://github.com/actions/runner-images)
- Current README: [Windows2022-Readme.md](https://github.com/actions/runner-images/blob/main/images/windows/Windows2022-Readme.md)
- Software tools are updated weekly

---

## 2. WiX Toolset Installation Methods

### Method 1: .NET Global Tool (Recommended for WiX 4.x)

**Installation**:
```yaml
- name: Install WiX Toolset
  run: dotnet tool install --global wix
```

**Requirements**:
- .NET SDK 6.0 or later (pre-installed on `windows-latest`)
- WiX package on nuget.org (automatically resolved)

**Building**:
```yaml
- name: Build MSI
  run: wix build -o output/installer.msi Product.wxs
```

**With Build Variables**:
```yaml
- name: Build MSI with version
  run: wix build Product.wxs -o output/installer-${{ github.ref_name }}.msi -d BuildVersion=${{ github.ref_name }}
```

**Pros**:
- Simple installation (single command)
- Consistent with modern .NET tooling
- Works with WiX 4.x
- Fastest installation method (< 10 seconds)

**Cons**:
- Requires NuGet.org connectivity
- Less familiar to WiX 3.x users

### Method 2: Chocolatey (WiX 3.x Legacy)

**Installation**:
```yaml
- name: Install WiX via Chocolatey
  run: choco install wixtoolset -y
```

**Usage**:
```yaml
- name: Add WiX to PATH
  run: echo "C:\Program Files (x86)\WiX Toolset v3.11\bin" | Out-File -FilePath $env:GITHUB_PATH -Encoding utf8 -Append

- name: Build MSI
  run: |
    candle.exe Product.wxs -out obj/Product.wixobj
    light.exe obj/Product.wixobj -out bin/Installer.msi
```

**Pros**:
- Familiar to WiX 3.x users
- Traditional candle/light workflow

**Cons**:
- WiX 3.x is legacy (not recommended for new projects)
- Longer installation time (30-60 seconds)
- Caching Chocolatey installs is difficult on GitHub Actions
- WiX 3.x is no longer actively maintained

### Method 3: MSBuild Integration

**Project File Setup** (.wixproj):
```xml
<Project Sdk="WixToolset.Sdk/4.0.0">
  <PropertyGroup>
    <OutputName>MyInstaller</OutputName>
    <OutputType>Package</OutputType>
  </PropertyGroup>
  <ItemGroup>
    <Compile Include="Product.wxs" />
  </ItemGroup>
</Project>
```

**Workflow**:
```yaml
- name: Setup MSBuild
  uses: microsoft/setup-msbuild@v1

- name: Install WiX
  run: dotnet tool install --global wix

- name: Build with MSBuild
  run: msbuild MyInstaller.wixproj /p:Configuration=Release
```

**Pros**:
- Integrates with existing MSBuild projects
- Supports complex build configurations
- Good for .NET-heavy projects

**Cons**:
- More setup overhead
- Requires .wixproj file

### Comparison Matrix

| Method | Installation Time | WiX Version | Complexity | Recommended |
|--------|------------------|-------------|------------|-------------|
| .NET Tool | < 10 seconds | 4.x | Low | Yes |
| Chocolatey | 30-60 seconds | 3.x | Medium | No (legacy) |
| MSBuild | 10-20 seconds | 4.x | Medium-High | For .NET projects |

---

## 3. Artifact Management Best Practices

### Artifact Size Limits

**Storage Quotas**:
- **GitHub Free**: 500 MB storage
- **GitHub Pro**: 2 GB storage
- **Private Repos**: 10 GB/month transfer limit
- **Public Repos**: Higher allowances

**Individual Artifact Limits**:
- Maximum per-artifact size: Not explicitly documented, but practical limit is ~10 GB
- GitHub Releases: 2 GB per file (separate from artifact storage)

**Job Limits**:
- Maximum 500 artifacts per job

### Retention Policies

**Default Retention**:
- **Public repositories**: 90 days (adjustable: 1-90 days)
- **Private repositories**: 90 days (adjustable: 1-400 days)

**Custom Retention**:
```yaml
- name: Upload MSI artifact
  uses: actions/upload-artifact@v4
  with:
    name: installer-msi
    path: output/*.msi
    retention-days: 30  # Override default
```

**Important Notes**:
- Retention period only applies to NEW artifacts (not retroactive)
- Artifacts auto-delete after the retention period
- Cannot exceed repository/organization/enterprise limits

### Compression Settings

**Default Compression**:
- Default level: 6 (same as GNU Gzip)
- Algorithm: Zlib

**Custom Compression**:
```yaml
- name: Upload large MSI with minimal compression
  uses: actions/upload-artifact@v4
  with:
    name: installer-msi
    path: output/*.msi
    compression-level: 0  # 0-9 range
```

**Compression Guidelines**:
- **Level 0**: No compression (fastest upload, largest size) - Recommended for pre-compressed files (ZIP, MSI)
- **Level 6**: Default (balanced speed/size)
- **Level 9**: Maximum compression (slowest upload, smallest size)

For MSI files (already compressed), use `compression-level: 0` for significantly faster uploads.

### Naming Conventions

**Best Practices**:
```yaml
# Bad: Generic names
- name: artifact
  path: output/*.msi

# Good: Descriptive names with metadata
- name: mcpproxy-installer-windows-amd64-${{ github.ref_name }}
  path: output/mcpproxy-${{ github.ref_name }}-amd64.msi

# Release vs Prerelease
- name: mcpproxy-installer-windows-amd64-prerelease-${{ github.sha }}
  path: output/mcpproxy-*.msi
  if: github.ref == 'refs/heads/next'
```

**Recommendations**:
- Include architecture: `windows-amd64`, `windows-arm64`
- Include version: `${{ github.ref_name }}` or `${{ github.sha }}`
- Distinguish release types: `prerelease`, `release`, `nightly`
- Use hyphens for readability

### Artifact Immutability (v4)

**Important**: In `actions/upload-artifact@v4`, artifacts are immutable. You cannot overwrite an existing artifact with the same name in the same job.

**Solution**:
```yaml
# Use unique names per build
- name: mcpproxy-installer-windows-amd64-${{ github.run_number }}
```

---

## 4. Windows-Specific Performance Considerations

### Build Time Performance

**Average Times**:
- .NET SDK installation (if not cached): 20-30 seconds
- WiX tool installation: < 10 seconds
- Go build (1 binary): 5-15 seconds
- WiX compile/link: 10-30 seconds
- Total workflow: 2-5 minutes

### Disk Performance Optimization

**Drive Performance Comparison**:
- **C: drive**: Standard performance
- **D: drive**: 20-30% faster for large builds
- **ReFS-based VHDX**: Up to 30X faster IOPS

**Optimization Technique**:
```yaml
- name: Install .NET SDK to D: drive
  run: |
    mkdir D:\dotnet
    $env:DOTNET_INSTALL_DIR = "D:\dotnet"
    # Install .NET or use existing from D:
```

**Real-world Impact**: Some projects report up to 86% faster .NET CI times by using D: drive.

### Caching Strategies

#### 1. Caching .NET NuGet Packages

**Setup**:
```yaml
- name: Setup .NET with caching
  uses: actions/setup-dotnet@v3
  with:
    dotnet-version: 8.0.x
    cache: true  # Enable NuGet caching

- name: Build
  run: dotnet build
```

**Requirements**:
- Enable lock files in project: `<RestorePackagesWithLockFile>true</RestorePackagesWithLockFile>`
- For multi-project repos: `cache-dependency-path: '**/packages.lock.json'`

#### 2. Caching Go Dependencies

```yaml
- name: Setup Go with caching
  uses: actions/setup-go@v4
  with:
    go-version: '1.23'
    cache: true  # Automatically caches go.mod/go.sum
```

#### 3. Caching .NET Global Tools (WiX)

```yaml
- name: Cache .NET tools
  uses: actions/cache@v3
  with:
    path: ~/.dotnet/tools
    key: ${{ runner.os }}-dotnet-tools-${{ hashFiles('**/dotnet-tools.json') }}

- name: Install WiX
  run: dotnet tool install --global wix --version 4.0.0
```

**Note**: Global tools cache is more effective if you specify a fixed version.

#### 4. Caching WiX Intermediate Files

```yaml
- name: Cache WiX obj files
  uses: actions/cache@v3
  with:
    path: |
      installer/obj/
      installer/bin/
    key: ${{ runner.os }}-wix-${{ hashFiles('installer/**/*.wxs') }}
```

### Chocolatey Caching Challenges

**Problem**: Chocolatey installs packages to various locations (C:\tools, C:\Program Files), making comprehensive caching difficult.

**Verdict**: Not recommended for GitHub Actions. Use .NET global tools instead.

### Advanced: VHDX Caching

**Technique**: Install common tools (PowerShell, Node.js, .NET SDK, WiX) to a virtual hard disk (VHDX on X: drive), then cache the entire VHDX file.

**Setup** (Advanced):
```yaml
- name: Restore VHDX cache
  uses: actions/cache@v3
  with:
    path: tools.vhdx
    key: ${{ runner.os }}-vhdx-tools-v1

- name: Mount VHDX
  run: |
    Mount-DiskImage -ImagePath tools.vhdx
    # Tools now available on X: drive
```

**Pros**: Massive performance improvement (30X faster IOPS)
**Cons**: Complex setup, large cache size

---

## 5. Real-World Workflow Examples

### Example 1: Simple WiX 4.x Build (Recommended)

```yaml
name: Build MSI Installer

on:
  push:
    branches: [main, next]
  pull_request:

jobs:
  build-msi:
    runs-on: windows-latest
    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Setup .NET
        uses: actions/setup-dotnet@v3
        with:
          dotnet-version: 8.0.x

      - name: Setup Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.23'
          cache: true

      - name: Build Go binary
        run: |
          $VERSION = "${{ github.ref_name }}"
          go build -o output/mcpproxy.exe ./cmd/mcpproxy

      - name: Install WiX Toolset
        run: dotnet tool install --global wix

      - name: Build MSI
        run: |
          $VERSION = "${{ github.ref_name }}"
          wix build installer/Product.wxs `
            -o output/mcpproxy-$VERSION-amd64.msi `
            -d BuildVersion=$VERSION

      - name: Upload MSI artifact
        uses: actions/upload-artifact@v4
        with:
          name: mcpproxy-installer-windows-amd64-${{ github.ref_name }}
          path: output/*.msi
          compression-level: 0  # MSI already compressed
          retention-days: 30
```

### Example 2: Multi-Architecture Build with Caching

```yaml
name: Build MSI Installers (All Architectures)

on:
  push:
    tags:
      - 'v*'

jobs:
  build-msi:
    runs-on: windows-latest
    strategy:
      matrix:
        arch: [amd64, arm64]
    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Setup .NET with caching
        uses: actions/setup-dotnet@v3
        with:
          dotnet-version: 8.0.x
          cache: true

      - name: Setup Go with caching
        uses: actions/setup-go@v4
        with:
          go-version: '1.23'
          cache: true

      - name: Cache .NET global tools
        uses: actions/cache@v3
        with:
          path: ~/.dotnet/tools
          key: ${{ runner.os }}-dotnet-tools-wix-4.0.0

      - name: Build Go binary for ${{ matrix.arch }}
        run: |
          $env:GOOS = "windows"
          $env:GOARCH = "${{ matrix.arch }}"
          $VERSION = "${{ github.ref_name }}"
          go build -o output/mcpproxy-${{ matrix.arch }}.exe ./cmd/mcpproxy

      - name: Install WiX Toolset
        run: dotnet tool install --global wix --version 4.0.0

      - name: Build MSI for ${{ matrix.arch }}
        run: |
          $VERSION = "${{ github.ref_name }}"
          $ARCH = "${{ matrix.arch }}"
          wix build installer/Product.wxs `
            -o output/mcpproxy-$VERSION-$ARCH.msi `
            -d BuildVersion=$VERSION `
            -d Platform=$ARCH `
            -d BinaryPath=output/mcpproxy-$ARCH.exe

      - name: Upload MSI artifact
        uses: actions/upload-artifact@v4
        with:
          name: mcpproxy-installer-windows-${{ matrix.arch }}-${{ github.ref_name }}
          path: output/mcpproxy-*.msi
          compression-level: 0
          retention-days: 90
```

### Example 3: Prerelease vs Release Build

```yaml
name: Build and Release MSI

on:
  push:
    branches:
      - main      # Release builds
      - next      # Prerelease builds
    tags:
      - 'v*'      # Tagged releases

jobs:
  build-msi:
    runs-on: windows-latest
    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Determine version
        id: version
        run: |
          if ("${{ github.ref }}" -match "^refs/tags/v(.+)$") {
            $VERSION = $matches[1]
            $IS_RELEASE = "true"
          } elseif ("${{ github.ref }}" -eq "refs/heads/next") {
            $COMMIT = "${{ github.sha }}".Substring(0, 7)
            $VERSION = "prerelease-$COMMIT"
            $IS_RELEASE = "false"
          } else {
            $VERSION = "dev-${{ github.sha }}".Substring(0, 7)
            $IS_RELEASE = "false"
          }
          echo "VERSION=$VERSION" >> $env:GITHUB_OUTPUT
          echo "IS_RELEASE=$IS_RELEASE" >> $env:GITHUB_OUTPUT

      - name: Setup .NET
        uses: actions/setup-dotnet@v3
        with:
          dotnet-version: 8.0.x

      - name: Setup Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.23'
          cache: true

      - name: Build Go binary
        run: |
          go build -ldflags "-X main.Version=${{ steps.version.outputs.VERSION }}" `
            -o output/mcpproxy.exe ./cmd/mcpproxy

      - name: Install WiX Toolset
        run: dotnet tool install --global wix

      - name: Build MSI
        run: |
          wix build installer/Product.wxs `
            -o output/mcpproxy-${{ steps.version.outputs.VERSION }}-amd64.msi `
            -d BuildVersion=${{ steps.version.outputs.VERSION }}

      - name: Upload MSI artifact (Prerelease)
        if: steps.version.outputs.IS_RELEASE == 'false'
        uses: actions/upload-artifact@v4
        with:
          name: mcpproxy-installer-windows-amd64-prerelease-${{ github.sha }}
          path: output/*.msi
          compression-level: 0
          retention-days: 30

      - name: Upload MSI artifact (Release)
        if: steps.version.outputs.IS_RELEASE == 'true'
        uses: actions/upload-artifact@v4
        with:
          name: mcpproxy-installer-windows-amd64-${{ steps.version.outputs.VERSION }}
          path: output/*.msi
          compression-level: 0
          retention-days: 90

      - name: Create GitHub Release
        if: steps.version.outputs.IS_RELEASE == 'true'
        uses: softprops/action-gh-release@v1
        with:
          files: output/*.msi
          draft: false
          prerelease: false
```

### Example 4: Legacy WiX 3.x Workflow (For Reference)

```yaml
name: Build MSI (WiX 3.x Legacy)

on:
  push:
    branches: [main]

jobs:
  build-msi:
    runs-on: windows-latest
    steps:
      - name: Checkout code
        uses: actions/checkout@v4

      - name: Setup MSBuild
        uses: microsoft/setup-msbuild@v1

      - name: Setup .NET Core
        uses: actions/setup-dotnet@v3
        with:
          dotnet-version: 8.0.x

      - name: Build application
        run: dotnet build --configuration Release

      - name: Add WiX to PATH
        run: |
          echo "C:\Program Files (x86)\WiX Toolset v3.11\bin" | `
            Out-File -FilePath $env:GITHUB_PATH -Encoding utf8 -Append

      - name: Compile WiX source
        run: candle.exe installer/Product.wxs -out obj/Product.wixobj

      - name: Link MSI
        run: light.exe obj/Product.wixobj -out bin/Installer.msi

      - name: Upload MSI artifact
        uses: actions/upload-artifact@v4
        with:
          name: installer-msi
          path: bin/Installer.msi
          retention-days: 30
```

---

## 6. Go Application + WiX Integration

### WXS File Structure for Go Binaries

**Basic Product.wxs Example**:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<Wix xmlns="http://wixtoolset.org/schemas/v4/wxs">
  <Package
    Name="MCPProxy"
    Version="$(var.BuildVersion)"
    Manufacturer="MCPProxy Contributors"
    UpgradeCode="YOUR-GUID-HERE">

    <MajorUpgrade
      DowngradeErrorMessage="A newer version is already installed." />

    <Media Id="1" Cabinet="mcpproxy.cab" EmbedCab="yes" />

    <!-- Directory structure -->
    <StandardDirectory Id="ProgramFiles64Folder">
      <Directory Id="INSTALLFOLDER" Name="MCPProxy">
        <Component Id="MainExecutable" Guid="YOUR-COMPONENT-GUID">
          <File
            Id="mcpproxy.exe"
            Source="output/mcpproxy.exe"
            KeyPath="yes" />
        </Component>

        <Component Id="TrayExecutable" Guid="YOUR-TRAY-COMPONENT-GUID">
          <File
            Id="mcpproxy_tray.exe"
            Source="output/mcpproxy-tray.exe" />
        </Component>
      </Directory>
    </StandardDirectory>

    <!-- Feature definition -->
    <Feature Id="Complete" Level="1">
      <ComponentRef Id="MainExecutable" />
      <ComponentRef Id="TrayExecutable" />
    </Feature>
  </Package>
</Wix>
```

### Advanced: Path Environment Variable

```xml
<Component Id="MainExecutable" Guid="YOUR-COMPONENT-GUID">
  <File
    Id="mcpproxy.exe"
    Source="output/mcpproxy.exe"
    KeyPath="yes" />

  <!-- Add to PATH -->
  <Environment
    Id="PATH"
    Name="PATH"
    Value="[INSTALLFOLDER]"
    Permanent="no"
    Part="last"
    Action="set"
    System="yes" />
</Component>
```

### Advanced: Windows Service Installation

```xml
<Component Id="ServiceComponent" Guid="YOUR-SERVICE-GUID">
  <File
    Id="mcpproxy_service.exe"
    Source="output/mcpproxy.exe"
    KeyPath="yes" />

  <ServiceInstall
    Id="MCPProxyService"
    Name="MCPProxyService"
    DisplayName="MCPProxy Service"
    Description="MCPProxy background service"
    Type="ownProcess"
    Start="auto"
    ErrorControl="normal" />

  <ServiceControl
    Id="StartService"
    Name="MCPProxyService"
    Start="install"
    Stop="both"
    Remove="uninstall" />
</Component>
```

### Advanced: Desktop Shortcut

```xml
<StandardDirectory Id="DesktopFolder">
  <Component Id="DesktopShortcut" Guid="YOUR-SHORTCUT-GUID">
    <Shortcut
      Id="DesktopShortcut"
      Name="MCPProxy"
      Target="[INSTALLFOLDER]mcpproxy-tray.exe"
      WorkingDirectory="INSTALLFOLDER"
      Icon="mcpproxy.ico" />
    <RegistryValue
      Root="HKCU"
      Key="Software\MCPProxy"
      Name="installed"
      Type="integer"
      Value="1"
      KeyPath="yes" />
  </Component>
</StandardDirectory>

<!-- Icon definition -->
<Icon Id="mcpproxy.ico" SourceFile="assets/icon.ico" />
```

### Multi-Architecture Support

**Product.wxs with Platform Variable**:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<Wix xmlns="http://wixtoolset.org/schemas/v4/wxs">
  <Package
    Name="MCPProxy ($(var.Platform))"
    Version="$(var.BuildVersion)"
    Manufacturer="MCPProxy Contributors"
    UpgradeCode="YOUR-GUID-HERE">

    <!-- Platform-specific properties -->
    <?if $(var.Platform) = "x64" ?>
      <?define PlatformProgramFilesFolder = "ProgramFiles64Folder" ?>
    <?elseif $(var.Platform) = "arm64" ?>
      <?define PlatformProgramFilesFolder = "ProgramFiles64Folder" ?>
    <?else ?>
      <?define PlatformProgramFilesFolder = "ProgramFilesFolder" ?>
    <?endif ?>

    <StandardDirectory Id="$(var.PlatformProgramFilesFolder)">
      <Directory Id="INSTALLFOLDER" Name="MCPProxy">
        <Component Id="MainExecutable" Guid="YOUR-COMPONENT-GUID">
          <File
            Id="mcpproxy.exe"
            Source="$(var.BinaryPath)"
            KeyPath="yes" />
        </Component>
      </Directory>
    </StandardDirectory>

    <Feature Id="Complete" Level="1">
      <ComponentRef Id="MainExecutable" />
    </Feature>
  </Package>
</Wix>
```

**Build Command**:
```powershell
wix build Product.wxs `
  -o output/mcpproxy-$VERSION-amd64.msi `
  -d BuildVersion=$VERSION `
  -d Platform=x64 `
  -d BinaryPath=output/mcpproxy-amd64.exe
```

### GoReleaser Integration

**Alternative Approach**: Use [GoReleaser](https://goreleaser.com/customization/msi/) with WiX support.

**.goreleaser.yml**:
```yaml
builds:
  - id: mcpproxy
    main: ./cmd/mcpproxy
    binary: mcpproxy
    goos:
      - windows
    goarch:
      - amd64
      - arm64

msi:
  - id: mcpproxy-msi
    builds:
      - mcpproxy
    name: "MCPProxy-{{ .Version }}-{{ .Arch }}.msi"
    wxs: installer/Product.wxs
    extra_files:
      - assets/icon.ico
```

**GitHub Actions with GoReleaser**:
```yaml
- name: Run GoReleaser
  uses: goreleaser/goreleaser-action@v5
  with:
    version: latest
    args: release --clean
  env:
    GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

---

## Key Takeaways

### Recommended Approach for MCPProxy

1. **Use WiX Toolset 4.x** with .NET global tool installation
2. **Cache Go dependencies** with `actions/setup-go@v4`
3. **Use compression-level: 0** for MSI artifacts (already compressed)
4. **Separate prerelease and release artifacts** with descriptive names
5. **Multi-architecture support** with build matrix (amd64, arm64)
6. **Retention: 30 days for prerelease, 90 days for release**

### Performance Optimization Checklist

- [ ] Enable Go dependency caching
- [ ] Enable .NET NuGet caching (if applicable)
- [ ] Cache .NET global tools with fixed version
- [ ] Use `compression-level: 0` for MSI uploads
- [ ] Consider D: drive for large builds (optional)

### Build Time Targets

- **Simple build** (single arch): 2-3 minutes
- **Multi-arch build** (amd64 + arm64): 4-6 minutes
- **With full caching**: 1-2 minutes

---

## References

- [GitHub Actions Runner Images](https://github.com/actions/runner-images)
- [Windows 2022 Runner README](https://github.com/actions/runner-images/blob/main/images/windows/Windows2022-Readme.md)
- [WiX Toolset Documentation](https://docs.firegiant.com/wix/)
- [GitHub Actions Artifact Storage](https://docs.github.com/actions/using-workflows/storing-workflow-data-as-artifacts)
- [GoReleaser MSI Support](https://goreleaser.com/customization/msi/)
- [Windows Performance Optimization Blog](https://chadgolden.com/blog/github-actions-hosted-windows-runners-slower-than-expected-ci-and-you)
