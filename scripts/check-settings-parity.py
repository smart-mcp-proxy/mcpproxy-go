#!/usr/bin/env python3
"""Web <-> native settings catalog parity gate.

The native macOS tray Settings form (native/macos/.../Settings/SettingsCatalog.swift)
is a HAND-PORT of the Web UI catalog (frontend/src/views/settings/fields.ts).
Because they are two separate implementations, a field can drift in one and not
the other -- exactly how spec-074's duration fields shipped with a hardcoded
"2m" placeholder on macOS while the web form had the correct 30s / 5m
(see MCP-1214).

Originally this canary pinned only *duration* fields. The 2026-08 macOS tray UX
audit (finding F6) found the bigger hole the narrow check could not see: three
non-deprecated Web UI settings -- security.deep_scan.enabled, instructions and
code_execution_max_parallel -- were simply ABSENT from the native catalogue,
while its header claimed to mirror fields.ts 1:1. The tray's Raw tab is
read-only, so those settings were not editable from the tray at all.

So the gate now checks, for every field:

  1. KEY COVERAGE -- the two catalogues describe the same set of config keys
     (minus the documented exclusions below). This is what stops the next
     web-first field from drifting.
  2. ATTRIBUTE PARITY -- control / placeholder / optional / step / min / max /
     restart / select options agree wherever both sides declare the field. A
     control that drifts (number -> toggle) or a select option that disappears
     changes what the tray can SET while leaving key coverage intact.

Both parsers strip comments first: a commented-out ConfigField would otherwise
be counted as editable, which is precisely the drift this gate exists to catch.
A key declared twice on either side is a hard failure -- one definition would
silently win.

It is intentionally line-oriented and conservative. If either catalogue's
format changes so that keys stop being parseable, the script FAILS rather than
silently passing, forcing it to be updated in lockstep.
"""
from __future__ import annotations
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
TS = ROOT / "frontend/src/views/settings/fields.ts"
SWIFT = ROOT / "native/macos/MCPProxy/MCPProxy/Settings/SettingsCatalog.swift"

# Keys that legitimately live on one side only.
#
# server_edition.*  -- the server edition is not shipped in the personal tray,
#                      so the native app has no business editing it.
WEB_ONLY_PREFIXES = ("server_edition.",)
WEB_ONLY_KEYS: set[str] = set()
NATIVE_ONLY_KEYS: set[str] = set()

# Minimum number of fields we expect to parse on each side. A parser that
# silently stops matching would otherwise report perfect parity over zero
# fields.
MIN_FIELDS = 40


def strip_comments(text: str, block_comments: bool) -> str:
    """Blank out `//` (and optionally `/* */`) comments, string-aware.

    Without this the parsers happily read a COMMENTED-OUT field and report it
    as editable — a false negative in a gate whose whole job is to notice a
    field going missing. Characters are replaced with spaces rather than
    deleted so no line/offset shifts."""
    out = list(text)
    i, n = 0, len(text)
    quote: str | None = None
    while i < n:
        c = text[i]
        if quote:
            if c == "\\":
                i += 2
                continue
            if c == quote:
                quote = None
            i += 1
            continue
        if c in "\"'`":
            quote = c
            i += 1
            continue
        if c == "/" and i + 1 < n and text[i + 1] == "/":
            while i < n and text[i] != "\n":
                out[i] = " "
                i += 1
            continue
        if block_comments and c == "/" and i + 1 < n and text[i + 1] == "*":
            while i < n and not (text[i] == "*" and i + 1 < n and text[i + 1] == "/"):
                if text[i] != "\n":
                    out[i] = " "
                i += 1
            for _ in range(2):
                if i < n:
                    out[i] = " "
                    i += 1
            continue
        i += 1
    return "".join(out)


def _ts_options(blob: str) -> list[str] | None:
    """The selectable VALUES of a select/multiselect, in order. Dropping one
    silently narrows what the tray can set, which no key-coverage check sees."""
    m = re.search(r"options:\s*\[(.*?)\]", blob, re.S)
    if not m:
        return None
    return re.findall(r"value:\s*['\"]([^'\"]+)['\"]", m.group(1)) or \
        re.findall(r"['\"]([^'\"]+)['\"]", m.group(1))


def _swift_options(block: str) -> list[str] | None:
    """Swift writes options either as explicit ConfigOption(value:label:) pairs
    or as `["a","b"].map { ConfigOption(value: $0, label: $0) }`."""
    m = re.search(r"options:\s*\[(.*?)\]", block, re.S)
    if not m:
        return None
    inner = m.group(1)
    explicit = re.findall(r'value:\s*"([^"]+)"', inner)
    if explicit:
        return explicit
    return re.findall(r'"([^"]+)"', inner)


def _num(raw: str | None) -> float | None:
    if raw is None:
        return None
    try:
        return float(raw)
    except ValueError:
        sys.exit(f"[parity] unparseable numeric attribute: {raw!r}")


