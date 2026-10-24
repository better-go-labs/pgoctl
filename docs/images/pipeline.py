#!/usr/bin/env python3
"""Generate the README pipeline diagram as light and dark SVGs.

Hand-placed rather than Mermaid: GitHub's Mermaid renderer clipped labels
and reordered the stages. Colors and type follow the pgoctl benchmarks
design system so the README matches the results site. Run from the repo
root:

    python3 docs/images/pipeline.py
"""
import math
from pathlib import Path

OUT = Path(__file__).resolve().parent
W, H = 960, 420
M = 24            # outer margin
GAP = 48          # gap between columns, room for a connector
COL = (W - 2 * M - 2 * GAP) / 3
CARD_H = 124

SANS = "Inter,-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,sans-serif"
MONO = "'JetBrains Mono',ui-monospace,SFMono-Regular,Menlo,Consolas,monospace"

# Tokens from the pgoctl benchmarks design system.
THEMES = {
    "light": dict(surface="#f6f7f9", panel="#ffffff", ink="#1a1d23", muted="#667085",
                  line="#e3e6ea", soft="#eef0f3", accent="#2f5bea", run_bg="#e6ecfc",
                  good="#1b7a43", good_bg="#e7f5ec", warn="#8a5a12", warn_bg="#fbf1dc",
                  bad="#b53434", bad_bg="#fbe7e7", neutral="#5d6573", neutral_bg="#eceef2"),
    "dark": dict(surface="#0f1115", panel="#171a20", ink="#e6e8ec", muted="#8b93a1",
                 line="#262b34", soft="#1e222a", accent="#7b9bff", run_bg="#1b2440",
                 good="#4fc47f", good_bg="#15301f", warn="#e2b25a", warn_bg="#332a14",
                 bad="#f07171", bad_bg="#3a1a1a", neutral="#a0a7b4", neutral_bg="#262b34"),
}

STEPS = [
    ("collect", ["Pull a CPU profile from a pprof", "endpoint, Parca or a file."], ("chip", ["cpu.pprof"])),
    ("validate", ["Score samples and stack coverage;", "a thin profile fails here."], ("chip", ["quality score"])),
    ("merge", ["Fold it into the previous profile", "to make the PGO input."], ("chip", ["default.pgo"])),
    ("leverage-check", ["Diff a -pgo build against a plain", "one: will this profile help?"],
     ("badges", [("HIGH", "good"), ("LOW", "warn"), ("NONE", "neutral")])),
    ("compare", ["Current profile against the previous", "default.pgo: did the PGO build help?"],
     ("badges", [("promote", "good"), ("neutral", "neutral"), ("rollback", "bad")])),
]


