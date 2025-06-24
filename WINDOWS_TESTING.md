# Windows Testing Guide

## Issue with Unit Tests on Windows

When running unit tests on Windows, you may encounter this error:

```
go test -v -race -coverprofile=coverage.out -run "^Test[^E]" ./...
no required module provides package .out; to add it:
	go get .out
Error: Process completed with exit code 1.
```

This happens because PowerShell incorrectly parses the `-coverprofile=coverage.out` argument, treating `.out` as a separate package name.

## Solutions

### Option 1: Use Provided Scripts (Recommended)

**PowerShell:**
```powershell
.\scripts\run-unit-tests.ps1
```

**Command Prompt:**
```cmd
.\scripts\run-unit-tests.cmd
```

### Option 2: Manual Command with Proper Escaping

**PowerShell:**
```powershell
go test -v -race -timeout 5m '-coverprofile=coverage.out' -covermode=atomic '-run=^Test[^E]' ./...
```

**Command Prompt:**
```cmd
go test -v -race -timeout 5m "-coverprofile=coverage.out" -covermode=atomic "-run=^Test[^E]" ./...
```

### Option 3: Run Tests Without Coverage

If you just want to run the tests without coverage:

```bash
go test -v -race -run "^Test[^E]" ./...
```

### Option 4: Use Alternative Coverage File

Use a different filename for the coverage profile:

```bash
go test -v -race -coverprofile=coverage.txt -run "^Test[^E]" ./...
```

## Running E2E Tests

E2E tests should be run separately:

```bash
go test -v -race -run "TestE2E" ./internal/server
```

## Script Features

The provided scripts include:

- **Proper argument escaping** for Windows command interpreters
- **Build verification** before running tests
- **Coverage report generation** (HTML and summary)
- **Colored output** for better readability
- **Automatic cleanup** of temporary files
- **Error handling** with appropriate exit codes

## Troubleshooting

### PowerShell Execution Policy

If you can't run the PowerShell script:

```powershell
Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser
```

### Permission Issues

Run your terminal as Administrator if you encounter permission issues.

### Go Module Issues

Make sure you're in the project root directory and run:

```bash
go mod download
go mod tidy
``` 