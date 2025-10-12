#!/usr/bin/env pwsh
# PowerShell build script for mcpproxy

param(
    [string]$Version = "",
    [switch]$BuildTray,
    [switch]$OnlyCurrentPlatform
)

# Enable strict mode
$ErrorActionPreference = "Stop"

# Get version from git tag, or use default
if ([string]::IsNullOrEmpty($Version)) {
    try {
        $Version = git describe --tags --abbrev=0 2>$null
        if ([string]::IsNullOrEmpty($Version)) {
            $Version = "v0.1.0-dev"
        }
    }
    catch {
        $Version = "v0.1.0-dev"
    }
}

# Get commit hash
try {
    $Commit = git rev-parse --short HEAD 2>$null
    if ([string]::IsNullOrEmpty($Commit)) {
        $Commit = "unknown"
    }
}
catch {
    $Commit = "unknown"
}

# Get current date in UTC
$Date = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

Write-Host "Building mcpproxy version: $Version" -ForegroundColor Green
Write-Host "Commit: $Commit" -ForegroundColor Green
Write-Host "Date: $Date" -ForegroundColor Green
Write-Host ""

$LDFLAGS = "-X main.version=$Version -X main.commit=$Commit -X main.date=$Date -s -w"

# Array to track built binaries
$BuiltBinaries = @()

# Build for current platform (with CGO for tray support if needed)
Write-Host "Building for current platform..." -ForegroundColor Cyan
go build -ldflags $LDFLAGS -o mcpproxy.exe ./cmd/mcpproxy
if ($LASTEXITCODE -ne 0) {
    Write-Error "Failed to build for current platform"
    exit $LASTEXITCODE
}
$BuiltBinaries += "mcpproxy.exe"

if (!$OnlyCurrentPlatform) {
    # Build for Linux (with CGO disabled to avoid systray issues)
    Write-Host "Building for Linux..." -ForegroundColor Cyan
    $env:CGO_ENABLED = "0"
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"
    go build -ldflags $LDFLAGS -o mcpproxy-linux-amd64 ./cmd/mcpproxy
    if ($LASTEXITCODE -ne 0) {
        Write-Error "Failed to build for Linux"
        exit $LASTEXITCODE
    }
    $BuiltBinaries += "mcpproxy-linux-amd64"

    # Build for Windows (with CGO disabled to avoid systray issues)
    Write-Host "Building for Windows..." -ForegroundColor Cyan
    $env:CGO_ENABLED = "0"
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    go build -ldflags $LDFLAGS -o mcpproxy-windows-amd64.exe ./cmd/mcpproxy
    if ($LASTEXITCODE -ne 0) {
        Write-Error "Failed to build for Windows"
        exit $LASTEXITCODE
    }
    $BuiltBinaries += "mcpproxy-windows-amd64.exe"

    # Reset environment variables
    Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Remove-Item Env:GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:GOARCH -ErrorAction SilentlyContinue

    # Build for macOS (skip on Windows as cross-compilation for macOS systray is problematic)
    Write-Host "Skipping macOS builds (running on Windows - systray dependencies prevent cross-compilation)" -ForegroundColor Yellow
} else {
    Write-Host "Skipping cross-platform builds (OnlyCurrentPlatform flag is set)" -ForegroundColor Yellow
}

# Build tray binaries if requested
if ($BuildTray) {
    Write-Host ""
    Write-Host "Building mcpproxy-tray binaries..." -ForegroundColor Magenta
    
    # Build tray for current platform (Windows with CGO)
    Write-Host "Building tray for Windows..." -ForegroundColor Cyan
    $env:CGO_ENABLED = "1"
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    go build -ldflags $LDFLAGS -o mcpproxy-tray.exe ./cmd/mcpproxy-tray
    if ($LASTEXITCODE -ne 0) {
        Write-Warning "Failed to build tray for Windows"
    } else {
        Write-Host "✓ Windows tray binary built successfully" -ForegroundColor Green
        $BuiltBinaries += "mcpproxy-tray.exe"
    }
    
    if (!$OnlyCurrentPlatform) {
        # Build tray for macOS (requires macOS host for proper CGO compilation)
        Write-Host "Attempting to build tray for macOS..." -ForegroundColor Cyan
        $env:CGO_ENABLED = "1"
        $env:GOOS = "darwin"
        $env:GOARCH = "amd64"
        go build -ldflags $LDFLAGS -o mcpproxy-tray-darwin-amd64 ./cmd/mcpproxy-tray
        if ($LASTEXITCODE -ne 0) {
            Write-Warning "Failed to build tray for macOS (cross-compilation from Windows requires proper CGO toolchain)"
        } else {
            Write-Host "✓ macOS tray binary built successfully" -ForegroundColor Green
            $BuiltBinaries += "mcpproxy-tray-darwin-amd64"
        }
        
        # Build tray for macOS ARM64
        Write-Host "Attempting to build tray for macOS ARM64..." -ForegroundColor Cyan
        $env:CGO_ENABLED = "1"
        $env:GOOS = "darwin"
        $env:GOARCH = "arm64"
        go build -ldflags $LDFLAGS -o mcpproxy-tray-darwin-arm64 ./cmd/mcpproxy-tray
        if ($LASTEXITCODE -ne 0) {
            Write-Warning "Failed to build tray for macOS ARM64 (cross-compilation from Windows requires proper CGO toolchain)"
        } else {
            Write-Host "✓ macOS ARM64 tray binary built successfully" -ForegroundColor Green
            $BuiltBinaries += "mcpproxy-tray-darwin-arm64"
        }
    } else {
        Write-Host "Skipping cross-platform tray builds (OnlyCurrentPlatform flag is set)" -ForegroundColor Yellow
    }
    
    # Reset environment variables
    Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Remove-Item Env:GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
}

Write-Host ""
Write-Host "Build complete!" -ForegroundColor Green
Write-Host "Built binaries:" -ForegroundColor Green

# Display only the binaries built in this run
$BuiltFiles = @()
foreach ($binary in $BuiltBinaries) {
    if (Test-Path $binary) {
        $fileInfo = Get-Item $binary
        $BuiltFiles += [PSCustomObject]@{
            Name = $fileInfo.Name
            'Size (MB)' = [math]::Round($fileInfo.Length / 1MB, 2)
            LastWriteTime = $fileInfo.LastWriteTime
        }
    }
}

if ($BuiltFiles.Count -gt 0) {
    $BuiltFiles | Format-Table -AutoSize
} else {
    Write-Host "  No binaries were built" -ForegroundColor Yellow
}

Write-Host ""
Write-Host "Test version info:" -ForegroundColor Cyan
& .\mcpproxy.exe --version

