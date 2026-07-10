#!/usr/bin/env python3
"""Generate ROADMAP.md from roadmap.yaml (+ live specs/<NNN>/tasks.md badges).

This is the renderer for the git-native roadmap prototype. It reads the
hand-maintained DAG in roadmap.yaml, recomputes per-spec progress by counting
checkboxes in each specs/<NNN>/tasks.md, and writes a single ROADMAP.md
containing:

  1. A generated-file banner + schema/regenerate instructions.
  2. "Roadmap at a glance": a small Mermaid `graph LR` of the CROSS-EPIC DAG
     (only epics that have an edge; the dependency-free ones are listed as text
     beneath it) plus a per-epic `<details>` block holding that epic's own task
     graph and task table. One 70-node graph was illegible on GitHub; splitting
     it by zoom level is what makes the roadmap browsable.
  3. A status table (epic, status, priority, progress, spec/PR links).
  4. A compact "Shipped" table of epics swept into roadmap.archive.yaml.
  5. An aggregate per-spec progress table (recomputed from tasks.md).

Design choice: we write the aggregate spec table into ROADMAP.md rather than
overwriting the hand-maintained specs/README.md, so the existing curated index
(with its prose, runbooks and design-doc links) is never clobbered. ROADMAP.md
is fully generated and safe to overwrite on every run.

Archive: roadmap.yaml is the WORKING SET (todo/in_progress/in_review/blocked/
parked). Cold `done` epics are swept into roadmap.archive.yaml by `--archive`
so the active file does not grow without bound. Provenance (PR refs, notes) is
preserved verbatim: the sweep moves the raw YAML text block, it does not
re-serialise it. A `depends_on:` edge pointing at an archived id is satisfied
by definition (archived == done), so the generator resolves ids against BOTH
files and only ERRORs on ids that exist in neither.

Usage:
    python3 scripts/gen-roadmap.py [--check | --check-github [--strict]]
    python3 scripts/gen-roadmap.py --archive [--min-age-days N] [--dry-run]

    --check         Exit non-zero if ROADMAP.md is out of date (does not write).
                    Useful as a CI canary.
    --check-github  Cross-check roadmap.yaml against ground truth (does not write):
                    live GitHub PR state (via `gh`), spec: links resolving to
                    real specs/ dirs, depends_on ids resolving to a known epic or
                    task, and status sanity. Reports ERROR/WARN and exits 1 on any
                    ERROR, 0 otherwise; 2 if `gh` is unavailable.
    --strict        With --check-github, promote warnings to errors for the exit
                    code (report is unchanged).
    --archive       Sweep cold `done` epics out of roadmap.yaml into
                    roadmap.archive.yaml, then regenerate ROADMAP.md. An epic is
                    cold when it is `done`, every child task is `done`, every PR
                    it references is MERGED, and the NEWEST of those merges is at
                    least --min-age-days old (default 14 — "shipped and two weeks
                    cold"). Epics carrying `keep: true` are never swept; epics
                    with no PR refs cannot be dated and are skipped unless
                    --min-age-days 0. Needs an authenticated `gh` unless
                    --min-age-days 0.
    --min-age-days  Cool-off window for --archive (default 14).
    --dry-run       With --archive, print what would move and change nothing.

Pure stdlib + PyYAML (already used by scripts/check-settings-parity.py).
Idempotent: running twice with no source change produces identical output.
"""
from __future__ import annotations

import argparse
import datetime as _dt
import json
import os
import re
import shutil
import subprocess
import sys

try:
    import yaml
except ImportError:  # pragma: no cover
    sys.stderr.write("error: PyYAML required (pip install pyyaml)\n")
    sys.exit(2)

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ROADMAP_YAML = os.path.join(REPO_ROOT, "roadmap.yaml")
ROADMAP_ARCHIVE_YAML = os.path.join(REPO_ROOT, "roadmap.archive.yaml")
ROADMAP_MD = os.path.join(REPO_ROOT, "ROADMAP.md")
SPECS_DIR = os.path.join(REPO_ROOT, "specs")

# Default cool-off before a done epic is swept into the archive.
DEFAULT_MIN_AGE_DAYS = 14

ARCHIVE_HEADER = """\
# roadmap.archive.yaml — cold storage for SHIPPED epics.
#
# Swept out of roadmap.yaml by `python3 scripts/gen-roadmap.py --archive` once an
# epic is `done`, all its PRs are merged, and the newest merge has cooled off
# (default 14 days). roadmap.yaml stays the working set; this file keeps the
# provenance: PR refs, gotcha notes, and the DAG ids other epics depend on.
#
# Schema is identical to roadmap.yaml, plus two generator-stamped fields:
#   shipped_on:  date of the NEWEST merged PR in the epic subtree
#   archived_on: date the sweep moved it here
#
# A `depends_on:` edge pointing at an id in this file is satisfied by definition.
# Nothing here is re-serialised: blocks are moved as raw text, so comments and
# formatting survive. Append-only in practice; edit by hand only to fix a typo.
"""

