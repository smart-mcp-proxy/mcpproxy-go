#!/usr/bin/env python3
"""Web <-> native settings catalog parity canary.

The native macOS tray Settings form (native/macos/.../Settings/SettingsCatalog.swift)
is a HAND-PORT of the Web UI catalog (frontend/src/views/settings/fields.ts).
Because they are two separate implementations, a field can drift in one and not
the other -- exactly how spec-074's duration fields shipped with a hardcoded
"2m" placeholder on macOS while the web form had the correct 30s / 5m
(see MCP-1214).

This canary pins parity for *duration* fields (the demonstrated risk class):
for every duration field it compares `optional` and `placeholder` across the
two files and fails loudly on any mismatch -- or if a duration field is missing
its placeholder on either side. Run in CI as a required merge check.

It is intentionally line-oriented and conservative: each duration field is
declared on a single line in both catalogs today. If that format changes, the
parser will stop finding attributes and FAIL, forcing this script to be updated
in lockstep rather than silently passing.
"""
from __future__ import annotations
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
TS = ROOT / "frontend/src/views/settings/fields.ts"
SWIFT = ROOT / "native/macos/MCPProxy/MCPProxy/Settings/SettingsCatalog.swift"


def parse_ts(text: str) -> dict[str, dict]:
    """Extract {key: {optional, placeholder}} for `control: 'duration'` fields."""
    out: dict[str, dict] = {}
    for line in text.splitlines():
        if "control: 'duration'" not in line and 'control: "duration"' not in line:
            continue
        m = re.search(r"key:\s*['\"]([^'\"]+)['\"]", line)
        if not m:
            sys.exit(f"[parity] TS duration field with no parseable key:\n  {line.strip()}")
        key = m.group(1)
        ph = re.search(r"placeholder:\s*['\"]([^'\"]*)['\"]", line)
        opt = re.search(r"optional:\s*(true|false)", line)
        out[key] = {
            "placeholder": ph.group(1) if ph else None,
            "optional": (opt.group(1) == "true") if opt else False,
        }
    return out


def _swift_configfield_blocks(text: str) -> list[str]:
    """Yield the argument text of each `ConfigField(...)` call, paren-balanced
    and aware of Swift double-quoted string literals (so parens/commas inside
    help strings don't break the scan). Handles single- and multi-line fields."""
    blocks: list[str] = []
    marker = "ConfigField("
    i = 0
    while True:
        start = text.find(marker, i)
        if start == -1:
            break
        j = start + len(marker)
        depth = 1
        in_str = False
        buf: list[str] = []
        while j < len(text) and depth > 0:
            c = text[j]
            if in_str:
                if c == "\\":
                    buf.append(c)
                    j += 1
                    if j < len(text):
                        buf.append(text[j])
                    j += 1
                    continue
                if c == '"':
                    in_str = False
                buf.append(c)
            else:
                if c == '"':
                    in_str = True
                    buf.append(c)
                elif c == "(":
                    depth += 1
                    buf.append(c)
                elif c == ")":
                    depth -= 1
                    if depth == 0:
                        break
                    buf.append(c)
                else:
                    buf.append(c)
            j += 1
        blocks.append("".join(buf))
        i = j
    return blocks


def parse_swift(text: str) -> dict[str, dict]:
    """Extract {key: {optional, placeholder}} for `control: .duration` fields."""
    out: dict[str, dict] = {}
    for block in _swift_configfield_blocks(text):
        if "control: .duration" not in block:
            continue
        m = re.search(r'key:\s*"([^"]+)"', block)
        if not m:
            sys.exit(f"[parity] Swift duration ConfigField with no parseable key:\n  {block.strip()[:120]}")
        key = m.group(1)
        ph = re.search(r'placeholder:\s*"([^"]*)"', block)
        opt = re.search(r"optional:\s*(true|false)", block)
        out[key] = {
            "placeholder": ph.group(1) if ph else None,
            "optional": (opt.group(1) == "true") if opt else False,
        }
    return out


def main() -> int:
    web = parse_ts(TS.read_text())
    native = parse_swift(SWIFT.read_text())

    errors: list[str] = []

    if not web or not native:
        errors.append(f"parsed 0 duration fields (web={len(web)}, native={len(native)}) "
                      "-- catalog format may have changed; update this script")

    only_web = sorted(set(web) - set(native))
    only_native = sorted(set(native) - set(web))
    if only_web:
        errors.append(f"duration field(s) only in web fields.ts: {only_web}")
    if only_native:
        errors.append(f"duration field(s) only in native SettingsCatalog.swift: {only_native}")

    for key in sorted(set(web) & set(native)):
        w, n = web[key], native[key]
        if w["placeholder"] is None:
            errors.append(f"{key}: web has no placeholder (must show the real default, e.g. 30s)")
        if n["placeholder"] is None:
            errors.append(f"{key}: native has no placeholder (must show the real default, e.g. 30s)")
        if w["placeholder"] != n["placeholder"]:
            errors.append(f"{key}: placeholder mismatch web={w['placeholder']!r} native={n['placeholder']!r}")
        if w["optional"] != n["optional"]:
            errors.append(f"{key}: optional mismatch web={w['optional']} native={n['optional']}")

    if errors:
        print("Settings parity check FAILED:", file=sys.stderr)
        for e in errors:
            print(f"  - {e}", file=sys.stderr)
        return 1

    print(f"Settings parity OK: {len(web)} duration field(s) consistent across web + native:")
    for key in sorted(web):
        print(f"  - {key}: placeholder={web[key]['placeholder']!r} optional={web[key]['optional']}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
