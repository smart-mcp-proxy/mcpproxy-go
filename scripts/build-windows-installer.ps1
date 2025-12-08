# MCPProxy Windows Installer Build Script
# This script builds Go binaries and generates Windows installers using Inno Setup or WiX Toolset

param(
    [Parameter(Mandatory=$true)]
    [string]$Version,

    [Parameter(Mandatory=$true)]
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch,

    [Parameter(Mandatory=$false)]
    [ValidateSet('inno', 'wix', 'both')]
    [string]$InstallerType = 'inno'
)

$ErrorActionPreference = "Stop"

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "MCPProxy Windows Installer Builder" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Version: $Version" -ForegroundColor Yellow
Write-Host "Architecture: $Arch" -ForegroundColor Yellow
Write-Host "Installer Type: $InstallerType" -ForegroundColor Yellow
Write-Host ""

# Determine repository root (script is in scripts/)
$RepoRoot = Split-Path -Parent $PSScriptRoot
$DistDir = Join-Path $RepoRoot "dist"
$BinDir = Join-Path $DistDir "windows-$Arch"

# Ensure directories exist
if (-not (Test-Path $DistDir)) {
    New-Item -ItemType Directory -Path $DistDir -Force | Out-Null
}
if (-not (Test-Path $BinDir)) {
    New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
}

# Step 1: Build Go binaries
Write-Host "[1/3] Building Go binaries for windows/$Arch..." -ForegroundColor Green

$env:GOOS = "windows"
$env:GOARCH = $Arch

# Build core binary (CGO disabled for portability)
Write-Host "  Building mcpproxy.exe..." -ForegroundColor White
$env:CGO_ENABLED = "0"
$CoreBinary = Join-Path $BinDir "mcpproxy.exe"
$CoreCmd = Join-Path $RepoRoot "cmd/mcpproxy"
go build -buildvcs=false -ldflags "-s -w -X main.version=$Version" -o $CoreBinary $CoreCmd

if ($LASTEXITCODE -ne 0) {
    Write-Host "  Failed to build mcpproxy.exe" -ForegroundColor Red
    exit 1
}
Write-Host "  mcpproxy.exe built successfully" -ForegroundColor Green

# Build tray binary (CGO enabled for GUI on Windows)
Write-Host "  Building mcpproxy-tray.exe..." -ForegroundColor White
$env:CGO_ENABLED = "1"
$TrayBinary = Join-Path $BinDir "mcpproxy-tray.exe"
$TrayCmd = Join-Path $RepoRoot "cmd/mcpproxy-tray"
go build -buildvcs=false -ldflags "-s -w -X main.version=$Version -H windowsgui" -o $TrayBinary $TrayCmd

if ($LASTEXITCODE -ne 0) {
    Write-Host "  Failed to build mcpproxy-tray.exe" -ForegroundColor Red
    exit 1
}
Write-Host "  mcpproxy-tray.exe built successfully" -ForegroundColor Green

Write-Host ""
Write-Host "[2/3] Verifying binaries..." -ForegroundColor Green

if (-not (Test-Path $CoreBinary)) {
    Write-Host "  mcpproxy.exe not found at $CoreBinary" -ForegroundColor Red
    exit 1
}
if (-not (Test-Path $TrayBinary)) {
    Write-Host "  mcpproxy-tray.exe not found at $TrayBinary" -ForegroundColor Red
    exit 1
}

$CoreSize = (Get-Item $CoreBinary).Length / 1MB
$TraySize = (Get-Item $TrayBinary).Length / 1MB
Write-Host "  mcpproxy.exe: $([math]::Round($CoreSize, 2)) MB" -ForegroundColor Green
Write-Host "  mcpproxy-tray.exe: $([math]::Round($TraySize, 2)) MB" -ForegroundColor Green

# Step 3: Generate installers
Write-Host ""
Write-Host "[3/3] Generating installers..." -ForegroundColor Green

$InstallerBuilt = $false

