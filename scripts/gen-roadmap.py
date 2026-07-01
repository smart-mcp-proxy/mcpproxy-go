#!/usr/bin/env python3
"""Generate ROADMAP.md from roadmap.yaml (+ live specs/<NNN>/tasks.md badges).

This is the renderer for the git-native roadmap prototype. It reads the
hand-maintained DAG in roadmap.yaml, recomputes per-spec progress by counting
checkboxes in each specs/<NNN>/tasks.md, and writes a single ROADMAP.md
containing:

  1. A generated-file banner + schema/regenerate instructions.
  2. A Mermaid `graph TD` of the epic/task DAG, styled by status.
  3. A status table (epic, status, assignee, progress, spec/PR links).
  4. An aggregate per-spec progress table (recomputed from tasks.md).

Design choice: we write the aggregate spec table into ROADMAP.md rather than
overwriting the hand-maintained specs/README.md, so the existing curated index
(with its prose, runbooks and design-doc links) is never clobbered. ROADMAP.md
is fully generated and safe to overwrite on every run.

Usage:
    python3 scripts/gen-roadmap.py [--check]

    --check   Exit non-zero if ROADMAP.md is out of date (does not write).
              Useful as a CI canary.

Pure stdlib + PyYAML (already used by scripts/check-settings-parity.py).
Idempotent: running twice with no source change produces identical output.
"""
from __future__ import annotations

import argparse
import os
import re
import sys

try:
    import yaml
except ImportError:  # pragma: no cover
    sys.stderr.write("error: PyYAML required (pip install pyyaml)\n")
    sys.exit(2)

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ROADMAP_YAML = os.path.join(REPO_ROOT, "roadmap.yaml")
ROADMAP_MD = os.path.join(REPO_ROOT, "ROADMAP.md")
SPECS_DIR = os.path.join(REPO_ROOT, "specs")

# A checkbox line: "- [ ] ...", "- [x] ...", "- [X] ..." (matches specs/README.md).
CHECKBOX_RE = re.compile(r"^- \[([ xX])\]")

STATUS_ORDER = ["in_progress", "in_review", "blocked", "todo", "done"]
STATUS_LABEL = {
    "todo": "Todo",
    "in_progress": "In progress",
    "in_review": "In review",
    "blocked": "Blocked",
    "done": "Done",
}
# Mermaid classDef colours keyed by status.
STATUS_CLASSDEF = {
    "done": "fill:#1f7a1f,stroke:#0d3d0d,color:#ffffff",
    "in_progress": "fill:#1f6feb,stroke:#0b3d91,color:#ffffff",
    "in_review": "fill:#9a6700,stroke:#5c3d00,color:#ffffff",
    "blocked": "fill:#a40e26,stroke:#5c0712,color:#ffffff",
    "todo": "fill:#6e7781,stroke:#3d4248,color:#ffffff",
    "parked": "fill:#30363d,stroke:#161b22,color:#9da7b3,stroke-dasharray:4 3",
}


# ── spec checkbox accounting ────────────────────────────────────────────────
def count_checkboxes(tasks_md: str) -> tuple[int, int]:
    """Return (checked, total) from a tasks.md path. (0, 0) if absent."""
    if not os.path.isfile(tasks_md):
        return (0, 0)
    checked = total = 0
    with open(tasks_md, encoding="utf-8") as fh:
        for line in fh:
            m = CHECKBOX_RE.match(line)
            if not m:
                continue
            total += 1
            if m.group(1) in ("x", "X"):
                checked += 1
    return (checked, total)


def spec_badge(checked: int, total: int) -> tuple[str, str]:
    """Map counts to (status_word, progress_str) using the specs/README legend."""
    if total == 0:
        return ("—", "—")
    pct = round(100 * checked / total)
    if pct >= 95:
        word = "shipped"
    elif pct >= 1:
        word = "in-flight"
    else:
        word = "drafted"
    return (word, f"{checked}/{total} ({pct}%)")


def spec_progress(spec_path: str | None) -> tuple[str, str]:
    """Resolve a roadmap spec: link to a (status, progress) badge pair."""
    if not spec_path:
        return ("", "")
    tasks_md = os.path.join(REPO_ROOT, spec_path, "tasks.md")
    return spec_badge(*count_checkboxes(tasks_md))


