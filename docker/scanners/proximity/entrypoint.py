#!/usr/bin/env python3
"""Minimal NOVA-inspired keyword scanner used by the Nova Proximity image.

Scans every file under /scan/source for a small set of suspicious keyword
patterns that commonly appear in malicious or compromised MCP servers and
writes a SARIF 2.1 report to /scan/report/results.sarif.

This is intentionally offline and dependency-free so the scanner runs in
restricted environments without any API calls.
"""
from __future__ import annotations

import json
import os
import re
import sys
from pathlib import Path

SOURCE = Path("/scan/source")
REPORT = Path("/scan/report/results.sarif")

# (rule_id, regex, severity, message)
RULES: list[tuple[str, re.Pattern[str], str, str]] = [
    (
        "prompt_injection_hidden_instructions",
        re.compile(r"(?i)ignore (all )?previous instructions|system prompt override|jailbreak"),
        "error",
        "Possible prompt injection: hidden instructions directed at an LLM.",
    ),
    (
        "credential_harvesting",
        re.compile(r"(?i)(exfiltrate|steal|harvest).{0,40}(password|api[_ ]?key|token|secret)"),
        "error",
        "Possible credential harvesting: string pairs 'exfiltrate/steal/harvest' with credentials.",
    ),
    (
        "tool_poisoning_hidden_directive",
        re.compile(r"<!--\s*(assistant|system|tool)\s*:", re.I),
        "warning",
        "HTML comment that looks like a hidden assistant/system directive — possible tool poisoning.",
    ),
    (
        "dangerous_command_exec",
        re.compile(r"\b(curl|wget)\s+[^\s]+\s*\|\s*(sh|bash|python)"),
        "warning",
        "Piping a remote download directly into a shell — classic malicious bootstrap.",
    ),
    (
        "data_exfiltration_webhook",
        re.compile(r"https?://(?:[a-z0-9-]+\.)?(?:ngrok|webhook\.site|requestbin|pipedream|oastify)\.[a-z]+", re.I),
        "warning",
        "Reference to a well-known exfiltration endpoint.",
    ),
]

MAX_FILE_BYTES = 1 * 1024 * 1024  # skip huge binaries
TEXT_EXT = {
    ".py", ".js", ".ts", ".mjs", ".cjs", ".go", ".rs", ".rb", ".php", ".java",
    ".json", ".yaml", ".yml", ".toml", ".md", ".txt", ".sh",
}


def scan_file(path: Path) -> list[dict]:
    try:
        data = path.read_bytes()
    except OSError:
        return []
    if not data:
        return []
    try:
        text = data.decode("utf-8", errors="replace")
    except Exception:
        return []

    findings: list[dict] = []
    for rule_id, regex, level, message in RULES:
        for match in regex.finditer(text):
            line_no = text.count("\n", 0, match.start()) + 1
            findings.append(
                {
                    "ruleId": rule_id,
                    "level": level,
                    "message": {"text": message},
                    "locations": [
                        {
                            "physicalLocation": {
                                "artifactLocation": {
                                    "uri": str(path.relative_to(SOURCE)),
                                },
                                "region": {"startLine": line_no},
                                "contextRegion": {
                                    "snippet": {"text": match.group(0)[:200]},
                                },
                            }
                        }
                    ],
                }
            )
    return findings


def iter_source_files():
    if not SOURCE.exists():
        return
    for root, _dirs, files in os.walk(SOURCE):
        for name in files:
            p = Path(root) / name
            if p.suffix.lower() not in TEXT_EXT:
                continue
            try:
                if p.stat().st_size > MAX_FILE_BYTES:
                    continue
            except OSError:
                continue
            yield p


def main() -> int:
    all_findings: list[dict] = []
    for file in iter_source_files():
        all_findings.extend(scan_file(file))

    sarif = {
        "version": "2.1.0",
        "$schema": "https://raw.githubusercontent.com/oasis-tcs/sarif-spec/master/Schemata/sarif-schema-2.1.0.json",
        "runs": [
            {
                "tool": {
                    "driver": {
                        "name": "nova-proximity",
                        "version": "0.1.0",
                        "informationUri": "https://github.com/fr0gger/proximity",
                        "rules": [
                            {"id": rid, "shortDescription": {"text": msg}, "defaultConfiguration": {"level": level}}
                            for rid, _, level, msg in RULES
                        ],
                    }
                },
                "results": all_findings,
            }
        ],
    }

    REPORT.parent.mkdir(parents=True, exist_ok=True)
    REPORT.write_text(json.dumps(sarif, indent=2))
    # Print a short summary to stdout so execution logs show something useful.
    print(f"nova-proximity: {len(all_findings)} finding(s) across {SOURCE}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