# GitHub repo `gh` queries target for --check-github.
REPO_SLUG = "smart-mcp-proxy/mcpproxy-go"
# A PR ref inside a pr: field, either "#786" or ".../pull/786".
PR_NUM_RE = re.compile(r"(?:#|/pull/)(\d+)")

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


def all_ids(epics: list[dict]) -> set[str]:
    """Every epic id and task id declared in a roadmap document."""
    ids: set[str] = set()
    for epic in epics:
        ids.add(epic["id"])
        for t in epic.get("tasks") or []:
            ids.add(t["id"])
    return ids


# ── DAG rendering ────────────────────────────────────────────────────────────
# The roadmap has two zoom levels — ~24 epics and ~70 tasks — and cramming both
# into one Mermaid `graph TD` renders as an illegible postage stamp on GitHub.
# We split them: a small EPIC-LEVEL overview graph (legible at default zoom),
# then per-epic <details> blocks (GitHub renders Mermaid + tables inside them)
# each carrying that one epic's task DAG and links. A reader expands only what
# they care about, so the page stays short.

# Status glyphs for the collapsed <summary> lines and per-task table cells,
# mirroring the STATUS_CLASSDEF fills (green/blue/amber/red/grey/dark).
STATUS_EMOJI = {
    "done": "🟢",
    "in_progress": "🔵",
    "in_review": "🟡",
    "blocked": "🔴",
    "todo": "⚪",
    "parked": "⚫",
}


def truncate(text: str, limit: int) -> str:
    """Collapse whitespace and clip to `limit` chars with an ellipsis."""
    text = " ".join(text.split())
    return text if len(text) <= limit else text[: limit - 1].rstrip() + "…"


def node_box(label: str) -> str:
    """A default-rectangle Mermaid node body: `["label"]`, quotes escaped so
    parens/slashes/em dashes survive."""
    return f'["{label.replace(chr(34), chr(39))}"]'


def epic_owner_map(epics: list[dict]) -> dict[str, str]:
    """Map every node id (epic id or task id) to the id of its owning epic."""
    owner: dict[str, str] = {}
    for epic in epics:
        owner[epic["id"]] = epic["id"]
        for t in epic.get("tasks") or []:
            owner[t["id"]] = epic["id"]
    return owner


def iter_edges(epics: list[dict]):
    """Yield every (prerequisite_id, dependent_id) edge in the whole DAG."""
    for epic in epics:
        for dep in epic.get("depends_on") or []:
            yield (dep, epic["id"])
        for t in epic.get("tasks") or []:
            for dep in t.get("depends_on") or []:
                yield (dep, t["id"])


def _emit_classes(lines: list[str], classed: dict[str, list[str]]) -> None:
    """Append Mermaid classDef + class-assignment lines for used statuses only."""
    for status, ids in classed.items():
        if ids:
            lines.append(f"  classDef {status} {STATUS_CLASSDEF[status]};")
    for status, ids in classed.items():
        if ids:
            lines.append(f"  class {','.join(ids)} {status};")


def overview_edges(epics: list[dict]) -> list[tuple[str, str]]:
    """Distinct epic→epic edges, collapsing every cross-epic task edge onto the
    epics that own its endpoints. Edges whose endpoint resolves to an archived
    or absent id are dropped — an edge into the archive is satisfied by
    definition, and drawing it would conjure a phantom node."""
    owner = epic_owner_map(epics)
    seen: list[tuple[str, str]] = []
    for dep, dependent in iter_edges(epics):
        src, dst = owner.get(dep), owner.get(dependent)
        if not src or not dst or src == dst or (src, dst) in seen:
            continue
        seen.append((src, dst))
    return seen


def render_overview_mermaid(epics: list[dict]) -> tuple[str, list[dict]]:
    """Epic-level overview: one node per *connected* epic, epic→epic edges only.

    Returns (mermaid_block, standalone_epics). Epics with no cross-epic edge are
    NOT drawn: with 24 epics and only ~6 edges, including them turned the graph
    into a 2000px column of disconnected boxes — a list pretending to be a graph.
    They are listed as text beside it instead, which is what a reader wants and
    what keeps the actual DAG legible at default zoom.
    """
    edges = overview_edges(epics)
    connected = {e for pair in edges for e in pair}
    drawn = [e for e in epics if e["id"] in connected]

    lines = ["```mermaid", "graph LR"]
    classed: dict[str, list[str]] = {k: [] for k in STATUS_CLASSDEF}
    for epic in drawn:
        eid = node_id(epic["id"])
        classed[status_of(epic)].append(eid)
        lines.append(f"  {eid}{node_box(truncate(epic['title'], 40))}")

    lines.append("")
    for src, dst in edges:
        lines.append(f"  {node_id(src)} --> {node_id(dst)}")

    lines.append("")
    _emit_classes(lines, classed)
    lines.append("```")
    return "\n".join(lines), [e for e in epics if e["id"] not in connected]