# Inno Setup installer
if ($InstallerType -eq 'inno' -or $InstallerType -eq 'both') {
    Write-Host "  Building Inno Setup installer..." -ForegroundColor White

    $ISCC = "C:\Program Files (x86)\Inno Setup 6\ISCC.exe"
    if (-not (Test-Path $ISCC)) {
        Write-Host "    Inno Setup not found at $ISCC" -ForegroundColor Yellow
        Write-Host "    Install via: choco install innosetup -y" -ForegroundColor Yellow
    } else {
        $InnoScript = Join-Path $RepoRoot "scripts\installer.iss"
        $RelativeBinPath = "..\dist\windows-$Arch"

        # Check for release notes file (downloaded from workflow artifact)
        $ReleaseNotesFile = Get-ChildItem -Path $RepoRoot -Filter "RELEASE_NOTES-*.md" -ErrorAction SilentlyContinue | Select-Object -First 1
        if (-not $ReleaseNotesFile) {
            $ReleaseNotesFile = Get-ChildItem -Path $RepoRoot -Filter "RELEASE_NOTES.md" -ErrorAction SilentlyContinue | Select-Object -First 1
        }

        if ($ReleaseNotesFile) {
            Write-Host "    Including release notes: $($ReleaseNotesFile.Name)" -ForegroundColor Cyan
            $ReleaseNotesPath = $ReleaseNotesFile.FullName
            & $ISCC /DVersion=$Version /DArch=$Arch "/DBinPath=$RelativeBinPath" "/DReleaseNotesPath=$ReleaseNotesPath" $InnoScript
        } else {
            Write-Host "    No release notes file found, building without" -ForegroundColor Yellow
            & $ISCC /DVersion=$Version /DArch=$Arch "/DBinPath=$RelativeBinPath" $InnoScript
        }

        if ($LASTEXITCODE -eq 0) {
            $InnoOutput = Join-Path $DistDir "mcpproxy-setup-$Version-$Arch.exe"
            Write-Host "    Inno Setup installer: $InnoOutput" -ForegroundColor Green
            $InstallerBuilt = $true
        } else {
            Write-Host "    Inno Setup build failed" -ForegroundColor Red
        }
    }
}

# WiX Toolset installer
if ($InstallerType -eq 'wix' -or $InstallerType -eq 'both') {
    Write-Host "  Building WiX MSI installer..." -ForegroundColor White

    # Check if wix is available
    $WixAvailable = Get-Command "wix" -ErrorAction SilentlyContinue
    if (-not $WixAvailable) {
        Write-Host "    WiX Toolset not found" -ForegroundColor Yellow
        Write-Host "    Install via: dotnet tool install --global wix" -ForegroundColor Yellow
    } else {
        $WixArch = if ($Arch -eq 'amd64') { 'x64' } else { 'arm64' }
        $WixSource = if ($Arch -eq 'amd64') {
            Join-Path $RepoRoot "wix\Package.wxs"
        } else {
            Join-Path $RepoRoot "wix\Package-arm64.wxs"
        }
        $WixOutput = Join-Path $DistDir "mcpproxy-$Version-windows-$Arch.msi"

        wix build -arch $WixArch -d "BinPath=$BinDir" -ext WixToolset.Util.wixext -ext WixToolset.UI.wixext -o $WixOutput $WixSource

        if ($LASTEXITCODE -eq 0) {
            Write-Host "    WiX MSI installer: $WixOutput" -ForegroundColor Green
            $InstallerBuilt = $true
        } else {
            Write-Host "    WiX build failed" -ForegroundColor Red
        }
    }
}

# Final status
Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
if ($InstallerBuilt) {
    Write-Host "Build completed successfully!" -ForegroundColor Green
    Write-Host ""
    Write-Host "Installer location: $DistDir" -ForegroundColor Yellow
    Write-Host ""
    Write-Host "To test installation:" -ForegroundColor White
    Write-Host "  Silent: .\dist\mcpproxy-setup-$Version-$Arch.exe /VERYSILENT" -ForegroundColor Gray
    Write-Host "  Interactive: .\dist\mcpproxy-setup-$Version-$Arch.exe" -ForegroundColor Gray
} else {
    Write-Host "No installers were built" -ForegroundColor Red
    Write-Host "Please install Inno Setup or WiX Toolset" -ForegroundColor Yellow
    exit 1
}
Write-Host "========================================" -ForegroundColor Cyan
