# Testing Guide

## Config Race Condition Tests

### Quick Test

Run the automated Go test to verify the fix:

```bash
# Test that reproduces the race condition (without fix)
go test -v ./internal/config -run TestConfigFileRaceCondition

# Test that verifies atomic writes prevent race conditions
go test -v ./internal/config -run TestAtomicConfigWrite
```

### Manual Testing

Run the shell script for a visual demonstration:

```bash
# Run with default 50 iterations
./scripts/test-config-race.sh

# Run with more iterations for better statistical analysis
./scripts/test-config-race.sh 200
```

The script will:
1. Test non-atomic writes (current implementation before fix)
2. Test atomic writes (fixed implementation)
3. Show comparison of failure rates

### Expected Results

**Before Fix (Non-Atomic)**:
- Failure rate: ~1-5% (varies by disk speed and system load)
- Errors: "Partial read" or "JSON parse error"
- Demonstrates the bug reported in GitHub issue #86

**After Fix (Atomic)**:
- Failure rate: 0%
- All reads successful
- Core never sees corrupted config files

### Understanding the Test

The test simulates the real-world scenario:
1. Config file is being written (web UI saving changes)
2. Core process starts and tries to read config
3. **Without fix**: Core sometimes reads partial file → crash
4. **With fix**: Core always sees complete file → success

### CI Integration

Add to CI pipeline:

```yaml
- name: Test config atomicity
  run: |
    go test -v ./internal/config -run TestAtomicConfigWrite
    if [ $? -ne 0 ]; then
      echo "❌ Atomic config write test failed!"
      exit 1
    fi
```

### Benchmarking

Compare performance of non-atomic vs atomic writes:

```bash
go test -bench=BenchmarkConfigWriteRead ./internal/config
```

Expected: Similar performance (fsync is negligible for config writes)

## Related Files

- `internal/config/loader.go` - Atomic write implementation
- `internal/config/race_test.go` - Automated tests
- `scripts/test-config-race.sh` - Manual test script
- `RACE_CONDITION_FIX_SUMMARY.md` - Detailed analysis and results