def render_standalone_epics(epics: list[dict]) -> str:
    """One line per dependency-free epic, grouped by status — the epics the
    overview graph omits because they have no edges to show."""
    if not epics:
        return ""
    order = {s: i for i, s in enumerate(STATUS_ORDER)}
    rows = []
    for epic in sorted(epics, key=lambda e: (order.get(e.get("status", "todo"), 99),
                                             e.get("priority", "P9"))):
        st = status_of(epic)
        prio = epic.get("priority")
        meta = " · ".join(x for x in (STATUS_LABEL.get(epic.get("status", "todo")), prio,
                                      "parked" if epic.get("parked") else None) if x)
        rows.append(f"- {STATUS_EMOJI[st]} **{epic['title']}** — {meta}")
    return "\n".join(rows)


def render_epic_mini_mermaid(epic: dict) -> str:
    """Per-epic task DAG: the epic's tasks + their INTERNAL edges only. Empty
    string when the epic has no tasks (nothing to draw)."""
    tasks = epic.get("tasks") or []
    if not tasks:
        return ""
    task_ids = {t["id"] for t in tasks}
    lines = ["```mermaid", "graph LR"]
    classed: dict[str, list[str]] = {k: [] for k in STATUS_CLASSDEF}
    for t in tasks:
        tid = node_id(t["id"])
        classed[status_of(t)].append(tid)
        label = truncate(t["title"], 46)
        if t.get("mcp"):
            label = f"{label}<br/>{t['mcp']}"
        lines.append(f"  {tid}{node_box(label)}")
    lines.append("")
    for t in tasks:
        for dep in t.get("depends_on") or []:
            if dep in task_ids:  # internal edges only; cross-epic edges live in the overview
                lines.append(f"  {node_id(dep)} --> {node_id(t['id'])}")
    lines.append("")
    _emit_classes(lines, classed)
    lines.append("```")
    return "\n".join(lines)


def render_epic_task_table(epic: dict) -> str:
    """Compact per-epic task table (status + tracker/PR links). Empty string
    when the epic has no tasks."""
    tasks = epic.get("tasks") or []
    if not tasks:
        return ""
    rows = ["| Task | Status | Refs |", "| --- | --- | --- |"]
    for t in tasks:
        st = t.get("status", "todo")
        cell = f"{STATUS_EMOJI.get(status_of(t), '')} {STATUS_LABEL.get(st, st)}"
        refs = []
        if t.get("mcp"):
            refs.append(f"`{t['mcp']}`")
        pr = fmt_pr(t.get("pr"))
        if pr:
            refs.append(pr)
        title = t["title"].replace("|", "\\|")
        rows.append(f"| {title} | {cell} | {' '.join(refs) or '—'} |")
    return "\n".join(rows)


def render_epic_details(epics: list[dict]) -> str:
    """One collapsible <details> per epic (active status first), each holding
    the epic note, spec/PR links, its task DAG and a task table."""
    order = {s: i for i, s in enumerate(STATUS_ORDER)}

    def sort_key(e):
        return (order.get(e.get("status", "todo"), 99),
                1 if e.get("parked") else 0,
                e.get("priority", "P9"))

    out: list[str] = []
    for epic in sorted(epics, key=sort_key):
        st = epic.get("status", "todo")
        label = STATUS_LABEL.get(st, st)
        if epic.get("parked"):
            label += " · parked"
        head = [label]
        if epic.get("priority"):
            head.append(epic["priority"])
        if epic.get("mcp"):
            head.append(epic["mcp"])
        summary = f"{STATUS_EMOJI.get(status_of(epic), '')} {epic['title']} — {' · '.join(head)}"

        out.append("<details>")
        out.append(f"<summary>{summary}</summary>")
        out.append("")  # blank line: GitHub needs it to render markdown inside <details>
        if epic.get("note"):
            out.append(f"> {epic['note']}")
            out.append("")
        links = []
        if epic.get("spec"):
            spec = epic["spec"]
            links.append(f"Spec: [{os.path.basename(spec)}](./{spec}/)")
        pr = fmt_pr(epic.get("pr"))
        if pr:
            links.append(f"PR: {pr}")
        if links:
            out.append(" · ".join(links))
            out.append("")
        mini = render_epic_mini_mermaid(epic)
        if mini:
            out.append(mini)
            out.append("")
        table = render_epic_task_table(epic)
        if table:
            out.append(table)
            out.append("")
        out.append("</details>")
        out.append("")
    return "\n".join(out).rstrip("\n")


def render_status_table(epics: list[dict]) -> str:
    rows = ["| Epic | Status | Priority | Progress | Spec | PR |",
            "| --- | --- | --- | --- | --- | --- |"]
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
            f"| {epic_cell} | {st} | {epic.get('priority', '')} | "
            f"{progress or '—'} | {spec_cell} | {pr} |"
        )
    return "\n".join(rows)


def load_archive() -> dict:
    """Read roadmap.archive.yaml; an absent file is an empty archive."""
    if not os.path.isfile(ROADMAP_ARCHIVE_YAML):
        return {"version": 1, "epics": []}
    with open(ROADMAP_ARCHIVE_YAML, encoding="utf-8") as fh:
        data = yaml.safe_load(fh) or {}
    data.setdefault("epics", [])
    if data["epics"] is None:
        data["epics"] = []
    return data


