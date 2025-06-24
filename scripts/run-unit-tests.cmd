@echo off
setlocal enabledelayedexpansion

echo Running MCP Proxy Unit Tests
echo =============================

REM Check if Go is installed
where go >nul 2>nul
if %errorlevel% neq 0 (
    echo Error: Go is not installed
    exit /b 1
)

echo Go version:
go version
echo.

echo Building mcpproxy binary...
go build -o mcpproxy.exe ./cmd/mcpproxy
if %errorlevel% neq 0 (
    echo Build failed
    exit /b 1
)
echo Build successful
echo.

echo Running unit tests...
REM Use quotes to properly handle the coverprofile argument
go test -v -race -timeout 5m "-coverprofile=coverage.out" -covermode=atomic "-run=^Test[^E]" ./...

set test_result=%errorlevel%

if %test_result% equ 0 (
    echo.
    echo Unit tests passed
    
    REM Generate coverage report if coverage.out exists
    if exist coverage.out (
        echo Generating coverage report...
        go tool cover "-html=coverage.out" -o coverage.html
        echo Coverage report generated: coverage.html
        
        REM Show coverage summary
        go tool cover "-func=coverage.out" | findstr /C:"total:"
    )
    
    echo All tests completed successfully!
) else (
    echo.
    echo Unit tests failed
)

REM Cleanup
echo Cleaning up...
if exist mcpproxy.exe del mcpproxy.exe
if exist coverage.out del coverage.out

exit /b %test_result% 