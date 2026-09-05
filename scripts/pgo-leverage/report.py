#!/usr/bin/env python3
"""PGO leverage probe report generator.

Reads pre-computed gcflags -m=2 outputs and pgoctl explain JSON,
classifies PGO leverage as NONE|LOW|HIGH|INCOMPLETE, and writes a
Markdown report.

Env vars:
  TARGET_NAME         - e.g. "prometheus/prometheus"
  HAS_PROFILE         - "true" or "false"
  GCFLAGS_NO_PGO      - path to go build -gcflags=-m=2 output WITHOUT PGO
  GCFLAGS_WITH_PGO    - path to go build -gcflags=-m=2 output WITH PGO
  EXPLAIN_JSON        - path to pgoctl explain --format json output
  REPORT_OUT          - output path for the Markdown report
"""
import os
import re
import json

TARGET_NAME   = os.environ.get("TARGET_NAME", "(unknown)")
HAS_PROFILE   = os.environ.get("HAS_PROFILE", "false").lower() == "true"
NO_PGO_PATH   = os.environ.get("GCFLAGS_NO_PGO", "/tmp/gcflags-no-pgo.txt")
WITH_PGO_PATH = os.environ.get("GCFLAGS_WITH_PGO", "/tmp/gcflags-with-pgo.txt")
EXPLAIN_PATH  = os.environ.get("EXPLAIN_JSON", "/tmp/explain.json")
REPORT_OUT    = os.environ.get("REPORT_OUT", "/tmp/leverage-report.md")


def count_decisions(path):
    """Returns (inline_count, pgo_marker_count, pgo_lines, skipped)."""
    try:
        text = open(path).read()
        if text.strip().startswith("SKIPPED_NO_PROFILE"):
            return None, None, [], True
    except Exception:
        return 0, 0, [], False
    inline_n = len(re.findall(r"can inline|inlining call", text))
    pgo_lines = [
        l for l in text.splitlines()
        if re.search(r"devirtuali|pgo.driven|pgo-driven", l, re.I)
    ]
    return inline_n, len(pgo_lines), pgo_lines, False


no_inline, _, _, _                        = count_decisions(NO_PGO_PATH)
wi_inline, wi_pgo_n, wi_pgo_lines, wi_skipped = count_decisions(WITH_PGO_PATH)

if no_inline is None:
    no_inline = 0

# Parse pgoctl explain JSON
explain_data = {}
try:
    explain_data = json.loads(open(EXPLAIN_PATH).read())
except Exception:
    pass

top_funcs  = explain_data.get("top_functions", [])
pkg_groups = explain_data.get("package_groups", [])

# Classify verdict
if not HAS_PROFILE or wi_skipped:
    leverage_verdict = "INCOMPLETE"
    verdict_emoji    = "⚠️"
    delta_inline     = None
else:
    delta_inline = (wi_inline - no_inline) if wi_inline is not None else 0
    if wi_pgo_n >= 5 or delta_inline >= 30:
        leverage_verdict = "HIGH"
        verdict_emoji    = "\U0001f7e2"
    elif wi_pgo_n >= 1 or delta_inline >= 5:
        leverage_verdict = "LOW"
        verdict_emoji    = "\U0001f7e1"
    else:
        leverage_verdict = "NONE"
        verdict_emoji    = "\U0001f534"

# Recommendation text
if leverage_verdict == "HIGH":
    recommendation = (
        f"PGO found **{wi_pgo_n or 0} devirt decision(s)** and "
        f"**{max(0, delta_inline or 0)} additional inline(s)** on `{TARGET_NAME}`. "
        "**Run a full PGO benchmark cycle** — the compiler has concrete levers to pull."
    )
elif leverage_verdict == "LOW":
    recommendation = (
        f"PGO found **{wi_pgo_n or 0} devirt decision(s)** and "
        f"**{max(0, delta_inline or 0)} additional inline(s)** on `{TARGET_NAME}`. "
        "**Benchmarking may show modest gains** — signal is marginal; confirm with a full cycle."
    )
elif leverage_verdict == "NONE":
    recommendation = (
        f"PGO found **0 devirt decisions** and **{max(0, delta_inline or 0)} additional inline(s)** "
        f"on `{TARGET_NAME}`. "
        "**Skip the full benchmark cycle** — the hot path offers no codegen lever for PGO. "
        "Consider a different target or restructuring the hot path to expose interface dispatch."
    )
else:
    recommendation = (
        f"No profile provided — cannot assess PGO leverage on `{TARGET_NAME}`. "
        f"Baseline: `{no_inline}` inline decisions without PGO. "
        "Collect a profile first, then re-run this probe for a full verdict."
    )

# Decisions table
if not wi_skipped and wi_inline is not None:
    delta_i = wi_inline - no_inline
    decisions_table = (
        "| | Without PGO | With PGO | Delta |\n"
        "|---|---|---|---|\n"
        f"| Inline decisions | {no_inline} | {wi_inline} | {delta_i:+d} |\n"
        f"| PGO devirt/driven markers | 0 | {wi_pgo_n} | +{wi_pgo_n} |\n"
    )
else:
    decisions_table = (
        "| | Without PGO | With PGO |\n"
        "|---|---|---|\n"
        f"| Inline decisions | {no_inline} | _(profile required)_ |\n"
        "| PGO devirt/driven markers | — | _(profile required)_ |\n"
    )

top_pgo_section = ""
if wi_pgo_lines:
    top_pgo_section = (
        "\n\n**Top PGO decisions:**\n"
        + "\n".join(f"> `{l.strip()}`" for l in wi_pgo_lines[:10])
    )

top_funcs_section = ""
if top_funcs:
    rows = "\n".join(
        f"| {i+1} | `{f.get('function', '?')}` | {f.get('flat_pct', 0):.1f}% | {f.get('cum_pct', 0):.1f}% |"
        for i, f in enumerate(top_funcs[:10])
    )
    top_funcs_section = (
        "\n\n**Top hot functions (from profile):**\n"
        "| # | Function | Flat % | Cum % |\n"
        "|---|----------|--------|-------|\n"
        + rows
    )

report_lines = [
    "## PGO Leverage Probe",
    "",
    f"> **Target:** `{TARGET_NAME}`  ",
    f"> **Profile:** {'provided' if HAS_PROFILE else 'not provided'}",
    "",
    "---",
    "",
    f"### {verdict_emoji} Verdict: `PGO-leverage = {leverage_verdict}`",
    "",
    recommendation,
    "",
    "---",
    "",
    "### Static Leverage Analysis (`go build -gcflags=-m=2`)",
    "",
    decisions_table + top_pgo_section + top_funcs_section,
    "",
    "---",
    "",
    "### Verdict Reference",
    "",
    "| Verdict | Condition | Action |",
    "|---------|-----------|--------|",
    "| `HIGH` | ≥5 devirt decisions **or** ≥30 new PGO inlines | Run full benchmark |",
    "| `LOW` | 1–4 devirt **or** 5–29 new inlines | Benchmark; gains likely modest |",
    "| `NONE` | 0 devirt, <5 new inlines | Skip benchmark — no codegen lever |",
    "| `INCOMPLETE` | No profile provided | Collect a profile first |",
    "",
    "---",
    f"*Workflow: `pgo-leverage-probe.yml` · Target: `{TARGET_NAME}` · Runner: ubuntu-latest*",
]

report = "\n".join(report_lines)
with open(REPORT_OUT, "w") as f:
    f.write(report)
print(report)
