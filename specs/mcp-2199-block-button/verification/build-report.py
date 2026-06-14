#!/usr/bin/env python3
"""Build a self-contained HTML verification report for MCP-2199 (Block button)."""
import base64, os, pathlib

HERE = pathlib.Path(__file__).parent
SHOTS = HERE / "shots"

SCENARIOS = [
    {
        "title": "Block + Block All render in the Tool Quarantine view",
        "expected": "A red-outlined 'Block' sits next to every 'Approve', and 'Block All' next to 'Approve All', for each quarantined (changed/pending) tool.",
        "observed": "Banner shows Approve All + Block All; create_issue (changed) and list_repos (pending) each show Approve + Block.",
        "img": "01-quarantine-with-block-buttons.png",
    },
    {
        "title": "Block (single) POSTs {tools:[name]} and the tool leaves the list",
        "expected": "Clicking 'Block' on create_issue calls POST /api/v1/servers/github/tools/block with body {tools:['create_issue']}; the tool then leaves the quarantine list (mocked as approved+disabled).",
        "observed": "blockCalls[0].body == {tools:['create_issue']}; quarantine banner disappears once the list empties.",
        "img": "02-after-single-block.png",
    },
    {
        "title": "Block All POSTs {block_all:true}",
        "expected": "Clicking 'Block All' calls POST .../tools/block with body {block_all:true}.",
        "observed": "blockCalls[0].body == {block_all:true}; banner disappears.",
        "img": "03-after-block-all.png",
    },
]


def b64(p):
    return base64.b64encode((SHOTS / p).read_bytes()).decode()


def main():
    cards = []
    for i, s in enumerate(SCENARIOS, 1):
        cards.append(f"""
        <details class="scenario" {'open' if i==1 else ''}>
          <summary><span class="pass">PASS</span> {i}. {s['title']}</summary>
          <div class="body">
            <p><strong>Expected:</strong> {s['expected']}</p>
            <p><strong>Observed:</strong> {s['observed']}</p>
            <img src="data:image/png;base64,{b64(s['img'])}" />
          </div>
        </details>""")

    html = f"""<!doctype html><html><head><meta charset="utf-8">
<title>MCP-2199 — Block button verification</title>
<style>
  body{{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;max-width:1100px;margin:2rem auto;padding:0 1rem;color:#1f2937}}
  .card{{background:#ecfdf5;border:1px solid #10b981;border-radius:12px;padding:1.2rem 1.5rem;margin-bottom:1.5rem}}
  .card h1{{margin:0 0 .3rem;font-size:1.4rem}}
  .scenario{{border:1px solid #e5e7eb;border-radius:10px;margin-bottom:1rem;overflow:hidden}}
  summary{{cursor:pointer;padding:.9rem 1.1rem;font-weight:600;background:#f9fafb}}
  .body{{padding:1rem 1.2rem}}
  .pass{{background:#10b981;color:#fff;border-radius:6px;padding:.1rem .5rem;font-size:.8rem;margin-right:.5rem}}
  img{{max-width:100%;border:1px solid #d1d5db;border-radius:8px;margin-top:.6rem}}
  code{{background:#f3f4f6;padding:.1rem .35rem;border-radius:4px}}
</style></head><body>
<div class="card">
  <h1>MCP-2199 — Block button in Tool Quarantine view</h1>
  <p>Frontend lane of GH #632. Playwright sweep: <strong>3/3 passed</strong>.
  Built to the backend contract with <code>page.route</code> mocks (POST
  <code>/api/v1/servers/&#123;id&#125;/tools/block</code> not yet merged) — to be
  re-run unmocked after the backend lane lands + rebase onto origin/main.</p>
</div>
{''.join(cards)}
</body></html>"""
    out = HERE / "report.html"
    out.write_text(html)
    print(f"wrote {out}")


if __name__ == "__main__":
    main()
