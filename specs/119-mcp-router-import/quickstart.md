# Quickstart (verification script for reviewers)

```bash
# Every command targets the scratch instance explicitly
D=$(mktemp -d); C="--data-dir $D --config $D/mcp_config.json"
go test ./internal/mcprouter -run TestFixtureWriteDir -count=1 -args -out $D/mcpr-fixture
./mcpproxy serve $C --listen 127.0.0.1:18921 &   # wait for: ./mcpproxy status $C

# 1. Preview: nothing changes
./mcpproxy import $C --from mcp-router --path $D/mcpr-fixture --dry-run
./mcpproxy import $C --from mcp-router --path $D/mcpr-fixture --dry-run -o json | jq '.summary'

# 2. Apply (macOS terminal: prefix MCPPROXY_KEYRING_WRITE=1), then re-run: second run adds nothing
./mcpproxy import $C --from mcp-router --path $D/mcpr-fixture --yes
./mcpproxy import $C --from mcp-router --path $D/mcpr-fixture --yes
./mcpproxy upstream list $C                      # imported servers quarantined; disabled ones disabled

# 3. Secret rules on a FRESH destination with the keyring unavailable (unit tests use a fake provider;
#    manually: run in an environment where KeyringAvailability() is false, e.g. macOS terminal without
#    MCPPROXY_KEYRING_WRITE): refuse with exit 24, then opt in
D2=$(mktemp -d); C2="--data-dir $D2 --config $D2/mcp_config.json"
./mcpproxy import $C2 --from mcp-router --path $D/mcpr-fixture --yes; echo $?   # 24, nothing written
./mcpproxy import $C2 --from mcp-router --path $D/mcpr-fixture --yes --secrets-inline

# 4. Export file path
./mcpproxy import $C --from mcp-router --path $D/mcpr-fixture/mcp-servers-2026-09-18.json --dry-run
```

Expected: step 1 lists classes and redacted summaries and exits 0; step 2's second run reports `already_present` for every server (config file byte-identical); step 3 exits 24 and writes nothing, then succeeds with the opt-in; canary secrets never appear in any output.
