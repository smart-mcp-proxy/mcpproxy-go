#!/usr/bin/env bash
# installer-resources.test.sh — guards the prose the macOS PKG shows (audit F03).
#
# welcome_en.rtf / conclusion_en.rtf are the only authored text in the installer
# (scripts/create-pkg.sh references them from the Distribution.xml it generates).
# Nothing in CI reads them, so a false claim ships silently. These checks render
# the exact committed bytes the way Installer.app will, and assert the claims are
# still true of the shipped app and CLI.
#
# The command check reads the RENDERED panes, so it does not depend on how the
# author marked a command up in the RTF source.
#
# macOS only (needs textutil). Usage:
#   ./scripts/installer-resources.test.sh          # builds mcpproxy to a temp dir
#   MCPPROXY_BIN=./mcpproxy ./scripts/installer-resources.test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WELCOME="$ROOT/scripts/installer-resources/welcome_en.rtf"
CONCLUSION="$ROOT/scripts/installer-resources/conclusion_en.rtf"
INFO_PLIST="$ROOT/native/macos/MCPProxy/MCPProxy/Info.plist"

command -v textutil >/dev/null 2>&1 || { echo "SKIP - textutil not available (macOS only)"; exit 0; }

pass=0; fail=0
ok()  { echo "ok   - $1"; pass=$((pass+1)); }
bad() { echo "FAIL - $1"; fail=$((fail+1)); }

WELCOME_TXT="$(textutil -convert txt -stdout "$WELCOME")" || { echo "FAIL - welcome_en.rtf is not renderable RTF"; exit 1; }
CONCLUSION_TXT="$(textutil -convert txt -stdout "$CONCLUSION")" || { echo "FAIL - conclusion_en.rtf is not renderable RTF"; exit 1; }

# 1. Nothing reaches the user as literal markup. RTF renders no Markdown, and a
#    leaked control word means a malformed pane.
for pane in welcome conclusion; do
  txt="$WELCOME_TXT"; [ "$pane" = conclusion ] && txt="$CONCLUSION_TXT"
  if printf '%s' "$txt" | grep -qE '\*\*|\\b0?([[:space:]]|$)|\\f[0-9]|\\par'; then
    bad "$pane pane renders literal markup: $(printf '%s' "$txt" | grep -oE '\*\*|\\b0?|\\f[0-9]|\\par' | sort -u | tr '\n' ' ')"
  else
    ok "$pane pane renders no literal markup"
  fi
done

# 2. The minimum macOS version the welcome pane promises must match the app's own.
PLIST_MIN="$(/usr/libexec/PlistBuddy -c 'Print :LSMinimumSystemVersion' "$INFO_PLIST" 2>/dev/null)"
CLAIMED="$(printf '%s' "$WELCOME_TXT" | grep -oE 'macOS [0-9]+(\.[0-9]+)?' | head -1 | awk '{print $2}')"
if [ -z "$PLIST_MIN" ] || [ -z "$CLAIMED" ]; then
  bad "could not read minimum macOS version (plist='$PLIST_MIN' pane='$CLAIMED')"
elif [ "${CLAIMED%%.*}" = "${PLIST_MIN%%.*}" ]; then
  ok "welcome pane's minimum macOS ($CLAIMED) matches LSMinimumSystemVersion ($PLIST_MIN)"
else
  bad "welcome pane claims macOS $CLAIMED but the app requires $PLIST_MIN"
fi

# 3. Every command shown to the user must exist in the binary that ships with it.
BIN="${MCPPROXY_BIN:-}"
if [ -z "$BIN" ]; then
  BIN="$(mktemp -d)/mcpproxy"
  (cd "$ROOT" && go build -o "$BIN" ./cmd/mcpproxy) || { echo "FAIL - could not build mcpproxy to check CLI mentions"; exit 1; }
fi

# Scan the RENDERED panes rather than the RTF source. An earlier version keyed
# off the source markup (a \f1 ... \f0 monospace run, or a "quoted" phrase);
# a command written as plain body text — or in a \f1 run whose \f0 the author
# forgot — was then invisible to the extractor, and this check reported ok while
# the pane advertised a command that does not exist. Matching the rendered words
# instead makes it convention-independent. Restricting the tail to [a-z0-9-]
# also keeps the phrase free of glob and regex metacharacters, which are
# interpolated unquoted below.
commands="$(
  printf '%s\n%s\n' "$WELCOME_TXT" "$CONCLUSION_TXT" \
    | grep -oE '\bmcpproxy( +[a-z][a-z0-9-]*)*' \
    | sed 's/[[:space:]]*$//' | sort -u
)"
[ -n "$commands" ] || bad "no mcpproxy commands found in the panes (extractor broken?)"

while IFS= read -r phrase; do
  [ -n "$phrase" ] || continue
  # shellcheck disable=SC2086
  set -- $phrase; shift            # drop the "mcpproxy" word itself
  first="${1:-}"; second="${2:-}"
  case "$first" in ""|-*) continue;; esac
  if ! "$BIN" --help 2>&1 | grep -qE "^[[:space:]]+${first}([[:space:]]|$)"; then
    bad "pane advertises '$phrase' but 'mcpproxy $first' is not a command"
    continue
  fi
  case "$second" in ""|-*) ok "pane command exists: $phrase"; continue;; esac
  if "$BIN" "$first" --help 2>&1 | grep -qE "^[[:space:]]+${second}([[:space:]]|$)"; then
    ok "pane command exists: $phrase"
  else
    bad "pane advertises '$phrase' but '$second' is not a subcommand of '$first'"
  fi
done <<< "$commands"

# 4. The welcome pane must answer the certificate question before the admin
#    prompt: the installer copies a CA cert but never touches the trust store.
if printf '%s' "$WELCOME_TXT" | grep -q 'trust-cert' && printf '%s' "$WELCOME_TXT" | grep -qi 'not added to your system trust store'; then
  ok "welcome pane discloses that system trust is not modified"
else
  bad "welcome pane must say the bundled certificate is not added to your system trust store, and name 'mcpproxy trust-cert' as the opt-in"
fi

echo
echo "passed=$pass failed=$fail"
[ "$fail" -eq 0 ]