def parse_ts(text: str) -> dict[str, dict]:
    """Extract {key: attrs} for every field object in fields.ts.

    Fields are written either on one line or as a multi-line object literal;
    both start with a `key:` line, so the scan is anchored there and reads
    forward to the end of that object (tracked by brace depth)."""
    out: dict[str, dict] = {}
    text = strip_comments(text, block_comments=True)
    for m in re.finditer(r"\bkey:\s*['\"]([^'\"]+)['\"]", text):
        key = m.group(1)
        if key in out:
            sys.exit(f"[parity] TS declares {key!r} twice — one definition silently wins")
        # The field is the object literal containing this key: walk back to the
        # nearest `{` and forward until the braces balance.
        open_idx = text.rfind("{", 0, m.start())
        if open_idx == -1:
            sys.exit(f"[parity] TS field {key!r} is not inside an object literal")
        depth = 0
        end = len(text)
        for i in range(open_idx, len(text)):
            if text[i] == "{":
                depth += 1
            elif text[i] == "}":
                depth -= 1
                if depth == 0:
                    end = i + 1
                    break
        blob = text[open_idx:end]
        ph = re.search(r"placeholder:\s*['\"]([^'\"]*)['\"]", blob)
        opt = re.search(r"optional:\s*(true|false)", blob)
        control = re.search(r"control:\s*['\"]([^'\"]+)['\"]", blob)
        out[key] = {
            "placeholder": ph.group(1) if ph else None,
            "optional": (opt.group(1) == "true") if opt else False,
            "control": control.group(1) if control else None,
            "step": _num(m2.group(1)) if (m2 := re.search(r"\bstep:\s*([\d.]+)", blob)) else None,
            "min": _num(m3.group(1)) if (m3 := re.search(r"\bmin:\s*(-?[\d.]+)", blob)) else None,
            "max": _num(m4.group(1)) if (m4 := re.search(r"\bmax:\s*(-?[\d.]+)", blob)) else None,
            "restart": bool(re.search(r"\brestart:\s*true", blob)),
            "options": _ts_options(blob),
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


def _swift_placeholder(block: str) -> str | None:
    """Swift placeholders may be written with \\u{...} escapes; decode them so
    they compare equal to the TS literal."""
    m = re.search(r'placeholder:\s*"((?:[^"\\]|\\.)*)"', block)
    if not m:
        return None
    raw = m.group(1)
    return re.sub(r"\\u\{([0-9a-fA-F]+)\}", lambda h: chr(int(h.group(1), 16)), raw)


def parse_swift(text: str) -> dict[str, dict]:
    """Extract {key: attrs} for every ConfigField in SettingsCatalog.swift."""
    out: dict[str, dict] = {}
    text = strip_comments(text, block_comments=True)
    for block in _swift_configfield_blocks(text):
        m = re.search(r'key:\s*"([^"]+)"', block)
        if not m:
            # `ConfigField` also appears as a parameter TYPE (e.g.
            # `validateConfigField(_ field: ConfigField, …)`); those blocks
            # carry no `key:` and are not catalogue entries.
            if re.search(r"^\s*_?\s*\w+\s*:\s*ConfigField", block):
                continue
            sys.exit(f"[parity] Swift ConfigField with no parseable key:\n  {block.strip()[:120]}")
        key = m.group(1)
        if key in out:
            sys.exit(f"[parity] SettingsCatalog declares {key!r} twice — one definition silently wins")
        opt = re.search(r"optional:\s*(true|false)", block)
        control = re.search(r"control:\s*\.(\w+)", block)
        out[key] = {
            "placeholder": _swift_placeholder(block),
            "optional": (opt.group(1) == "true") if opt else False,
            "control": control.group(1) if control else None,
            "step": _num(m2.group(1)) if (m2 := re.search(r"\bstep:\s*([\d.]+)", block)) else None,
            "min": _num(m3.group(1)) if (m3 := re.search(r"\bmin:\s*(-?[\d.]+)", block)) else None,
            "max": _num(m4.group(1)) if (m4 := re.search(r"\bmax:\s*(-?[\d.]+)", block)) else None,
            "restart": bool(re.search(r"\brestart:\s*true", block)),
            "options": _swift_options(block),
        }
    return out


def main() -> int:
    web = parse_ts(TS.read_text())
    native = parse_swift(SWIFT.read_text())

    errors: list[str] = []

    if len(web) < MIN_FIELDS or len(native) < MIN_FIELDS:
        errors.append(
            f"parsed too few fields (web={len(web)}, native={len(native)}, expected >= {MIN_FIELDS}) "
            "-- catalog format may have changed; update this script"
        )

    comparable_web = {
        k for k in web
        if not k.startswith(WEB_ONLY_PREFIXES) and k not in WEB_ONLY_KEYS
    }
    comparable_native = {k for k in native if k not in NATIVE_ONLY_KEYS}

    only_web = sorted(comparable_web - comparable_native)
    only_native = sorted(comparable_native - comparable_web)
    if only_web:
        errors.append(
            "setting(s) in the Web UI but NOT editable in the macOS tray: "
            f"{only_web} -- add them to SettingsCatalog.swift (the tray's Raw tab is read-only, "
            "so a missing field is unreachable there, not merely inconvenient)"
        )
    if only_native:
        errors.append(f"setting(s) only in native SettingsCatalog.swift: {only_native}")

    for key in sorted(comparable_web & comparable_native):
        w, n = web[key], native[key]
        for attr in ("control", "placeholder", "optional", "step", "min", "max", "restart", "options"):
            if w[attr] != n[attr]:
                errors.append(f"{key}: {attr} mismatch web={w[attr]!r} native={n[attr]!r}")
        if w["control"] == "duration":
            if w["placeholder"] is None:
                errors.append(f"{key}: web duration field has no placeholder (must show the real default, e.g. 30s)")
            if n["placeholder"] is None:
                errors.append(f"{key}: native duration field has no placeholder (must show the real default, e.g. 30s)")

    if errors:
        print("Settings parity check FAILED:", file=sys.stderr)
        for e in errors:
            print(f"  - {e}", file=sys.stderr)
        return 1

    shared = comparable_web & comparable_native
    print(f"Settings parity OK: {len(shared)} setting(s) consistent across web + native.")
    excluded = sorted(set(web) - comparable_web)
    if excluded:
        print(f"  (excluded by design: {excluded})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
