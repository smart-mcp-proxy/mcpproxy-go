#!/usr/bin/env python3
"""Build a self-contained HTML verification report for MCP-2101.

Base64-embeds each screenshot so the report opens offline with no external
assets. Run from this directory: `python3 build-report.py`.
"""
import base64
import os

HERE = os.path.dirname(os.path.abspath(__file__))

SCENARIOS = [
    {
        "id": "A",
        "title": "Server quarantined → only the Security-Quarantine banner",
        "img": "01-stateA-security-only.png",
        "expected": "Server has quarantined=true with two non-approved (pending) tools. "
                    "Exactly ONE banner shows — the server-level 'Security Quarantine' — "
                    "and the tool-level 'Tool Quarantine' banner is suppressed entirely "
                    "(never two banners at once).",
        "observed": "Only the 'Security Quarantine' banner renders. No 'Tool Quarantine' "
                    "banner despite both tools being pending. (data-test=tool-quarantine-banner "
                    "asserted count 0; security-quarantine-banner visible.)",
        "pass": True,
    },
    {
        "id": "B",
        "title": "Not quarantined, baseline pending tools → NO tool banner (core fix)",
        "img": "02-stateB-no-tool-banner.png",
        "expected": "Server is Active (not quarantined) with only freshly-pending baseline "
                    "tools and no changed tool. Neither banner shows — a freshly-pending "
                    "baseline tool must not nag the operator. (Pre-fix this showed "
                    "'2 tool(s) require approval'.)",
        "observed": "No banner of either kind. Tools list 'echo'/'get_time' with informational "
                    "'new' badges only. (Both banner data-tests asserted count 0.)",
        "pass": True,
    },
    {
        "id": "C",
        "title": "A changed (rug-pull) tool exists → tool banner appears",
        "img": "03-stateC-tool-banner.png",
        "expected": "Server is Active (not quarantined); two tools flipped to 'changed' "
                    "(rug-pull) plus a residual 'pending' new tool. The 'Tool Quarantine' "
                    "banner appears keyed off the changed status, listing changed + residual "
                    "pending tools with per-tool Approve UI and before/after diffs.",
        "observed": "'Tool Quarantine' banner shows '3 tool(s) require approval'. echo/get_time "
                    "show 'changed' with before/after diffs; steal_data shows 'pending'. "
                    "(tool-quarantine-banner visible; security-quarantine-banner count 0.)",
        "pass": True,
    },
]


def b64(path):
    with open(os.path.join(HERE, path), "rb") as f:
        return base64.b64encode(f.read()).decode("ascii")


def main():
    passed = sum(1 for s in SCENARIOS if s["pass"])
    total = len(SCENARIOS)
    cards = []
    for s in SCENARIOS:
        badge = "PASS" if s["pass"] else "FAIL"
        color = "#16a34a" if s["pass"] else "#dc2626"
        cards.append(f"""
        <details open>
          <summary><span class="badge" style="background:{color}">{badge}</span>
            <strong>State {s['id']}.</strong> {s['title']}</summary>
          <div class="body">
            <p><span class="lbl">Expected</span> {s['expected']}</p>
            <p><span class="lbl">Observed</span> {s['observed']}</p>
            <img src="data:image/png;base64,{b64(s['img'])}" alt="State {s['id']}" />
          </div>
        </details>""")

    html = f"""<!doctype html><html><head><meta charset="utf-8">
<title>MCP-2101 — Tool-Quarantine banner verification</title>
<style>
  body {{ font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
         margin: 0; background:#f8fafc; color:#0f172a; }}
  .wrap {{ max-width: 1100px; margin: 0 auto; padding: 32px 20px 80px; }}
  h1 {{ font-size: 22px; margin: 0 0 4px; }}
  .sub {{ color:#64748b; margin: 0 0 24px; }}
  .summary {{ background:#fff; border:1px solid #e2e8f0; border-radius:12px;
             padding:20px 24px; margin-bottom:24px; box-shadow:0 1px 2px rgba(0,0,0,.04); }}
  .count {{ font-size: 30px; font-weight: 700; color:#16a34a; }}
  details {{ background:#fff; border:1px solid #e2e8f0; border-radius:12px;
            margin-bottom:16px; overflow:hidden; box-shadow:0 1px 2px rgba(0,0,0,.04); }}
  summary {{ cursor:pointer; padding:16px 20px; font-size:15px; list-style:none; }}
  summary::-webkit-details-marker {{ display:none; }}
  .badge {{ color:#fff; font-size:11px; font-weight:700; padding:2px 8px;
           border-radius:999px; margin-right:10px; letter-spacing:.04em; }}
  .body {{ padding: 4px 20px 20px; }}
  .lbl {{ display:inline-block; min-width:78px; font-weight:600; color:#475569;
         font-size:12px; text-transform:uppercase; letter-spacing:.04em; }}
  img {{ width:100%; border:1px solid #e2e8f0; border-radius:8px; margin-top:12px; }}
  p {{ line-height:1.5; }}
  code {{ background:#f1f5f9; padding:1px 5px; border-radius:4px; font-size:13px; }}
</style></head><body><div class="wrap">
  <h1>MCP-2101 — Suppress tool-quarantine banner for baseline pending tools</h1>
  <p class="sub">Spec 032 · parent MCP-2081 · <code>frontend/src/views/ServerDetail.vue</code></p>
  <div class="summary">
    <div class="count">{passed}/{total} scenarios pass</div>
    <p>The Tool-Quarantine banner now keys off <code>status=changed</code> (plus residual
    <code>pending</code> once a change has surfaced) and is suppressed entirely while the
    server-level Security-Quarantine banner is showing. Captured live against a fresh
    mcpproxy + the <code>echo-rugpull</code> test server via Playwright (pinned Chromium 1217).</p>
  </div>
  {''.join(cards)}
</div></body></html>"""

    out = os.path.join(HERE, "report.html")
    with open(out, "w") as f:
        f.write(html)
    print(f"wrote {out} ({passed}/{total} pass)")


if __name__ == "__main__":
    main()
