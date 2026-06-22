#!/bin/sh
# Tests for entrypoint.sh fail-closed behavior (MCP-2443).
#
# Run: sh docker/scanners/ramparts/entrypoint_test.sh
#
# These stub the `ramparts` binary on PATH so we can drive every report/exit
# combination without Docker or the real scanner, and assert the entrypoint
# fails CLOSED (non-zero exit AND no report left for the engine to read) on a
# failed/garbled scan, while keeping a genuine report (even when Ramparts exits
# non-zero to signal findings).
set -u

HERE=$(cd "$(dirname "$0")" && pwd)
ENTRYPOINT="$HERE/entrypoint.sh"
PASS=0
FAIL=0

# Build a throwaway stub-bin dir with a `ramparts` that emits a chosen payload
# (env REPORT_BODY) and exit code (env STUB_RC) to stdout, which the entrypoint
# redirects into the report file. When URL_CAPTURE_FILE is set, the stub writes
# its first positional argument (the scan target) to that file so URL-mode tests
# can verify the target is the MCP_SERVER_URL, not the stdio replay shim.
STUBDIR=$(mktemp -d)
cat > "$STUBDIR/ramparts" <<'STUB'
#!/bin/sh
if [ -n "${URL_CAPTURE_FILE:-}" ]; then
  # ramparts scan <target> ... -> target is $2
  printf '%s' "${2:-}" > "$URL_CAPTURE_FILE"
fi
printf '%s' "$REPORT_BODY"
exit "${STUB_RC:-0}"
STUB
chmod +x "$STUBDIR/ramparts"

VALID='{"url":"stdio:replay","security_issues":{"tool_issues":[]},"yara_results":[]}'
VALID_FINDING='{"url":"stdio:replay","security_issues":{"tool_issues":[{"message":"poison"}]},"yara_results":[]}'
ERROR_PAYLOAD='{"error":"failed to connect to MCP endpoint","code":1}'
GARBLED='{not valid json'
# Type-confusion error payloads: the expected keys are PRESENT but carry the
# wrong type (a status string, not the real result object/array). A presence-only
# gate would wave these through as clean (MCP-2443 re-review).
STRING_SECISSUES='{"url":"stdio:replay","security_issues":"scan failed","yara_results":[]}'
STRING_YARA='{"url":"stdio:replay","security_issues":{"tool_issues":[]},"yara_results":"oops"}'

# run_case <name> <stub_rc> <report_body> <expect_exit> <expect_report_present:yes|no>
run_case() {
  name=$1; rc=$2; body=$3; want_exit=$4; want_report=$5
  rundir=$(mktemp -d)
  REPORT_DIR="$rundir" STUB_RC="$rc" REPORT_BODY="$body" \
    PATH="$STUBDIR:$PATH" sh "$ENTRYPOINT" >/dev/null 2>&1
  got_exit=$?
  if [ -s "$rundir/results.json" ]; then got_report=yes; else got_report=no; fi
  ok=1
  [ "$got_exit" -eq "$want_exit" ] || ok=0
  [ "$got_report" = "$want_report" ] || ok=0
  if [ "$ok" -eq 1 ]; then
    PASS=$((PASS + 1)); echo "PASS $name"
  else
    FAIL=$((FAIL + 1))
    echo "FAIL $name: exit got=$got_exit want=$want_exit; report got=$got_report want=$want_report"
  fi
  rm -rf "$rundir"
}

