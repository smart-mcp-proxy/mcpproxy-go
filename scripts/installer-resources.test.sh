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
#
#    The control-word alternatives are terminated by a non-[a-z] character (or
#    end of string), which is what an RTF control word actually looks like when
#    it leaks. Without that terminator the pattern both over- and under-fires:
#    an ordinary rendered path like "C:\party" matched \par, while a leaked
#    "\b0." was missed because the old \b0 arm demanded whitespace after it.
#    The numeric controls (\fN, \fsNN) carry NO such terminator: an RTF numeric
#    parameter ends at the first non-digit, so a leaked "\f0hello" is a font
#    control followed by text and must still be caught. They need no boundary
#    because "\f" or "\fs" immediately followed by a digit is not something
#    ordinary prose contains.
MARKUP_RE='\*\*|\\fs?[0-9]|\\(b|b0|i|i0|ul|ulnone|par|pard|line|tab)([^a-z]|$)'
for pane in welcome conclusion; do
  txt="$WELCOME_TXT"; [ "$pane" = conclusion ] && txt="$CONCLUSION_TXT"
  # Capture, then branch on grep's own status. `if grep ...; then bad; else ok`
  # reads any non-zero status as "clean", so a grep ERROR (exit >1) would have
  # reported ok and let the script exit 0 with the check never having run.
  markup="$(printf '%s' "$txt" | grep -oE "$MARKUP_RE")"
  rc=$?
  if [ "$rc" -gt 1 ]; then
    bad "$pane pane: markup scan could not run (grep exit $rc)"
  elif [ -n "$markup" ]; then
    bad "$pane pane renders literal markup: $(printf '%s' "$markup" | sort -u | tr '\n' ' ')"
  else
    ok "$pane pane renders no literal markup"
  fi
done

# 2. The minimum macOS version the welcome pane promises must match the app's own.
#    Anchored to the requirement line ("... or later") rather than the first
#    mention of macOS anywhere in the pane, and compared on the full version
#    rather than the major, so neither an unrelated sentence nor a 13.0 -> 13.5
#    bump can slip through.
norm_ver() { case "$1" in *.*) printf '%s' "$1";; *) printf '%s.0' "$1";; esac; }
# Match the version that is BOUND to "or later" — optionally through a codename
# parenthetical — rather than the first macOS mention on a line that happens to
# contain the phrase. "Unlike macOS 13, macOS 14 or later is required" must
# yield 14, not 13. Any number of dotted components is captured, so a promise of
# 13.0.1 is compared as 13.0.1 and not silently truncated to 13.0; anything that
# is not a dotted number ("macOS 13.garbage or later") matches nothing and is
# reported as unreadable rather than passing on its leading digits.
PLIST_MIN="$(/usr/libexec/PlistBuddy -c 'Print :LSMinimumSystemVersion' "$INFO_PLIST" 2>/dev/null)"
CLAIMED="$(printf '%s' "$WELCOME_TXT" \
  | grep -oE 'macOS [0-9]+(\.[0-9]+)*[[:space:]]*(\([^)]*\))?[[:space:]]*or later' \
  | head -1 | awk '{print $2}')"
if [ -z "$PLIST_MIN" ] || [ -z "$CLAIMED" ]; then
  bad "could not read minimum macOS version (plist='$PLIST_MIN' pane='$CLAIMED'); the welcome pane needs a 'macOS <version> [(codename)] or later' line"
elif [ "$(norm_ver "$CLAIMED")" = "$(norm_ver "$PLIST_MIN")" ]; then
  ok "welcome pane's minimum macOS ($CLAIMED) matches LSMinimumSystemVersion ($PLIST_MIN)"
else
  bad "welcome pane claims macOS $CLAIMED but the app requires $PLIST_MIN"
fi

# 3. Every command shown to the user must exist in the binary that ships with it.
BIN="${MCPPROXY_BIN:-}"
if [ -z "$BIN" ]; then
  BUILD_DIR="$(mktemp -d)"
  # Remove it on every exit path, including the build-failure one below.
  trap 'rm -rf "$BUILD_DIR"' EXIT
  BIN="$BUILD_DIR/mcpproxy"
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
