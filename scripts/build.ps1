#!/usr/bin/env pwsh
# PowerShell build script for mcpproxy

param(
    [string]$Version = ""
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

# Build for current platform (with CGO for tray support if needed)
Write-Host "Building for current platform..." -ForegroundColor Cyan
go build -ldflags $LDFLAGS -o mcpproxy.exe ./cmd/mcpproxy
if ($LASTEXITCODE -ne 0) {
    Write-Error "Failed to build for current platform"
    exit $LASTEXITCODE
}

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

# Reset environment variables
Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
Remove-Item Env:GOOS -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue

# Build for macOS (skip on Windows as cross-compilation for macOS systray is problematic)
Write-Host "Skipping macOS builds (running on Windows - systray dependencies prevent cross-compilation)" -ForegroundColor Yellow

Write-Host ""
Write-Host "Build complete!" -ForegroundColor Green
Write-Host "Available binaries:" -ForegroundColor Green
Get-ChildItem -Path . -Filter "mcpproxy*" | Select-Object Name, Length, LastWriteTime | Format-Table -AutoSize

Write-Host ""
Write-Host "Test version info:" -ForegroundColor Cyan
& .\mcpproxy.exe --version

