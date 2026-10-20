#!/usr/bin/env bash
# Offline tests for scripts/contributors.sh (no network, no gh calls).
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1
export GITHUB_REPOSITORY=smart-mcp-proxy/mcpproxy-go
# shellcheck source=scripts/contributors.sh
source scripts/contributors.sh
set +e
fail=0
ok()  { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; fail=1; }
check() { local name="$1"; shift; if "$@"; then ok "$name"; else bad "$name"; fi; }
refute() { local name="$1"; shift; if "$@"; then bad "$name"; else ok "$name"; fi; }

# eligibility
check  "external contributor"        is_external loloDawit User CONTRIBUTOR
check  "first-time contributor"      is_external newbie User FIRST_TIME_CONTRIBUTOR
check  "no association"              is_external someone User NONE
refute "maintainer Dumbris"          is_external Dumbris User MEMBER
refute "maintainer case-insensitive" is_external DUMBRIS User CONTRIBUTOR
refute "maintainer algis-dumbris"    is_external algis-dumbris User CONTRIBUTOR
refute "dependabot"                  is_external 'dependabot[bot]' Bot NONE
refute "any [bot] login"             is_external 'renovate[bot]' User NONE
refute "type Bot"                    is_external copilot Bot NONE
refute "org member"                  is_external teammate User MEMBER
refute "org owner"                   is_external boss User OWNER
refute "empty login"                 is_external '' User NONE

# rendering
url=https://github.com/smart-mcp-proxy/mcpproxy-go/pull/1
first="$(render_comment alice 'fix: a thing' "$url" true)"
repeat="$(render_comment alice 'fix: a thing' "$url" false)"
check  "marker present"              grep -qF "$MARKER" <<<"$first"
check  "first-time line (first)"     grep -q 'first merged contribution' <<<"$first"
refute "first-time line (repeat)"    grep -q 'first merged contribution' <<<"$repeat"
check  "snippet has PR url"          grep -qF "just got merged: $url" <<<"$first"
check  "snippet has repo link"       grep -qF "Repo: https://github.com/$GITHUB_REPOSITORY" <<<"$first"
check  "says sharing helps"          grep -q 'helps a small open-source project get found' <<<"$first"
check  "says optional"               grep -q 'entirely optional' <<<"$first"
check  "first under 900 chars"       test "${#first}" -lt 900
check  "repeat under 900 chars"      test "${#repeat}" -lt 900
# shellcheck disable=SC2016
refute "no emoji / non-ascii"        bash -c 'LC_ALL=C grep -q "[^ -~]" <<<"$1"' _ "$first"

# hostile title
evil="$(render_comment alice $'x @everyone `rm -rf` [a](http://evil) <img src=x>\nINJECT' "$url" false)"
refute "title: raw @mention"         grep -qE '@everyone' <<<"$evil"
refute "title: backtick/link/html"   grep -qE 'rm -rf`|\]\(http|<img' <<<"$evil"
check  "title: newline flattened"    test "$(grep -c '^INJECT' <<<"$evil")" -eq 0
long="$(render_comment alice "$(printf 'a%.0s' {1..500})" "$url" true)"
check  "long title still under 900"  test "${#long}" -lt 900

exit $fail