# Acceptance: a valid report with a real finding scans and reports normally.
run_case "valid_report_rc0_kept"           0 "$VALID"         0 yes
run_case "valid_finding_report_rc0_kept"   0 "$VALID_FINDING" 0 yes
# Ramparts may exit non-zero to signal findings/offline-LLM; a valid report
# must be kept, not discarded.
run_case "valid_report_nonzero_rc_kept"    1 "$VALID_FINDING" 0 yes
# Acceptance: non-zero ramparts exit with an error payload -> scan failure.
run_case "error_payload_nonzero_fails"     1 "$ERROR_PAYLOAD" 1 no
# Error payload even on rc 0 must not be accepted as a clean report.
run_case "error_payload_rc0_fails"         0 "$ERROR_PAYLOAD" 1 no
# Acceptance: empty/garbled report -> scan marked failed (report removed).
run_case "empty_report_fails"              1 ""               1 no
run_case "garbled_report_fails"            1 "$GARBLED"       1 no
# Type-confusion: keys present but wrong type must fail closed, not read clean.
run_case "string_security_issues_fails"    0 "$STRING_SECISSUES" 1 no
run_case "string_yara_results_fails"       0 "$STRING_YARA"      1 no

# URL-mode tests: MCP_SERVER_URL is set -> ramparts should receive the URL as
# its target argument instead of the stdio replay shim.
VALID_URL='{"url":"https://api.example.com/mcp","security_issues":{"tool_issues":[]},"yara_results":[]}'
VALID_FINDING_URL='{"url":"https://api.example.com/mcp","security_issues":{"tool_issues":[{"message":"poison"}]},"yara_results":[]}'
ERROR_PAYLOAD_URL='{"error":"failed to connect to MCP endpoint","code":1,"url":"https://api.example.com/mcp"}'

# run_case_url <name> <stub_rc> <report_body> <expect_exit> <expect_report_present:yes|no> <expected_target>
run_case_url() {
  name=$1; rc=$2; body=$3; want_exit=$4; want_report=$5; want_target=$6
  rundir=$(mktemp -d)
  captfile="$rundir/captured_target"
  MCP_SERVER_URL="https://api.example.com/mcp" \
    REPORT_DIR="$rundir" STUB_RC="$rc" REPORT_BODY="$body" \
    URL_CAPTURE_FILE="$captfile" \
    PATH="$STUBDIR:$PATH" sh "$ENTRYPOINT" >/dev/null 2>&1
  got_exit=$?
  if [ -s "$rundir/results.json" ]; then got_report=yes; else got_report=no; fi
  got_target="$(cat "$captfile" 2>/dev/null || echo '')"
  ok=1
  [ "$got_exit" -eq "$want_exit" ] || ok=0
  [ "$got_report" = "$want_report" ] || ok=0
  [ "$got_target" = "$want_target" ] || ok=0
  if [ "$ok" -eq 1 ]; then
    PASS=$((PASS + 1)); echo "PASS $name"
  else
    FAIL=$((FAIL + 1))
    echo "FAIL $name: exit got=$got_exit want=$want_exit; report got=$got_report want=$want_report; target got=$got_target want=$want_target"
  fi
  rm -rf "$rundir"
}

EXPECTED_TARGET="https://api.example.com/mcp"
# URL mode: valid reports kept regardless of exit code.
run_case_url "url_valid_rc0_kept"             0 "$VALID_URL"        0 yes "$EXPECTED_TARGET"
run_case_url "url_valid_finding_rc0_kept"     0 "$VALID_FINDING_URL" 0 yes "$EXPECTED_TARGET"
run_case_url "url_valid_finding_nonzero_kept" 1 "$VALID_FINDING_URL" 0 yes "$EXPECTED_TARGET"
# URL mode: error payloads fail closed (no report left).
run_case_url "url_error_payload_nonzero_fails" 1 "$ERROR_PAYLOAD_URL" 1 no "$EXPECTED_TARGET"
run_case_url "url_error_payload_rc0_fails"     0 "$ERROR_PAYLOAD_URL" 1 no "$EXPECTED_TARGET"
# URL mode: empty/garbled reports fail closed.
run_case_url "url_empty_report_fails"          1 ""                   1 no "$EXPECTED_TARGET"
run_case_url "url_garbled_report_fails"        1 "$GARBLED"           1 no "$EXPECTED_TARGET"

rm -rf "$STUBDIR"
echo
echo "$PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