def esc(s):
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def render(t):
    out = []
    add = out.append
    add(f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}" '
        f'role="img" aria-label="pgoctl pipeline">')
    add(f'<defs><marker id="m" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" '
        f'orient="auto"><path d="M1,1 L9,5 L1,9" fill="none" stroke="{t["muted"]}" stroke-width="1.6" '
        f'stroke-linecap="round" stroke-linejoin="round"/></marker></defs>')
    add(f'<rect x="0.5" y="0.5" width="{W - 1}" height="{H - 1}" rx="8" fill="{t["surface"]}" stroke="{t["line"]}"/>')

    def text(x, y, s, size=13, color="ink", weight=400, family=SANS, anchor="start", spacing=None):
        ls = f' letter-spacing="{spacing}"' if spacing else ""
        add(f'<text x="{x}" y="{y}" font-family="{family}" font-size="{size}" font-weight="{weight}" '
            f'fill="{t[color]}" text-anchor="{anchor}"{ls}>{esc(s)}</text>')

    def badge(x, y, label, tone):
        # Capitals in 12px Inter run wider than lowercase, so size per glyph.
        w = sum(8.1 if c.isupper() else 3.4 if c == " " else 6.7 for c in label) + 28
        add(f'<rect x="{x}" y="{y}" width="{w}" height="20" rx="10" fill="{t[tone + "_bg"]}"/>')
        add(f'<circle cx="{x + 11}" cy="{y + 10}" r="3" fill="{t[tone]}"/>')
        text(x + 19, y + 14, label, size=12, color=tone, weight=500)
        return x + w

    def chip(x, y, label):
        w = len(label) * 7.3 + 18
        add(f'<rect x="{x}" y="{y}" width="{w}" height="20" rx="4" fill="{t["soft"]}" stroke="{t["line"]}"/>')
        text(x + 9, y + 14, label, size=12, color="muted", family=MONO)

    def card(x, y, n, step, accent=False):
        cmd, desc, (kind, items) = step
        fill = t["run_bg"] if accent else t["panel"]
        stroke = t["accent"] if accent else t["line"]
        add(f'<rect x="{x}" y="{y}" width="{COL}" height="{CARD_H}" rx="8" fill="{fill}" stroke="{stroke}" '
            f'stroke-width="{1.5 if accent else 1}"/>')
        cx, cy = x + 26, y + 26
        add(f'<circle cx="{cx}" cy="{cy}" r="11" fill="{t["accent"] if accent else t["soft"]}" '
            f'stroke="{t["accent"] if accent else t["line"]}"/>')
        text(cx, cy + 4.5, str(n), size=12, weight=600, color="panel" if accent else "muted", anchor="middle")
        add(f'<text x="{x + 46}" y="{y + 31}" font-family="{MONO}" font-size="15" font-weight="500" '
            f'fill="{t["ink"]}"><tspan fill="{t["muted"]}">pgoctl </tspan>{esc(cmd)}</text>')
        for i, line in enumerate(desc):
            text(x + 16, y + 58 + i * 18, line, size=13, color="muted")
        by = y + CARD_H - 32
        if kind == "chip":
            chip(x + 16, by, items[0])
        else:
            bx = x + 16
            for label, tone in items:
                bx = badge(bx, by, label, tone) + 5

    def section(y, label):
        text(M, y, label, size=11, color="muted", weight=600, spacing="0.66")

    def arrow(d):
        add(f'<path d="{d}" fill="none" stroke="{t["muted"]}" stroke-width="1.4" stroke-linecap="round" '
            f'stroke-linejoin="round" marker-end="url(#m)"/>')

    xs = [M + i * (COL + GAP) for i in range(3)]
    r1, r2 = 44, 236

    section(30, "1  PREPARE THE PROFILE")
    for i in range(3):
        card(xs[i], r1, i + 1, STEPS[i])
    for i in range(2):
        mid = r1 + CARD_H / 2
        arrow(f"M{xs[i] + COL + 10},{mid} L{xs[i + 1] - 10},{mid}")

    section(222, "2  DECIDE")
    # Leverage first, its two outcomes next, and compare judges the build.
    card(xs[0], r2, 4, STEPS[3], accent=True)
    card(xs[2], r2, 5, STEPS[4])
    mid2 = r2 + CARD_H / 2

    # merge feeds the leverage check: down from card 3, across, into card 4.
    turn = r1 + CARD_H + 26
    m3 = xs[2] + COL / 2
    m1 = xs[0] + COL / 2
    add(f'<path d="M{m3},{r1 + CARD_H + 6} L{m3},{turn - 8} Q{m3},{turn} {m3 - 8},{turn} '
        f'L{m1 + 8},{turn} Q{m1},{turn} {m1},{turn + 8} L{m1},{r2 - 10}" fill="none" stroke="{t["muted"]}" '
        f'stroke-width="1.4" stroke-linecap="round" marker-end="url(#m)"/>')

    # Outcomes of the leverage check, in the middle column.
    ox = xs[1]
    oh = 56
    o1, o2 = r2, r2 + CARD_H - oh
    for oy, label, tone, lines in ((o1, "HIGH or LOW", "good", ["go build -pgo,", "then benchmark"]),
                                   (o2, "NONE", "neutral", ["skip the benchmark"])):
        add(f'<rect x="{ox}" y="{oy}" width="{COL}" height="{oh}" rx="8" fill="{t["panel"]}" stroke="{t["line"]}"/>')
        end = badge(ox + 14, oy + oh / 2 - 10, label, tone)
        top = oy + oh / 2 + 4.5 - (len(lines) - 1) * 8.5
        for i, line in enumerate(lines):
            text(end + 12, top + i * 17, line, size=13, color="ink")
    trunk = xs[0] + COL + GAP / 2
    arrow(f"M{xs[0] + COL + 6},{mid2} L{trunk},{mid2} L{trunk},{o1 + oh / 2} L{ox - 10},{o1 + oh / 2}")
    arrow(f"M{trunk},{mid2} L{trunk},{o2 + oh / 2} L{ox - 10},{o2 + oh / 2}")
    # Only a build goes on to compare.
    arrow(f"M{ox + COL + 10},{o1 + oh / 2} L{xs[2] - 10},{o1 + oh / 2}")

    # The loop back to step 1, as a footer rather than a wire round the frame.
    fy = H - 26
    cx, cy, r = M + 8, fy - 4.5, 6
    a0, a1 = math.radians(-50), math.radians(230)
    p0 = (cx + r * math.cos(a0), cy + r * math.sin(a0))
    p1 = (cx + r * math.cos(a1), cy + r * math.sin(a1))
    add(f'<path d="M{p0[0]:.2f},{p0[1]:.2f} A{r},{r} 0 1,1 {p1[0]:.2f},{p1[1]:.2f}" fill="none" '
        f'stroke="{t["accent"]}" stroke-width="1.6" stroke-linecap="round"/>')
    # Arrowhead at the end of the arc, pointing along its clockwise tangent.
    tx, ty = -math.sin(a1), math.cos(a1)
    nx, ny = -ty, tx
    tip = (p1[0] + tx * 1.5, p1[1] + ty * 1.5)
    w1 = (tip[0] - tx * 4 + nx * 3.2, tip[1] - ty * 4 + ny * 3.2)
    w2 = (tip[0] - tx * 4 - nx * 3.2, tip[1] - ty * 4 - ny * 3.2)
    add(f'<path d="M{w1[0]:.2f},{w1[1]:.2f} L{tip[0]:.2f},{tip[1]:.2f} L{w2[0]:.2f},{w2[1]:.2f}" fill="none" '
        f'stroke="{t["accent"]}" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>')
    text(M + 24, fy, "Profile the deployed build again, and the loop starts over at step 1.",
         size=12.5, color="muted")

    add("</svg>")
    return "\n".join(out) + "\n"


for name, theme in THEMES.items():
    (OUT / f"pipeline-{name}.svg").write_text(render(theme))
    print(f"wrote docs/images/pipeline-{name}.svg")
