#!/usr/bin/env python3
"""PGO leverage probe report formatter.

Reads pgoctl leverage-check --json output and writes a Markdown report.
All verdict/threshold logic lives in the Go command; this script is a
pure JSON-to-Markdown renderer.

Env vars:
  LEVERAGE_JSON  - path to pgoctl leverage-check --json output
  TARGET_NAME    - display name for the target (optional, falls back to profile_path)
  REPORT_OUT     - output path for the Markdown report
"""
import os
import json

LEVERAGE_PATH = os.environ.get("LEVERAGE_JSON", "/tmp/leverage.json")
REPORT_OUT    = os.environ.get("REPORT_OUT", "/tmp/leverage-report.md")

with open(LEVERAGE_PATH) as f:
    data = json.load(f)

verdict        = data.get("verdict", "INCOMPLETE")
verdict_reason = data.get("verdict_reason", "")
profile_path   = data.get("profile_path", "(unknown)")
total_samples  = data.get("total_samples", 0)
top_functions  = data.get("top_functions", [])
build_analysis = data.get("build_analysis")

TARGET_NAME = os.environ.get("TARGET_NAME", profile_path)

EMOJI = {"HIGH": "\U0001f7e2", "LOW": "\U0001f7e1", "NONE": "\U0001f534", "INCOMPLETE": "⚠️"}
verdict_emoji = EMOJI.get(verdict, "❓")

# Decisions table — data comes directly from build_analysis in the JSON
if build_analysis:
    devirt      = build_analysis.get("devirt_decisions", 0)
    extra       = build_analysis.get("pgo_extra_inlines", 0)
    base_inl    = build_analysis.get("baseline_inlines", 0)
    pgo_inl     = build_analysis.get("pgo_inlines", 0)
    decisions_table = (
        "| | Without PGO | With PGO | Delta |\n"
        "|---|---|---|---|\n"
        f"| Inline decisions | {base_inl} | {pgo_inl} | {extra:+d} |\n"
        f"| PGO devirt/driven markers | 0 | {devirt} | +{devirt} |\n"
    )
else:
    decisions_table = (
        "| | Without PGO | With PGO |\n"
        "|---|---|---|\n"
        "| Inline decisions | — | _(build analysis not run)_ |\n"
        "| PGO devirt/driven markers | — | _(build analysis not run)_ |\n"
    )

# Top hot functions table
top_funcs_section = ""
if top_functions:
    rows = "\n".join(
        f"| {i+1} | `{f.get('function', '?')}` | {f.get('flat_pct', 0):.1f}% | {f.get('cum_pct', 0):.1f}% |"
        for i, f in enumerate(top_functions[:10])
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
    f"> **Samples:** {total_samples:,}",
    "",
    "---",
    "",
    f"### {verdict_emoji} Verdict: `PGO-leverage = {verdict}`",
    "",
    verdict_reason,
    "",
    "---",
    "",
    "### Static Leverage Analysis (`pgoctl leverage-check --dir`)",
    "",
    decisions_table + top_funcs_section,
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
    "| `INCOMPLETE` | No build analysis (no --dir) | Run with --dir for full verdict |",
    "",
    "---",
    f"*Workflow: `pgo-leverage-probe.yml` · Target: `{TARGET_NAME}` · Runner: ubuntu-latest*",
]

report = "\n".join(report_lines)
with open(REPORT_OUT, "w") as f:
    f.write(report)
print(report)