def render_archive_table(archive: dict) -> str:
    """Compact provenance table for swept epics: what shipped, when, in which PRs."""
    epics = archive.get("epics") or []
    if not epics:
        return "_Nothing archived yet._"
    rows = ["| Epic | Shipped | Archived | PRs |", "| --- | --- | --- | --- |"]
    for epic in sorted(epics, key=lambda e: str(e.get("shipped_on", "")), reverse=True):
        prs = " ".join(
            sorted({f"#{n}" for n in parse_pr_refs(epic.get("pr"))}
                   | {f"#{n}" for t in epic.get("tasks") or []
                      for n in parse_pr_refs(t.get("pr"))},
                   key=lambda s: int(s[1:]))
        )
        mcp = epic.get("mcp")
        title = epic["title"] + (f" `{mcp}`" if mcp else "")
        rows.append(
            f"| {title} | {epic.get('shipped_on', '—')} | "
            f"{epic.get('archived_on', '—')} | {prs or '—'} |"
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


def render(data: dict, archive: dict | None = None) -> str:
    epics = data.get("epics", [])
    archive = archive if archive is not None else {"epics": []}
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
               "execution `status`, `priority`, and links — the things a "
               "per-spec `tasks.md` checkbox list cannot express. Per-spec checkbox "
               "progress is recomputed live from each `specs/<NNN>/tasks.md`.")
    out.append("")
    out.append("[`roadmap.yaml`](./roadmap.yaml) holds the **working set** (todo · in-progress · "
               "in-review · blocked · parked). Cold shipped epics are swept into "
               "[`roadmap.archive.yaml`](./roadmap.archive.yaml) and surface in the "
               "[Shipped](#shipped-archived) table below, so the working file stays small "
               "while provenance survives. A `depends_on:` edge into the archive is "
               "satisfied by definition.")
    out.append("")
    out.append("## How to regenerate")
    out.append("")
    out.append("```bash")
    out.append("python3 scripts/gen-roadmap.py     # writes ROADMAP.md")
    out.append("scripts/gen-roadmap                # convenience wrapper (same thing)")
    out.append("python3 scripts/gen-roadmap.py --check          # CI canary: fail if ROADMAP.md is stale")
    out.append("python3 scripts/gen-roadmap.py --check-github   # cross-check statuses vs live GitHub PR state,")
    out.append("                                                # spec links, depends_on ids, and status sanity")
    out.append("                                                # (add --strict to fail on warnings; needs gh)")
    out.append("python3 scripts/gen-roadmap.py --archive --dry-run   # preview the cold-done sweep")
    out.append("python3 scripts/gen-roadmap.py --archive             # sweep into roadmap.archive.yaml + regenerate")
    out.append("```")
    out.append("")
    out.append("## roadmap.yaml schema (short form)")
    out.append("")
    out.append("- **epics[]** — each has `id` (stable slug, DAG node), `title`, "
               "`status` (todo·in_progress·in_review·blocked·done), "
               "`priority` (P0–P3), `depends_on: [ids]` (DAG edges, prerequisite→dependent), "
               "optional `parked: true`, and links `spec:` / `pr:` / `mcp:` (external MCP-xxxx).")
    out.append("- **epics[].tasks[]** — child tasks with the same fields; their "
               "`depends_on` may reference sibling tasks or other epics.")
    out.append("- See the header comment in `roadmap.yaml` for the full field reference.")
    out.append("")
    out.append("## Roadmap at a glance")
    out.append("")
    out.append("The cross-epic dependency graph — **one node per epic**, edges point "
               "prerequisite → dependent. Task-level detail lives in the collapsible "
               "sections below, and dependency-free epics are listed under the graph "
               "rather than drawn as disconnected boxes, so this stays legible at "
               "default zoom. Node colour = status: "
               "🟢 done · 🔵 in-progress · 🟡 in-review · 🔴 blocked · ⚪ todo · ⚫ parked.")
    out.append("")
    overview, standalone = render_overview_mermaid(epics)
    out.append(overview)
    if standalone:
        out.append("")
        out.append(f"**Independent epics** ({len(standalone)}) — no cross-epic "
                   f"prerequisites; each stands alone:")
        out.append("")
        out.append(render_standalone_epics(standalone))
    out.append("")
    out.append("## Epic details")
    out.append("")
    out.append("Each epic's child tasks, their internal dependency graph, and "
               "tracker/PR links — **collapsed by default**, expand the ones you "
               "care about. Full metadata (priority, spec progress) is in "
               "the [Epics](#epics) table below.")
    out.append("")
    out.append(render_epic_details(epics))
    out.append("")
    out.append("## Epics")
    out.append("")
    out.append(render_status_table(epics))
    out.append("")
    out.append("## Shipped (archived)")
    out.append("")
    out.append("Swept out of the working set by `--archive` once done, merged and cooled off. "
               "Full entries — notes, child tasks, PR refs — live in "
               "[`roadmap.archive.yaml`](./roadmap.archive.yaml).")
    out.append("")
    out.append(render_archive_table(archive))
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


