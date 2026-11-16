# Third-Party Notices

MCPProxy includes material adapted from the following third-party open-source
projects. Each entry lists the license, the copyright notice, the upstream
files that were adapted and the files in this repository that carry the
adaptation. The full license texts live under `third_party/`.

## NVIDIA SkillSpector

- **License:** Apache-2.0 (`third_party/skillspector/LICENSE`)
- **Copyright:** Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
- **URL:** https://github.com/NVIDIA/SkillSpector
- **Reviewed at commit:** `5cb9385ab0dffa974088ee21c4376c7e560b2a52` (2026-11-02)
- **Nature of use:** pattern families, thresholds and a bounded grammar were
  studied and re-implemented in Go for MCP tool metadata; no Python code,
  YARA bundle or test fixture file is copied verbatim. Individual test
  sentences reused as regression cases are marked "(upstream)" in the Go test
  files. Each adapting Go file carries the upstream copyright line, the
  SPDX identifier, the upstream path, the reviewed commit and a summary of the
  modifications, as Apache-2.0 §4 requires.

| Upstream file | Adapted in |
|---|---|
| `src/skillspector/nodes/analyzers/static_patterns_anti_refusal.py` (AR1–AR3) | `internal/security/detect/checks/anti_refusal.go` |
| `src/skillspector/nodes/analyzers/whitespace_padding.py` (P9) | `internal/security/detect/checks/padding_hidden.go` |
| `src/skillspector/security_reconstruction.py` | `internal/security/detect/checks/marker_reconstruction.go` |
| `src/skillspector/nodes/analyzers/prohibition_context.py` | `internal/security/detect/prohibition.go` |
| `src/skillspector/yara_rules/agent_skills.yar` (`agent_skill_credential_exfiltration_webhook`), `src/skillspector/nodes/analyzers/static_patterns_data_exfiltration.py` (prose patterns) | `internal/security/detect/checks/composite_exfil.go` |
| `tests/nodes/analyzers/test_static_patterns_anti_refusal.py`, `tests/nodes/analyzers/test_whitespace_padding.py`, `tests/nodes/analyzers/test_security_reconstruction.py`, `tests/nodes/analyzers/test_prohibition_review_regressions.py` (selected sentences) | `internal/security/detect/checks/*_test.go`, `internal/security/detect/prohibition_test.go`, `specs/065-evaluation-foundation/datasets/detect_review_corpus_v1.json` |

The upstream repository's other YARA bundles (`cryptominers.yar`,
`hacktools.yar`, `webshells.yar`, `malware.yar.b64`) and its third-party
notices are NOT included.
