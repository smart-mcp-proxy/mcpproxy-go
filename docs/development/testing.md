---
id: testing
title: Testing Guide
sidebar_label: Testing
sidebar_position: 2
description: How to run tests for MCPProxy
keywords: [testing, e2e, unit tests, development]
---

# Testing Guide

This document covers how to run and write tests for MCPProxy.

## Quick Start

```bash
# Required before commits
./scripts/test-api-e2e.sh

# Full test suite
./scripts/run-all-tests.sh
```

## Test Types

### Unit Tests

```bash
# Run all unit tests
go test ./internal/... -v

# With race detection
go test -race ./internal/... -v

# Specific package
go test ./internal/server -v

# With coverage
go test -coverprofile=coverage.out ./internal/...
go tool cover -html=coverage.out
```

### E2E Tests

```bash
# API E2E tests (fast, required)
./scripts/test-api-e2e.sh

# Binary E2E tests
go test ./internal/server -run TestBinary -v

# MCP protocol E2E tests
go test ./internal/server -run TestMCP -v

# Original E2E tests (internal mocks)
./scripts/run-e2e-tests.sh
```

### OAuth Tests

```bash
# OAuth E2E test suite
./scripts/run-oauth-e2e.sh

# OAuth test server unit tests
go test ./tests/oauthserver/... -v

# OAuth integration tests
OAUTH_INTEGRATION_TESTS=1 go test ./tests/oauthserver/... -run TestIntegration -v
```

## E2E Test Requirements

The E2E tests use `@modelcontextprotocol/server-everything`:

**Prerequisites:**
- Node.js and npm installed
- `jq` installed for JSON parsing
- Built mcpproxy binary: `go build -o mcpproxy ./cmd/mcpproxy`

**Test failure investigation:**
- Check `/tmp/mcpproxy_e2e.log` for server logs
- Verify everything server connects: look for "Everything server is connected!"
- Ensure no port conflicts on 8081

## OpenAPI Coverage

```bash
# Verify all REST endpoints are documented
./scripts/verify-oas-coverage.sh
```

## Linting

```bash
# Run linter (requires golangci-lint v1.59.1+)
./scripts/run-linter.sh

# Or directly
golangci-lint run ./...
```

## Writing Tests

### Unit Test Pattern

```go
func TestFeatureName(t *testing.T) {
    // Arrange
    sut := NewSystemUnderTest()

    // Act
    result, err := sut.DoSomething()

    // Assert
    require.NoError(t, err)
    assert.Equal(t, expected, result)
}
```

### Table-Driven Tests

```go
func TestMultipleCases(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
    }{
        {"empty", "", ""},
        {"simple", "hello", "HELLO"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := Transform(tt.input)
            assert.Equal(t, tt.expected, result)
        })
    }
}
```

## CI Integration

Tests run automatically on:
- Pull requests (all tests)
- Push to main (all tests)
- Release tags (full suite + build)
