#!/usr/bin/env bash
# Contributor recognition helpers (experiment E5).
#
#   contributors.sh thanks <pr-number>        # post (or dry-run) the thank-you comment
#   contributors.sh release <prev-tag> <ref>  # print a "Contributors" markdown section
#
# Environment: GH_TOKEN, GITHUB_REPOSITORY. DRY_RUN (thanks only) defaults to
# "true": the comment is printed, never posted. Set DRY_RUN=false to post.
#
# Safety: reads the GitHub API only. It never checks out, builds or executes
# anything from a pull request, so it is safe under pull_request_target.
# Untrusted PR text (title, login) is handled as data and never reaches eval.
set -euo pipefail

REPO="${GITHUB_REPOSITORY:-smart-mcp-proxy/mcpproxy-go}"
MARKER='<!-- contributor-thanks:v1 -->'
MAINTAINERS='Dumbris algis-dumbris'

# is_external <login> <user-type> <author-association>
# Succeeds when the PR author is an outside contributor worth thanking.
is_external() {
  local login="$1" utype="$2" assoc="$3" m
  [[ -n "$login" ]] || return 1
  [[ "$utype" != "Bot" && "$login" != *"[bot]" ]] || return 1
  local l; l="$(printf '%s' "$login" | tr '[:upper:]' '[:lower:]')"
  for m in $MAINTAINERS; do
    [[ "$l" != "$(printf '%s' "$m" | tr '[:upper:]' '[:lower:]')" ]] || return 1
  done
  [[ "$assoc" != "OWNER" && "$assoc" != "MEMBER" ]] || return 1
  return 0
}

# neutralize <text>: one line, no @mentions, no backticks/markdown links (PR titles are untrusted).
neutralize() {
  printf '%s' "$1" | tr '\r\n\t' '   ' | sed -e 's/@/@\xE2\x80\x8B/g' -e 's/[]`<>[]//g' | cut -c1-120
}

# render_comment <login> <title> <pr-url> <first-time:true|false>
render_comment() {
  local login="$1" title url="$3" first="$4"
  title="$(neutralize "$2")"
  printf '%s\n' "$MARKER"
  printf 'Thank you @%s! Your PR "%s" is merged and will ship in the next release.\n\n' "$login" "$title"
  if [[ "$first" == "true" ]]; then
    printf 'This is your first merged contribution to MCPProxy. Welcome, and congratulations!\n\n'
  fi
  printf 'If you would like to mention it, here is a snippet you can copy and edit:\n\n'
  printf '```text\n'
  printf 'My PR to MCPProxy, an open-source MCP proxy for Claude Code, Codex and Cursor, just got merged: %s\n' "$url"
  printf 'Repo: https://github.com/%s\n' "$REPO"
  printf '```\n\n'
  printf 'Sharing genuinely helps a small open-source project get found. It is entirely optional, and we are grateful either way.\n'
}

cmd_thanks() {
  local pr="$1" dry="${DRY_RUN:-true}" info login utype assoc title url merged others first body existing
  info="$(gh api "repos/$REPO/pulls/$pr")"
  merged="$(jq -r '.merged' <<<"$info")"
  login="$(jq -r '.user.login' <<<"$info")"
  utype="$(jq -r '.user.type' <<<"$info")"
  assoc="$(jq -r '.author_association' <<<"$info")"
  title="$(jq -r '.title' <<<"$info")"
  url="$(jq -r '.html_url' <<<"$info")"

  [[ "$merged" == "true" ]] || { echo "skip: PR #$pr is not merged"; return 0; }
  is_external "$login" "$utype" "$assoc" || { echo "skip: $login ($utype, $assoc) is not an external contributor"; return 0; }

  # Read all comments first: piping into `grep -q` can SIGPIPE gh and, under
  # pipefail, look like "marker not found" (a duplicate comment).
  existing="$(gh api --paginate "repos/$REPO/issues/$pr/comments" --jq '.[].body')"
  if grep -qF "$MARKER" <<<"$existing"; then
    echo "skip: PR #$pr already has a thank-you comment"; return 0
  fi

  # First contribution = no OTHER merged PR by this author. Counting "others"
  # (instead of total == 1) keeps this right while the search index lags behind
  # the merge we are reacting to. A failed search degrades to "not first".
  others="$(gh api -X GET search/issues -f q="repo:$REPO is:pr is:merged author:$login" -f per_page=5 \
    --jq "[.items[].number | select(. != $pr)] | length" 2>/dev/null)" || others=-1
  first=false; [[ "$others" == "0" ]] && first=true

  body="$(render_comment "$login" "$title" "$url" "$first")"
  echo "--- comment for PR #$pr (author=$login assoc=$assoc other_merged_prs=$others first=$first, ${#body} chars) ---"
  printf '%s\n' "$body"
  if [[ "$dry" == "true" ]]; then
    echo "--- DRY_RUN=true: nothing posted ---"
    return 0
  fi
  printf '%s\n' "$body" | gh api -X POST "repos/$REPO/issues/$pr/comments" -F body=@- >/dev/null
  echo "--- posted ---"
}

cmd_release() {
  local prev="$1" ref="$2" range nums n info login utype assoc seen=' ' list=
  range="${prev:+$prev..}$ref"
  # Squash-merge subjects end in "(#N)"; one PR number per commit.
  nums="$(git log "$range" --no-merges --pretty=format:%s | sed -n 's/.*(#\([0-9][0-9]*\))$/\1/p' | sort -un)"
  for n in $nums; do
    info="$(gh api "repos/$REPO/pulls/$n" 2>/dev/null)" || continue
    login="$(jq -r '.user.login' <<<"$info")"
    utype="$(jq -r '.user.type' <<<"$info")"
    assoc="$(jq -r '.author_association' <<<"$info")"
    is_external "$login" "$utype" "$assoc" || continue
    [[ "$seen" == *" $login "* ]] && continue
    seen+="$login "; list="${list:+$list, }@$login"
  done
  [[ -n "$list" ]] || return 0
  printf '## Contributors\n\nThank you to our external contributors for this release: %s\n' "$list"
}

if [[ "${BASH_SOURCE[0]:-$0}" == "$0" ]]; then
  case "${1:-}" in
    thanks)  cmd_thanks "${2:?pr number}" ;;
    release) cmd_release "${2-}" "${3:?ref}" ;;
    *) echo "usage: $0 thanks <pr> | release <prev-tag> <ref>" >&2; exit 2 ;;
  esac
fi
