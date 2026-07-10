#!/usr/bin/env python3
"""Check that every TICKED task in specs/<NNN>/tasks.md points at code that exists.

Why this exists
---------------
A `- [x]` in a tasks.md is an unverified assertion. The 2026-07-10 truth-sync
found 26 tasks ticked whose artifact was never built — several of them even
carried a hand-written "**COMPLETE: added at line 64**" annotation next to a
file that does not exist. Checkboxes lie, and so does the prose beside them.

This script is the DETERMINISTIC half of catching that. It never asks a model
anything: it extracts the artifacts a task cites (file paths and code symbols)
and checks whether they are really in the tree.

The hard part is false positives, not detection
-----------------------------------------------
Measured on this repo: 1,698 inline path refs across 57 specs; 51 distinct
paths cited by ticked tasks do not exist. But 50 of those 51 NEVER EXISTED in
git history — speckit tasks are written before the code, so they cite a
*planned* path (`cmd/mcpproxy/doctor.go`) and the implementation lands next to
it (`cmd/mcpproxy/doctor_cmd.go`). A naive "does the path exist" gate would be
~98% noise and would be switched off within a week.

Current output on this tree: 62 unresolved / 1 removed / 25 relocated / 147
possibly-built. Spot-checking the unresolved set finds real holes — the
symbols `upgrade_funnel`, `ActivityLogView`, `useEventStream` and
`DiagnosticsDecoder` appear ONLY inside specs/ and nowhere in the source tree,
yet their tasks are ticked. It also still contains noise (a task citing
`internal/contracts/config.go` whose types really landed under a different
name), which is exactly why this reports and never edits.

So a missing path is classified, not just reported:

  RELOCATED   an obvious stand-in exists: a same-stem sibling in the same
              directory (high confidence), or a repo-unique basename under the
              same top-level dir (medium). Informational: the task is fine, its
              path reference is stale. A relocation is never allowed to point
              at a `_test` file when the task cited an implementation.
  REMOVED     the path existed in git history and is now gone. Worth a look —
              the work may have been reverted.
  UNRESOLVED  no path, no sibling, no history. This is the real signal.

A cited SYMBOL that exists in the tree is much stronger evidence than a path,
because symbols survive file moves. When a task cites a symbol we can find, its
path complaints are demoted to informational — the work is demonstrably there.

Un-ticked tasks whose evidence all resolves are reported separately as
`possibly-built`: candidates a human (or the gardener) should look at. They are
never auto-ticked here.

Usage:
    python3 scripts/check-spec-evidence.py [SPEC ...]   # default: all specs
        --json          machine-readable, for the roadmap-gardener routine
        --strict        exit 1 when any ticked task is UNRESOLVED
        --quiet         suppress the per-spec detail, print only the summary

Exit codes: 0 report-only (default) · 1 with --strict and unresolved findings.
Pure stdlib. Read-only: never edits a tasks.md.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from collections import defaultdict

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SPECS_DIR = os.path.join(REPO_ROOT, "specs")

# "- [x] T017 [US2] Do the thing in `internal/foo/bar.go`"
TASK_RE = re.compile(r"^\s*- \[([ xX])\]\s*(T\d+)?\s*(.*)$")

# A repo-relative source path. Anchored on the real top-level dirs so prose like
# "config.json" or "v0.47.0" cannot masquerade as a file reference.
PATH_RE = re.compile(
    r"(?:^|[\s`(\[])"
    r"((?:internal|cmd|frontend/src|frontend/tests|scripts|native|specs|docs|\.github)"
    r"/[\w./-]+"
    r"\.(?:go|ts|tsx|vue|py|sh|ps1|yml|yaml|swift|md|json|iss|wxs))"
)

# A backticked identifier worth grepping for: CamelCase or snake_case, long
# enough not to collide with English. `handleSSEEvents`, `TestAuthCodeFlow`,
# `first_retrieve_tools_call_ever`.
SYMBOL_RE = re.compile(r"`([A-Za-z_][A-Za-z0-9_]{4,})`")
# A symbol distinctive enough that finding it proves something. `error_code`
# (one underscore, ubiquitous) proves nothing; `first_retrieve_tools_call_ever`
# and `TestHandleOAuthAuthorization` do.
SYMBOL_CAMEL = re.compile(r"[a-z][A-Z]")

# Identifiers that pass the shape test but mean nothing to grep.
SYMBOL_STOPLIST = {
    "tasks_md", "spec_md", "plan_md", "readme_md", "in_progress", "in_review",
    "not_started", "user_story", "acceptance_criteria",
}

def is_test_file(basename: str) -> bool:
    """Go/TS/Python/Swift test-file naming. An implementation task is never
    satisfied by the test that exercises it."""
    stem = os.path.splitext(basename)[0]
    return (stem.endswith("_test") or stem.endswith(".test")
            or stem.endswith("Tests") or stem.endswith(".spec")
            or basename.startswith("test_"))


def sh(*args: str) -> str:
    """Run a git command at the repo root; "" on failure."""
    try:
        r = subprocess.run(args, cwd=REPO_ROOT, capture_output=True, text=True)
    except OSError:
        return ""
    return r.stdout if r.returncode == 0 else ""


def build_tree_index() -> tuple[set[str], dict[str, list[str]]]:
    """(every tracked path, basename -> [paths]) — one `git ls-files` pass."""
    files = [f for f in sh("git", "ls-files").splitlines() if f]
    by_base: dict[str, list[str]] = defaultdict(list)
    for f in files:
        by_base[os.path.basename(f)].append(f)
    return set(files), by_base


def ever_existed(path: str) -> bool:
    """True if the path appears anywhere in history (so it was really removed,
    rather than never having been created)."""
    return bool(sh("git", "log", "--all", "--oneline", "--", path).strip())


def relocation_for(path: str, tracked: set[str], by_base: dict[str, list[str]]) -> str | None:
    """A plausible stand-in for a missing cited path, or None.

    Two heuristics, strongest first. Both are deliberately conservative: a bad
    match here HIDES a real finding by reclassifying it as merely-stale, which
    is the expensive direction to be wrong in.

      1. A sibling in the same directory shares the stem — `doctor.go` ->
         `doctor_cmd.go`. Same package, same name: almost certainly the file.
      2. The basename is UNIQUE in the whole repo and shares its leading path
         component — `internal/contracts/diagnostics.go` ->
         `internal/management/diagnostics.go`. A package move.

    Uniqueness is what makes (2) safe. Without it, `internal/httpapi/
    server_test.go` "relocates" to `tests/oauthserver/server_test.go`, and
    `config.go` matches a dozen unrelated files.
    """
    base = os.path.basename(path)
    stem, ext = os.path.splitext(base)
    d = os.path.dirname(path)
    cited_is_test = is_test_file(base)

    if len(stem) >= 4:
        siblings = []
        for f in tracked:
            if os.path.dirname(f) != d or not f.endswith(ext):
                continue
            fb = os.path.basename(f)
            fs = os.path.splitext(fb)[0]
            # `doctor.go` -> `doctor_cmd.go`, never `serve.go` -> `serveredition_*.go`.
            if fs != stem and not fs.startswith(stem + "_"):
                continue
            # An implementation is not satisfied by its own test file.
            if is_test_file(fb) and not cited_is_test:
                continue
            siblings.append(f)
        if siblings:
            return sorted(siblings, key=len)[0] + "  [high]"

    candidates = by_base.get(base, [])
    if len(candidates) == 1:
        top = path.split("/", 1)[0]
        if candidates[0].split("/", 1)[0] == top:
            return candidates[0] + "  [medium]"
    return None


def symbol_exists(sym: str, cache: dict[str, bool]) -> bool:
    """Is this identifier present anywhere in tracked source?"""
    if sym in cache:
        return cache[sym]
    out = sh("git", "grep", "-l", "--fixed-strings", "--", sym)
    cache[sym] = bool(out.strip())
    return cache[sym]


def extract(text: str) -> tuple[list[str], list[str]]:
    """(cited paths, cited symbols) from one task line."""
    paths = PATH_RE.findall(text)
    symbols = [s for s in SYMBOL_RE.findall(text)
               if len(s) >= 8
               and (SYMBOL_CAMEL.search(s) or s.count("_") >= 2)
               and s.lower() not in SYMBOL_STOPLIST
               and not s.endswith("_md")]
    # A symbol that is really a filename fragment adds nothing.
    symbols = [s for s in symbols if not any(s in p for p in paths)]
    return paths, symbols


def audit_spec(spec: str, tracked: set[str], by_base: dict[str, list[str]],
               sym_cache: dict[str, bool]) -> dict:
    tasks_md = os.path.join(SPECS_DIR, spec, "tasks.md")
    findings: list[dict] = []
    possibly_built: list[dict] = []
    counts = {"ticked": 0, "unticked": 0, "ticked_with_evidence": 0}

    if not os.path.isfile(tasks_md):
        return {"spec": spec, "findings": [], "possibly_built": [], "counts": counts}

    for lineno, line in enumerate(open(tasks_md, encoding="utf-8"), 1):
        m = TASK_RE.match(line)
        if not m:
            continue
        box, tid, text = m.group(1), m.group(2) or "?", m.group(3)
        ticked = box in ("x", "X")
        counts["ticked" if ticked else "unticked"] += 1

        paths, symbols = extract(text)
        if not paths and not symbols:
            continue

        found_symbols = [s for s in symbols if symbol_exists(s, sym_cache)]
        missing_paths = [p for p in paths if p not in tracked]

        if ticked:
            if paths or symbols:
                counts["ticked_with_evidence"] += 1
            if not missing_paths:
                continue  # every cited path is present — nothing to say

            # A found symbol proves the work exists regardless of where the
            # spec *predicted* the file would live.
            demoted = bool(found_symbols)
            for p in missing_paths:
                reloc = relocation_for(p, tracked, by_base)
                if reloc:
                    target, _, conf = reloc.partition("  [")
                    conf = conf.rstrip("]") or "medium"
                    verdict = "RELOCATED"
                    detail = f"stale path; code lives at {target} (confidence: {conf})"
                elif ever_existed(p):
                    verdict, detail = "REMOVED", "existed in git history, now deleted"
                else:
                    verdict, detail = "UNRESOLVED", "never existed in git history"
                if demoted and verdict == "UNRESOLVED":
                    verdict = "RELOCATED"
                    detail = (f"never existed, but cited symbol(s) "
                              f"{', '.join(found_symbols)} are present")
                findings.append({
                    "spec": spec, "task": tid, "line": lineno, "path": p,
                    "verdict": verdict, "detail": detail,
                    "symbols_found": found_symbols,
                })
        else:
            # Unticked, but everything it cites is already in the tree.
            if paths and not missing_paths and (not symbols or found_symbols):
                possibly_built.append({
                    "spec": spec, "task": tid, "line": lineno,
                    "paths": paths, "symbols_found": found_symbols,
                })

    return {"spec": spec, "findings": findings,
            "possibly_built": possibly_built, "counts": counts}


def main() -> int:
    ap = argparse.ArgumentParser(
        description="Verify that ticked spec tasks cite code that exists.")
    ap.add_argument("specs", nargs="*", help="spec dir names (default: all)")
    ap.add_argument("--json", action="store_true", help="machine-readable output")
    ap.add_argument("--strict", action="store_true",
                    help="exit 1 if any ticked task is UNRESOLVED")
    ap.add_argument("--quiet", action="store_true", help="summary only")
    args = ap.parse_args()

    specs = args.specs or sorted(
        d for d in os.listdir(SPECS_DIR)
        if os.path.isfile(os.path.join(SPECS_DIR, d, "tasks.md")))

    tracked, by_base = build_tree_index()
    sym_cache: dict[str, bool] = {}
    results = [audit_spec(s, tracked, by_base, sym_cache) for s in specs]

    findings = [f for r in results for f in r["findings"]]
    possibly = [p for r in results for p in r["possibly_built"]]
    unresolved = [f for f in findings if f["verdict"] == "UNRESOLVED"]
    removed = [f for f in findings if f["verdict"] == "REMOVED"]
    relocated = [f for f in findings if f["verdict"] == "RELOCATED"]

    if args.json:
        json.dump({"findings": findings, "possibly_built": possibly,
                   "summary": {"specs": len(specs), "unresolved": len(unresolved),
                               "removed": len(removed), "relocated": len(relocated),
                               "possibly_built": len(possibly)}},
                  sys.stdout, indent=2)
        sys.stdout.write("\n")
        return 1 if (args.strict and unresolved) else 0

    if not args.quiet:
        for group, title in ((unresolved, "UNRESOLVED — ticked, but the cited artifact was never built"),
                             (removed, "REMOVED — ticked, and the cited artifact was deleted")):
            if group:
                print(f"\n{title}:")
                for f in group:
                    print(f"  {f['spec']}/{f['task']} (line {f['line']})")
                    print(f"      {f['path']} — {f['detail']}")
        if relocated:
            print(f"\nRELOCATED ({len(relocated)}) — work exists, the path reference is stale:")
            for f in relocated[:10]:
                print(f"  {f['spec']}/{f['task']}: {f['path']} → {f['detail']}")
            if len(relocated) > 10:
                print(f"  … and {len(relocated) - 10} more")
        if possibly:
            print(f"\nPOSSIBLY BUILT ({len(possibly)}) — unticked, but every cited artifact exists:")
            for p in possibly[:10]:
                print(f"  {p['spec']}/{p['task']} (line {p['line']}): {', '.join(p['paths'][:2])}")
            if len(possibly) > 10:
                print(f"  … and {len(possibly) - 10} more")

    print(f"\n{len(specs)} specs · {len(unresolved)} unresolved · {len(removed)} removed "
          f"· {len(relocated)} relocated (informational) · {len(possibly)} possibly-built")
    if unresolved and not args.strict:
        print("(report-only; pass --strict to fail on unresolved findings)")
    return 1 if (args.strict and unresolved) else 0


if __name__ == "__main__":
    sys.exit(main())