# ── node id sanitising for Mermaid ──────────────────────────────────────────
def node_id(raw: str) -> str:
    """Mermaid node ids must be alnum/underscore."""
    return re.sub(r"[^0-9A-Za-z]", "_", raw)


def status_of(item: dict) -> str:
    """Effective status; parked todos render as a distinct 'parked' class."""
    st = item.get("status", "todo")
    if st == "todo" and item.get("parked"):
        return "parked"
    return st


# ── rendering ───────────────────────────────────────────────────────────────
def fmt_pr(pr) -> str:
    if not pr:
        return ""
    if isinstance(pr, list):
        return " ".join(str(p) for p in pr)
    return str(pr)


def mermaid_label(item: dict) -> str:
    """Node shape+label: `["title<br/>MCP-xxx"]`. Quotes let parens/slashes/em
    dashes survive; brackets give the node its (default rectangle) shape."""
    title = item["title"].replace('"', "'")
    mcp = item.get("mcp")
    inner = f"{title}<br/>{mcp}" if mcp else title
    return f'["{inner}"]'


def render_mermaid(epics: list[dict]) -> str:
    lines = ["```mermaid", "graph TD"]
    classed: dict[str, list[str]] = {k: [] for k in STATUS_CLASSDEF}

    # Declare nodes (epics as subgraphs containing their tasks).
    for epic in epics:
        eid = node_id(epic["id"])
        classed[status_of(epic)].append(eid)
        tasks = epic.get("tasks") or []
        if tasks:
            lines.append(f'  subgraph sg_{eid}["{epic["title"].replace(chr(34), chr(39))}"]')
            lines.append(f"    {eid}{mermaid_label(epic)}")
            for t in tasks:
                tid = node_id(t["id"])
                lines.append(f"    {tid}{mermaid_label(t)}")
                classed[status_of(t)].append(tid)
            lines.append("  end")
        else:
            lines.append(f"  {eid}{mermaid_label(epic)}")

    # Edges (prerequisite --> dependent).
    lines.append("")
    for epic in epics:
        eid = node_id(epic["id"])
        for dep in epic.get("depends_on") or []:
            lines.append(f"  {node_id(dep)} --> {eid}")
        for t in epic.get("tasks") or []:
            tid = node_id(t["id"])
            for dep in t.get("depends_on") or []:
                lines.append(f"  {node_id(dep)} --> {tid}")

    # Class definitions + assignments.
    lines.append("")
    for status, style in STATUS_CLASSDEF.items():
        lines.append(f"  classDef {status} {style};")
    for status, ids in classed.items():
        if ids:
            lines.append(f"  class {','.join(ids)} {status};")

    lines.append("```")
    return "\n".join(lines)


def render_status_table(epics: list[dict]) -> str:
    rows = ["| Epic | Status | Assignee | Priority | Progress | Spec | PR |",
            "| --- | --- | --- | --- | --- | --- | --- |"]
    order = {s: i for i, s in enumerate(STATUS_ORDER)}

    def sort_key(e):
        return (order.get(e.get("status", "todo"), 99),
                1 if e.get("parked") else 0,
                e.get("priority", "P9"))

    for epic in sorted(epics, key=sort_key):
        st = STATUS_LABEL.get(epic.get("status", "todo"), epic.get("status", ""))
        if epic.get("parked"):
            st += " (parked)"
        _, progress = spec_progress(epic.get("spec"))
        spec = epic.get("spec")
        spec_cell = f"[{os.path.basename(spec)}](./{spec}/)" if spec else ""
        pr = fmt_pr(epic.get("pr"))
        mcp = epic.get("mcp")
        epic_cell = epic["title"] + (f" `{mcp}`" if mcp else "")
        rows.append(
            f"| {epic_cell} | {st} | {epic.get('assignee', '')} | "
            f"{epic.get('priority', '')} | {progress or '—'} | {spec_cell} | {pr} |"
        )
    return "\n".join(rows)


def render_spec_table() -> str:
    """Recompute the aggregate per-spec progress table from tasks.md files."""
    rows = ["| # | Status | Progress |", "| --- | --- | --- |"]
    for name in sorted(os.listdir(SPECS_DIR)):
        spec_dir = os.path.join(SPECS_DIR, name)
        if not os.path.isdir(spec_dir):
            continue
        if not re.match(r"^\d", name):  # only numbered spec dirs
            continue
        word, progress = spec_badge(*count_checkboxes(os.path.join(spec_dir, "tasks.md")))
        badge = f"`{word}`" if word != "—" else "—"
        rows.append(f"| [{name}](./specs/{name}/) | {badge} | {progress} |")
    return "\n".join(rows)


