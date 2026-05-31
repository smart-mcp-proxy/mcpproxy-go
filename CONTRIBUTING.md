# Contributing to MCPProxy

Thanks for your interest in improving MCPProxy! We welcome issues, feature
ideas, and pull requests.

For development setup, pre-commit hooks, and the build/test `make` targets, see
the [Contributing section in the README](README.md#contributing).

## Running the test suite

Before opening a pull request, run the test suite locally. Build the binary
first (`make build`), since the end-to-end tests drive the compiled `mcpproxy`.

```bash
./scripts/run-all-tests.sh     # Full suite: unit, race, E2E, and coverage
./scripts/test-api-e2e.sh      # Quick REST API end-to-end smoke test
```

- **`scripts/run-all-tests.sh`** runs the complete suite — unit tests, race
  detection, and the API E2E stage — and writes a coverage report. Use this for
  a thorough check before merging.
- **`scripts/test-api-e2e.sh`** stands up a local mcpproxy instance and exercises
  the `/api/v1` REST endpoints. It's faster and is the minimum check for any
  change that touches the server or API.

You can also run the Go tests directly:

```bash
go test ./internal/... -v          # Unit tests
go test -race ./internal/... -v    # With the race detector
```

See [CLAUDE.md](CLAUDE.md) and the [docs](https://docs.mcpproxy.app) for the
full development and testing reference.
