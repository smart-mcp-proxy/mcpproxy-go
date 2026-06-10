#!/usr/bin/env bash
#
# Post the `qa-gate` GitHub commit status — the bridge that makes the Paperclip
# QATester verdict a required, mechanical pre-merge check (MCP-1214).
#
# Branch protection on `main` requires the `qa-gate` context. A PR therefore
# cannot merge (except via the retained `--admin` escape hatch) until QATester
# posts `success` for the PR's CURRENT head SHA. Because the status is keyed to
# the SHA, any new push lands on a SHA with no qa-gate status -> the check goes
# back to pending -> QA must re-run and re-bless the new head. This enforces the
# spec-075 "PASS is valid only while PR head == qa_head_sha" rule in the merge
# button itself.
#
# Usage:
#   post-qa-gate-status.sh <head-sha> <success|failure|pending> ["description"] ["target_url"]
#
# Requires: gh authenticated with repo:status scope (run on the local macOS host
# where QATester executes). Run from within the repo.
set -euo pipefail

SHA="${1:-}"
STATE="${2:-}"
DESC="${3:-}"
URL="${4:-}"

if [[ -z "$SHA" || -z "$STATE" ]]; then
  echo "usage: $0 <head-sha> <success|failure|pending> [description] [target_url]" >&2
  exit 2
fi
case "$STATE" in
  success|failure|pending) ;;
  *) echo "state must be success|failure|pending (got: $STATE)" >&2; exit 2 ;;
esac

if [[ -z "$DESC" ]]; then
  case "$STATE" in
    success) DESC="QATester PASS at this SHA" ;;
    failure) DESC="QATester did not pass at this SHA" ;;
    pending) DESC="QA verification pending at this SHA" ;;
  esac
fi

args=(repos/:owner/:repo/statuses/"$SHA"
      -f state="$STATE"
      -f context=qa-gate
      -f description="$DESC")
[[ -n "$URL" ]] && args+=(-f target_url="$URL")

gh api --method POST "${args[@]}" >/dev/null
echo "qa-gate: posted '$STATE' for $SHA"
