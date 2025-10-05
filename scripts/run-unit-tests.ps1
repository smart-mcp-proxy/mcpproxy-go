# Windows PowerShell script for running unit tests
param(
    [switch]$Race = $true,
    [switch]$Cover = $true,
    [string]$CoverProfile = "coverage.out",
    [string]$Timeout = "5m",
    [switch]$Verbose = $true
)

Write-Host "Running MCP Proxy Unit Tests" -ForegroundColor Green
Write-Host "=============================" -ForegroundColor Green

# Check if Go is installed
if (!(Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host "Error: Go is not installed" -ForegroundColor Red
    exit 1
}

$goVersion = go version
Write-Host "Go version: $goVersion" -ForegroundColor Yellow

# Check if CGO is available (required for race detection)
if ($Race) {
    $env:CGO_ENABLED = "1"
    $cgoTest = go env CGO_ENABLED
    if ($cgoTest -eq "0") {
        Write-Host "Warning: CGO is not available. Race detection will be disabled." -ForegroundColor Yellow
        Write-Host "To enable race detection, install MinGW-w64: https://www.mingw-w64.org/" -ForegroundColor Yellow
        $Race = $false
    } else {
        # Check if gcc is available
        if (!(Get-Command gcc -ErrorAction SilentlyContinue)) {
            Write-Host "Warning: gcc not found. Race detection will be disabled." -ForegroundColor Yellow
            Write-Host "To enable race detection, install MinGW-w64 and add it to PATH" -ForegroundColor Yellow
            $Race = $false
        }
    }
}

Write-Host "Test timeout: $Timeout" -ForegroundColor Yellow
Write-Host "Race detection: $Race" -ForegroundColor Yellow
Write-Host "Coverage: $Cover" -ForegroundColor Yellow
Write-Host ""

# Build the binary first to ensure everything compiles
Write-Host "Building mcpproxy binary..." -ForegroundColor Yellow
go build -o mcpproxy.exe ./cmd/mcpproxy
if ($LASTEXITCODE -ne 0) {
    Write-Host "✗ Build failed" -ForegroundColor Red
    exit 1
}
Write-Host "✓ Build successful" -ForegroundColor Green

# Prepare test arguments
$testArgs = @("-v", "-timeout", $Timeout)
if ($Race) {
    $testArgs += "-race"
}
if ($Cover) {
    $testArgs += "-coverprofile=$CoverProfile"
    $testArgs += "-covermode=atomic"
}

# Add test pattern and package path
$testArgs += "-run"
$testArgs += "^Test[^E]"
$testArgs += "./..."

# Run unit tests
Write-Host "Running unit tests..." -ForegroundColor Yellow
Write-Host "Command: go test $($testArgs -join ' ')" -ForegroundColor Cyan

& go test @testArgs

if ($LASTEXITCODE -eq 0) {
    Write-Host "✓ Unit tests passed" -ForegroundColor Green
} else {
    Write-Host "✗ Unit tests failed" -ForegroundColor Red
    # Clean up
    if (Test-Path "mcpproxy.exe") { Remove-Item "mcpproxy.exe" }
    exit 1
}

# Generate coverage report if enabled
if ($Cover -and (Test-Path $CoverProfile)) {
    Write-Host "Generating coverage report..." -ForegroundColor Yellow
    go tool cover -html=$CoverProfile -o coverage.html
    Write-Host "Coverage report generated: coverage.html" -ForegroundColor Green
    
    # Show coverage summary
    $coverageOutput = go tool cover -func=$CoverProfile
    $coverageSummary = $coverageOutput | Select-Object -Last 1
    Write-Host $coverageSummary -ForegroundColor Green
}

# Cleanup
Write-Host "Cleaning up..." -ForegroundColor Yellow
if (Test-Path "mcpproxy.exe") { Remove-Item "mcpproxy.exe" }

Write-Host "All tests completed successfully!" -ForegroundColor Green 