# ── GitHub / ground-truth cross-check (--check-github) ──────────────────────
class Finding:
    """One report line: an ERROR or WARN against a roadmap item."""
    __slots__ = ("level", "ref", "reason")

    def __init__(self, level: str, ref: str, reason: str):
        self.level = level  # "ERROR" | "WARN"
        self.ref = ref
        self.reason = reason


def iter_items(data: dict):
    """Yield metadata for every epic and task, in file order.

    Each dict: item (raw), id, kind ('epic'|'task'), epic_id (owning epic),
    has_children (bool). A task's owning epic id lets us attribute a task's
    spec: link back to its epic for double-count detection.
    """
    for epic in data.get("epics", []):
        children = epic.get("tasks") or []
        yield {"item": epic, "id": epic["id"], "kind": "epic",
               "epic_id": epic["id"], "has_children": bool(children)}
        for t in children:
            yield {"item": t, "id": t["id"], "kind": "task",
                   "epic_id": epic["id"], "has_children": False}


def ref_label(meta: dict) -> str:
    if meta["kind"] == "epic":
        return f"{meta['id']} (epic)"
    return f"{meta['id']} (task · epic {meta['epic_id']})"


def parse_pr_refs(pr) -> list[int]:
    """Extract PR numbers from a pr: field ("#786", full URL, or a list)."""
    if not pr:
        return []
    refs = pr if isinstance(pr, list) else [pr]
    nums: list[int] = []
    for r in refs:
        for m in PR_NUM_RE.finditer(str(r)):
            n = int(m.group(1))
            if n not in nums:
                nums.append(n)
    return nums


def gh_available() -> tuple[bool, str]:
    """(ok, reason). ok=False means skip the live PR cross-check (exit 2)."""
    if not shutil.which("gh"):
        return False, "`gh` CLI not found on PATH"
    try:
        r = subprocess.run(["gh", "auth", "status"],
                           capture_output=True, text=True)
    except OSError as e:  # pragma: no cover
        return False, f"could not execute `gh`: {e}"
    if r.returncode != 0:
        return False, "`gh` is not authenticated (`gh auth status` failed)"
    return True, ""


def gh_pr_state(number: int, repo: str, cache: dict) -> dict:
    """Return {'state','mergedAt'} for a PR, or {'error': msg}. Cached per number."""
    if number in cache:
        return cache[number]
    r = subprocess.run(
        ["gh", "pr", "view", str(number), "--repo", repo,
         "--json", "state,mergedAt"],
        capture_output=True, text=True)
    if r.returncode != 0:
        cache[number] = {"error": (r.stderr.strip().splitlines() or ["not found"])[-1]}
    else:
        try:
            cache[number] = json.loads(r.stdout)
        except json.JSONDecodeError:
            cache[number] = {"error": "unparseable `gh` JSON output"}
    return cache[number]


def check_pr_status(items: list[dict], repo: str, cache: dict) -> list[Finding]:
    """Cross-check every pr: link against live GitHub state.

    MERGED but not done → ERROR; CLOSED (unmerged) but in_progress/in_review →
    ERROR; OPEN but done → ERROR; OPEN but todo → WARN; unresolvable ref → ERROR.
    """
    out: list[Finding] = []
    for meta in items:
        status = meta["item"].get("status", "todo")
        for num in parse_pr_refs(meta["item"].get("pr")):
            st = gh_pr_state(num, repo, cache)
            if "error" in st:
                out.append(Finding("ERROR", ref_label(meta),
                    f"PR #{num} could not be resolved on GitHub "
                    f"({st['error']}) — dangling pr: link."))
                continue
            state = st.get("state")               # OPEN | CLOSED | MERGED
            if state == "MERGED":
                if status != "done":
                    out.append(Finding("ERROR", ref_label(meta),
                        f"PR #{num} is MERGED but status is '{status}' "
                        f"(expected 'done')."))
            elif state == "CLOSED":
                if status in ("in_progress", "in_review"):
                    out.append(Finding("ERROR", ref_label(meta),
                        f"PR #{num} is CLOSED (unmerged) but status is "
                        f"'{status}'."))
            elif state == "OPEN":
                if status == "done":
                    out.append(Finding("ERROR", ref_label(meta),
                        f"PR #{num} is OPEN but status is 'done'."))
                elif status == "todo":
                    out.append(Finding("WARN", ref_label(meta),
                        f"PR #{num} is OPEN (work started) but status is "
                        f"still 'todo'."))
    return out


def check_spec_links(items: list[dict]) -> list[Finding]:
    """Every spec: must resolve to a real specs/<NNN> dir (ERROR if not).
    A spec shared by two different epics double-counts its badge (WARN)."""
    out: list[Finding] = []
    spec_to_epics: dict[str, set] = {}
    for meta in items:
        spec = meta["item"].get("spec")
        if not spec:
            continue
        if not os.path.isdir(os.path.join(REPO_ROOT, spec)):
            out.append(Finding("ERROR", ref_label(meta),
                f"spec: '{spec}' does not resolve to a directory under specs/."))
        # Attribute to the owning epic so an epic sharing a spec with its OWN
        # child task is not flagged — only genuinely distinct epics are.
        spec_to_epics.setdefault(spec, set()).add(meta["epic_id"])
    for spec, epics in sorted(spec_to_epics.items()):
        if len(epics) > 1:
            out.append(Finding("WARN", f"spec {spec}",
                f"shared by {len(epics)} distinct epics "
                f"({', '.join(sorted(epics))}) — the Epics-table progress "
                f"badge double-counts this spec."))
    return out


