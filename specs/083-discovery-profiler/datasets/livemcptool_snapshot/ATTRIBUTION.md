# LiveMCPTool Frozen Snapshot — Attribution & Provenance

This directory contains a frozen, normalized snapshot of the **LiveMCPTool**
corpus from **LiveMCPBench** ("LiveMCPBench: Can Agents Navigate an Ocean of
MCP Tools?"), used by the Spec 083 discovery profiler as a schema-bearing
MCP-native corpus for token/scale measurement of the encoding arms (FR-011b,
FR-013, research D6).

## Source

| Field | Value |
|---|---|
| Dataset | [ICIP/LiveMCPBench](https://huggingface.co/datasets/ICIP/LiveMCPBench) (Hugging Face), file `tools/tools.json` |
| Pinned revision | `ddea2d24196638bc4026c4cb891f679d0357bfd0` (dataset last modified 2025-08-07) |
| Code repository | [icip-cas/LiveMCPBench](https://github.com/icip-cas/LiveMCPBench) |
| Paper | Mo et al., *LiveMCPBench: Can Agents Navigate an Ocean of MCP Tools?*, [arXiv:2508.01780](https://arxiv.org/abs/2508.01780) |
| License | **Apache License 2.0** (per the LiveMCPBench repository `LICENSE`; Apache 2.0 permits redistribution with attribution — this file is that attribution) |
| Captured | 2026-07-14 |
| Scale | **70 servers / 527 tools** (matches the paper). All 527 tools carry an `inputSchema`; 19 have an empty/absent description (preserved as `""`). |

Note: the GitHub repo's working copy of `tools/LiveMCPTool/tools.json` drifts
from the paper (69 servers / 525 tools at commit `36a51a10`, checked
2026-07-14); the pinned Hugging Face revision above is the canonical 70/527
corpus and is what this snapshot freezes.

## Transformation

`tools.json` here is a normalization of the upstream file — content is
unchanged, only re-shaped:

1. Upstream shape: an array of 70 marketplace entries, each carrying
   `tools: {<server_name>: {tools: [{name, description, inputSchema, annotations}]}}`.
2. Flattened to one row per `(server, tool)`: `{server, tool, description, inputSchema}`.
   Marketplace metadata (`organization`, `web`, `config`, `category`) and the
   always-null `annotations` are dropped; `description: null` becomes `""`;
   `inputSchema` is kept verbatim when present.
3. Rows sorted by the composed ID `"<server>:<tool>"` (byte order); all JSON
   object keys sorted (`jq -S`); pretty-printed for git diffability.
4. A `version` stamp (`livemcptool@ddea2d24` — first 8 chars of the pinned HF
   revision), a `source` provenance block, and self-describing
   `server_count` / `tool_count` headers were added; the loader
   (`bench/corpusio/livemcptool.go`) fails loudly if the headers drift from
   the rows.

One-time reproduction (network required; the snapshot is otherwise frozen —
offline bench runs never fetch):

```bash
REV=ddea2d24196638bc4026c4cb891f679d0357bfd0
curl -sL "https://huggingface.co/datasets/ICIP/LiveMCPBench/resolve/${REV}/tools/tools.json" -o /tmp/livemcptool_raw.json
jq -S '{
  version: "livemcptool@ddea2d24",
  source: {
    dataset: "ICIP/LiveMCPBench",
    url: "https://huggingface.co/datasets/ICIP/LiveMCPBench",
    file: "tools/tools.json",
    revision: "ddea2d24196638bc4026c4cb891f679d0357bfd0",
    captured: "2026-07-14",
    license: "Apache-2.0",
    paper: "arXiv:2508.01780",
    note: "Normalized per ATTRIBUTION.md: flattened marketplace entries to one row per (server, tool); descriptions null->empty; inputSchema kept when present; rows sorted by \"server:tool\"; keys sorted (jq -S)."
  },
  server_count: ([.[].tools | keys[]] | unique | length),
  tool_count: ([.[].tools[].tools[]] | length),
  tools: ([.[] | .tools | to_entries[] | .key as $s | .value.tools[] |
    {server: $s, tool: .name, description: (.description // "")} +
    (if .inputSchema == null then {} else {inputSchema: .inputSchema} end)
  ] | sort_by(.server + ":" + .tool))
}' /tmp/livemcptool_raw.json > tools.json
```

## Relevance labels — explicit absence (FR-011)

Retrieval-quality scoring for this corpus was **attempted and not derived**
(FR-011 marks it SHOULD, not MUST). The 95 LiveMCPBench task annotations
(`annotated_data/all_annotations.json`) list the tools used by each reference
solution only as unqualified free-text names inside `"Annotator Metadata"`
(a numbered string list). Analysis at capture time:

- 150 distinct tool names across the 95 tasks;
- 145 match a corpus tool name exactly, **5 resolve to no corpus tool**
  (`ddg-search`, `execute_command`, `get_maven_latest_version`,
  `open_document`, `trial_references`);
- **13 names are ambiguous** — they exist on more than one server (e.g.
  `read_file`, `search_files`, `fetch`, `add_table`), and the annotations do
  not say which server's tool was used.

Deriving a golden set would therefore require guessing server attribution for
ambiguous names and dropping/aliasing unresolvable ones — a judgement-laden
mapping, not an explicit label set. The loader records this absence: it
returns the corpus, a `nil` golden set, and a machine-readable reason string.
LiveMCPTool is used for token/scale measurement only.

## Citation

```bibtex
@misc{mo2025livemcpbenchagentsnavigateocean,
      title={LiveMCPBench: Can Agents Navigate an Ocean of MCP Tools?},
      author={Guozhao Mo and Wenliang Zhong and Jiawei Chen and Xuanang Chen and Yaojie Lu and Hongyu Lin and Ben He and Xianpei Han and Le Sun},
      year={2025},
      eprint={2508.01780},
      archivePrefix={arXiv},
      primaryClass={cs.AI},
      url={https://arxiv.org/abs/2508.01780},
}
```