def render(data: dict) -> str:
    epics = data.get("epics", [])
    out = []
    out.append("<!-- GENERATED FILE — do not edit by hand. -->")
    out.append("<!-- Source: roadmap.yaml  ·  Generator: scripts/gen-roadmap.py -->")
    out.append("<!-- Regenerate: python3 scripts/gen-roadmap.py  (or scripts/gen-roadmap) -->")
    out.append("")
    out.append("# MCPProxy Roadmap")
    out.append("")
    out.append("> **Generated — do not edit by hand.** This file is rendered from "
               "[`roadmap.yaml`](./roadmap.yaml) by [`scripts/gen-roadmap.py`](./scripts/gen-roadmap.py). "
               "Edit `roadmap.yaml` and re-run the generator.")
    out.append("")
    out.append("The roadmap models cross-spec **epics → tasks** with a dependency DAG, "
               "execution `status`, `assignee`, `priority`, and links — the things a "
               "per-spec `tasks.md` checkbox list cannot express. Per-spec checkbox "
               "progress is recomputed live from each `specs/<NNN>/tasks.md`.")
    out.append("")
    out.append("## How to regenerate")
    out.append("")
    out.append("```bash")
    out.append("python3 scripts/gen-roadmap.py     # writes ROADMAP.md")
    out.append("scripts/gen-roadmap                # convenience wrapper (same thing)")
    out.append("python3 scripts/gen-roadmap.py --check   # CI canary: fail if stale")
    out.append("```")
    out.append("")
    out.append("## roadmap.yaml schema (short form)")
    out.append("")
    out.append("- **epics[]** — each has `id` (stable slug, DAG node), `title`, "
               "`status` (todo·in_progress·in_review·blocked·done), `assignee`, "
               "`priority` (P0–P3), `depends_on: [ids]` (DAG edges, prerequisite→dependent), "
               "optional `parked: true`, and links `spec:` / `pr:` / `mcp:` (external MCP-xxxx).")
    out.append("- **epics[].tasks[]** — child tasks with the same fields; their "
               "`depends_on` may reference sibling tasks or other epics.")
    out.append("- See the header comment in `roadmap.yaml` for the full field reference.")
    out.append("")
    out.append("## Epic / task DAG")
    out.append("")
    out.append("Node colour = status (green done · blue in-progress · amber in-review · "
               "red blocked · grey todo · dashed grey parked). Edges point "
               "prerequisite → dependent.")
    out.append("")
    out.append(render_mermaid(epics))
    out.append("")
    out.append("## Epics")
    out.append("")
    out.append(render_status_table(epics))
    out.append("")
    out.append("## Per-spec progress (recomputed from `specs/<NNN>/tasks.md`)")
    out.append("")
    out.append("Legend: `shipped` ≥95% checked · `in-flight` 1–94% · `drafted` 0% · "
               "`—` no `tasks.md`. This aggregate is regenerated here rather than "
               "overwriting the hand-maintained [`specs/README.md`](./specs/README.md), "
               "which keeps its curated prose, runbooks and design-doc links.")
    out.append("")
    out.append(render_spec_table())
    out.append("")
    return "\n".join(out)


def main() -> int:
    ap = argparse.ArgumentParser(description="Generate ROADMAP.md from roadmap.yaml")
    ap.add_argument("--check", action="store_true",
                    help="exit non-zero if ROADMAP.md is stale (do not write)")
    args = ap.parse_args()

    with open(ROADMAP_YAML, encoding="utf-8") as fh:
        data = yaml.safe_load(fh)

    rendered = render(data)

    if args.check:
        current = ""
        if os.path.isfile(ROADMAP_MD):
            with open(ROADMAP_MD, encoding="utf-8") as fh:
                current = fh.read()
        if current != rendered:
            sys.stderr.write("ROADMAP.md is out of date; run scripts/gen-roadmap.py\n")
            return 1
        print("ROADMAP.md is up to date.")
        return 0

    with open(ROADMAP_MD, "w", encoding="utf-8") as fh:
        fh.write(rendered)
    print(f"wrote {os.path.relpath(ROADMAP_MD, REPO_ROOT)} "
          f"({len(data.get('epics', []))} epics)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