def check_dangling_deps(items: list[dict], archive: dict) -> list[Finding]:
    """Every depends_on target must name a known id.

    Known == an epic/task in roadmap.yaml, or an epic/task in the archive (an
    edge into the archive is a satisfied prerequisite, not a dangling one).
    Anything else is a typo or a renamed id that silently broke the DAG.
    """
    out: list[Finding] = []
    known = {m["id"] for m in items} | all_ids(archive.get("epics") or [])
    for meta in items:
        for dep in meta["item"].get("depends_on") or []:
            if dep not in known:
                out.append(Finding("ERROR", ref_label(meta),
                    f"depends_on: '{dep}' matches no epic or task id in "
                    f"roadmap.yaml or roadmap.archive.yaml."))
    return out


def check_status_sanity(items: list[dict]) -> list[Finding]:
    """Reviews/in-flight work should have PR evidence; done epics should have
    all children done.

    in_review with no pr: → WARN for any item (an in-review claim with no PR
    anywhere is exactly the drift this audit found). in_progress with no pr: →
    WARN only for leaf items, since an umbrella epic legitimately delegates its
    PRs to child tasks.
    """
    out: list[Finding] = []
    for meta in items:
        item = meta["item"]
        status = item.get("status", "todo")
        has_pr = bool(parse_pr_refs(item.get("pr")))
        if not has_pr:
            if status == "in_review":
                out.append(Finding("WARN", ref_label(meta),
                    "status 'in_review' but no pr: link — an in-review item "
                    "should link its PR as evidence."))
            elif status == "in_progress" and not meta["has_children"]:
                out.append(Finding("WARN", ref_label(meta),
                    "status 'in_progress' but no pr: link and no child tasks "
                    "— nothing links the in-flight work."))
        if meta["kind"] == "epic" and status == "done":
            for t in item.get("tasks") or []:
                if t.get("status") != "done":
                    out.append(Finding("WARN", ref_label(meta),
                        f"epic is 'done' but child task '{t['id']}' is "
                        f"'{t.get('status', 'todo')}'."))
    return out


def print_report(findings: list[Finding], strict: bool) -> int:
    errors = [f for f in findings if f.level == "ERROR"]
    warnings = [f for f in findings if f.level == "WARN"]

    print(f"roadmap.yaml ground-truth cross-check (repo {REPO_SLUG})\n")

    def emit(group: list[Finding], head: str):
        print(f"{head} ({len(group)}):")
        if not group:
            print("  none")
        for f in group:
            print(f"  [{f.level:<5}] {f.ref}")
            print(f"          {f.reason}")
        print()

    emit(errors, "ERRORS")
    emit(warnings, "WARNINGS")

    print(f"Summary: {len(errors)} error(s), {len(warnings)} warning(s).")
    if strict and warnings:
        print("(--strict: warnings count as errors for the exit code.)")
    if errors or (strict and warnings):
        return 1
    return 0


def check_github(data: dict, strict: bool) -> int:
    items = list(iter_items(data))
    findings: list[Finding] = []

    ok, reason = gh_available()
    if ok:
        cache: dict = {}
        findings += check_pr_status(items, REPO_SLUG, cache)

    # spec + status + DAG checks are offline and always run.
    findings += check_spec_links(items)
    findings += check_dangling_deps(items, load_archive())
    findings += check_status_sanity(items)

    if not ok:
        print_report(findings, strict)
        sys.stderr.write(
            f"\nerror: PR cross-check skipped — {reason}. "
            "Install and authenticate `gh` (`gh auth login`) to validate PR "
            "state; offline spec/status checks above still ran.\n")
        return 2

    return print_report(findings, strict)


# ── archive sweep (--archive) ───────────────────────────────────────────────
# An epic block starts at indent 2 ("  - id: slug"); child tasks sit at indent 6
# and so never match. Section banners ("  # ── DONE ──") separate groups.
EPIC_START_RE = re.compile(r"^  - id: (\S+)\s*$")
SECTION_HDR_RE = re.compile(r"^  # ─")


def epic_pr_numbers(epic: dict) -> list[int]:
    """Every PR referenced by an epic or any of its child tasks."""
    nums = list(parse_pr_refs(epic.get("pr")))
    for t in epic.get("tasks") or []:
        for n in parse_pr_refs(t.get("pr")):
            if n not in nums:
                nums.append(n)
    return nums


def _parse_merged_at(value: str) -> _dt.datetime | None:
    if not value:
        return None
    try:
        return _dt.datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ").replace(
            tzinfo=_dt.timezone.utc)
    except ValueError:  # pragma: no cover
        return None


def epic_shipped_on(epic: dict, cache: dict, use_gh: bool) -> tuple[_dt.datetime | None, str]:
    """(newest merge datetime, blocker). blocker is '' when the epic is datable
    and every referenced PR is merged; otherwise it explains why not."""
    nums = epic_pr_numbers(epic)
    if not nums:
        return (None, "no PR refs to date it")
    if not use_gh:
        return (None, "")
    newest: _dt.datetime | None = None
    for n in nums:
        st = gh_pr_state(n, REPO_SLUG, cache)
        if "error" in st:
            return (None, f"PR #{n} unresolvable ({st['error']})")
        if st.get("state") != "MERGED":
            return (None, f"PR #{n} is {st.get('state')}, not MERGED")
        merged = _parse_merged_at(st.get("mergedAt") or "")
        if merged is None:
            return (None, f"PR #{n} has no mergedAt")
        if newest is None or merged > newest:
            newest = merged
    return (newest, "")


def find_epic_blocks(text: str) -> list[tuple[str, int, int]]:
    """[(epic_id, start_line, end_line_exclusive)] over the raw roadmap text.

    A block's end is trimmed back past trailing blank lines and section banners,
    which belong to the NEXT group rather than to the epic being cut.
    """
    lines = text.splitlines(keepends=True)
    starts = [(m.group(1), i) for i, l in enumerate(lines)
              if (m := EPIC_START_RE.match(l))]
    blocks: list[tuple[str, int, int]] = []
    for idx, (eid, start) in enumerate(starts):
        end = starts[idx + 1][1] if idx + 1 < len(starts) else len(lines)
        while end - 1 > start and (not lines[end - 1].strip()
                                   or SECTION_HDR_RE.match(lines[end - 1])):
            end -= 1
        blocks.append((eid, start, end))
    return blocks


def prune_empty_sections(lines: list[str]) -> list[str]:
    """Drop a section banner that no longer has any epic under it, and collapse
    the blank-line runs the excision leaves behind."""
    hdrs = [i for i, l in enumerate(lines) if SECTION_HDR_RE.match(l)]
    drop: set[int] = set()
    for pos, i in enumerate(hdrs):
        nxt = hdrs[pos + 1] if pos + 1 < len(hdrs) else len(lines)
        if not any(EPIC_START_RE.match(l) for l in lines[i + 1:nxt]):
            drop.add(i)
            j = i + 1
            while j < nxt and not lines[j].strip():
                drop.add(j)
                j += 1
    kept = [l for i, l in enumerate(lines) if i not in drop]

    out: list[str] = []
    for line in kept:
        blank = not line.strip()
        # Collapse blank runs, and never leave a blank between a surviving
        # section banner and the first epic left under it.
        if blank and out and (not out[-1].strip() or SECTION_HDR_RE.match(out[-1])):
            continue
        out.append(line)
    while out and not out[-1].strip():
        out.pop()
    if out and not out[-1].endswith("\n"):
        out[-1] += "\n"
    return out


def stamp_block(block: str, shipped_on: str, archived_on: str) -> str:
    """Inject shipped_on/archived_on right after the epic's `- id:` line."""
    lines = block.splitlines(keepends=True)
    if any(l.startswith("    archived_on:") for l in lines):
        return block  # already stamped; never double-stamp
    head, rest = lines[0], lines[1:]
    stamp = [f"    shipped_on: {shipped_on}\n", f"    archived_on: {archived_on}\n"]
    return "".join([head] + stamp + rest)


def sweep_archive(min_age_days: int, dry_run: bool) -> int:
    with open(ROADMAP_YAML, encoding="utf-8") as fh:
        raw = fh.read()
    data = yaml.safe_load(raw)
    epics = data.get("epics", [])

    use_gh, gh_reason = gh_available()
    if not use_gh and min_age_days > 0:
        sys.stderr.write(
            f"error: --archive needs an authenticated `gh` to date merges "
            f"({gh_reason}). Re-run with --min-age-days 0 to sweep without "
            f"the cool-off check.\n")
        return 2

    today = _dt.datetime.now(_dt.timezone.utc)
    cache: dict = {}
    chosen: list[tuple[dict, str]] = []   # (epic, shipped_on)
    skipped: list[tuple[str, str]] = []   # (id, reason)

    for epic in epics:
        eid = epic["id"]
        if epic.get("status") != "done":
            continue
        if epic.get("keep"):
            skipped.append((eid, "keep: true"))
            continue
        undone = [t["id"] for t in epic.get("tasks") or [] if t.get("status") != "done"]
        if undone:
            skipped.append((eid, f"child task(s) not done: {', '.join(undone)}"))
            continue
        newest, blocker = epic_shipped_on(epic, cache, use_gh)
        if blocker:
            skipped.append((eid, blocker))
            continue
        if newest is None:                       # min-age 0, gh unavailable
            chosen.append((epic, "unknown"))
            continue
        age = (today - newest).days
        if age < min_age_days:
            skipped.append((eid, f"merged {age}d ago, cool-off is {min_age_days}d"))
            continue
        chosen.append((epic, newest.date().isoformat()))

    print(f"archive sweep (cool-off {min_age_days}d, "
          f"{'dry run' if dry_run else 'writing'})\n")
    if skipped:
        print("kept in roadmap.yaml:")
        for eid, why in skipped:
            print(f"  · {eid}: {why}")
        print()
    if not chosen:
        print("nothing to archive.")
        return 0
    print("moving to roadmap.archive.yaml:")
    for epic, shipped in chosen:
        prs = " ".join(f"#{n}" for n in epic_pr_numbers(epic))
        print(f"  → {epic['id']} (shipped {shipped}) {prs}")
    print()
    if dry_run:
        print("dry run: no files changed.")
        return 0

    # Excise raw text blocks (comments and formatting survive verbatim).
    chosen_ids = {e["id"] for e, _ in chosen}
    lines = raw.splitlines(keepends=True)
    blocks = {eid: "".join(lines[s:e]) for eid, s, e in find_epic_blocks(raw)
              if eid in chosen_ids}
    missing = chosen_ids - set(blocks)
    if missing:
        sys.stderr.write(f"error: could not locate raw block(s) for {sorted(missing)}; "
                         "aborting without writing.\n")
        return 1

    cut = {i for eid, s, e in find_epic_blocks(raw) if eid in chosen_ids
           for i in range(s, e)}
    new_raw = "".join(prune_empty_sections([l for i, l in enumerate(lines) if i not in cut]))

    # Safety: the surviving text must still parse, and must have lost exactly
    # the epics we meant to move.
    reparsed = yaml.safe_load(new_raw)
    got = {e["id"] for e in reparsed.get("epics", [])}
    want = {e["id"] for e in epics} - chosen_ids
    if got != want:
        sys.stderr.write(f"error: post-sweep roadmap.yaml would contain {sorted(got)}, "
                         f"expected {sorted(want)}; aborting without writing.\n")
        return 1

    archived_on = today.date().isoformat()
    archive_text = ""
    if os.path.isfile(ROADMAP_ARCHIVE_YAML):
        with open(ROADMAP_ARCHIVE_YAML, encoding="utf-8") as fh:
            archive_text = fh.read().rstrip("\n") + "\n"
    else:
        archive_text = ARCHIVE_HEADER + "\nversion: 1\n\nepics:\n"
    for epic, shipped in chosen:
        sep = "" if archive_text.endswith("epics:\n") else "\n"
        archive_text += sep + stamp_block(blocks[epic["id"]], shipped, archived_on)

    parsed_archive = yaml.safe_load(archive_text)
    if len({e["id"] for e in parsed_archive.get("epics") or []}) != \
            len(parsed_archive.get("epics") or []):
        sys.stderr.write("error: archive would contain duplicate epic ids; aborting.\n")
        return 1

    with open(ROADMAP_YAML, "w", encoding="utf-8") as fh:
        fh.write(new_raw)
    with open(ROADMAP_ARCHIVE_YAML, "w", encoding="utf-8") as fh:
        fh.write(archive_text)

    with open(ROADMAP_MD, "w", encoding="utf-8") as fh:
        fh.write(render(reparsed, load_archive()))

    print(f"archived {len(chosen)} epic(s); roadmap.yaml now has "
          f"{len(reparsed.get('epics', []))}. Regenerated ROADMAP.md.")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser(description="Generate ROADMAP.md from roadmap.yaml")
    ap.add_argument("--check", action="store_true",
                    help="exit non-zero if ROADMAP.md is stale (do not write)")
    ap.add_argument("--check-github", action="store_true",
                    help="cross-check roadmap.yaml vs live GitHub PR state, "
                         "spec links, and status sanity (does not write)")
    ap.add_argument("--strict", action="store_true",
                    help="with --check-github, promote warnings to errors "
                         "for the exit code")
    ap.add_argument("--archive", action="store_true",
                    help="sweep cold done epics into roadmap.archive.yaml and "
                         "regenerate ROADMAP.md")
    ap.add_argument("--min-age-days", type=int, default=DEFAULT_MIN_AGE_DAYS,
                    metavar="N",
                    help=f"cool-off before a done epic is archived, dated from its "
                         f"newest merged PR (default {DEFAULT_MIN_AGE_DAYS}; 0 sweeps "
                         f"every done epic and needs no gh)")
    ap.add_argument("--dry-run", action="store_true",
                    help="with --archive, report the sweep and change nothing")
    args = ap.parse_args()

    if args.archive:
        return sweep_archive(args.min_age_days, args.dry_run)

    with open(ROADMAP_YAML, encoding="utf-8") as fh:
        data = yaml.safe_load(fh)

    if args.check_github:
        return check_github(data, args.strict)

    rendered = render(data, load_archive())

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
    archived = len(load_archive().get("epics") or [])
    print(f"wrote {os.path.relpath(ROADMAP_MD, REPO_ROOT)} "
          f"({len(data.get('epics', []))} epics"
          + (f", {archived} archived)" if archived else ")"))
    return 0


if __name__ == "__main__":
    sys.exit(main())
