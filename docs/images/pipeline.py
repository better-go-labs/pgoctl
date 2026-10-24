#!/usr/bin/env python3
"""Generate the README pipeline diagram as light and dark SVGs.

Hand-placed rather than Mermaid: GitHub's Mermaid renderer clipped labels
and reordered the stages. Run from the repo root:

    python3 docs/images/pipeline.py
"""
from pathlib import Path

OUT = Path(__file__).resolve().parent
W, H = 940, 296
CH = 8.4  # width of one 14px monospace glyph
BOX_H = 44

THEMES = {
    "light": dict(bg="#ffffff", fill="#f6f8fa", stroke="#8c959f", text="#1f2328",
                  muted="#59636e", accent="#0969da", accent_fill="#ddf4ff"),
    "dark": dict(bg="#0d1117", fill="#161b22", stroke="#6e7681", text="#e6edf3",
                 muted="#9198a1", accent="#4493f8", accent_fill="#0c2d6b"),
}


def box_w(label):
    return round(len(label) * CH + 32)


def esc(s):
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def render(t):
    p = []
    add = p.append
    add(f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}" '
        f'role="img" aria-label="pgoctl pipeline">')
    add(f'<defs><marker id="a" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" '
        f'orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" fill="{t["muted"]}"/></marker>'
        f'<marker id="b" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" '
        f'orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" fill="{t["accent"]}"/></marker></defs>')
    add(f'<rect width="{W}" height="{H}" fill="{t["bg"]}"/>')
    mono = 'font-family="ui-monospace,SFMono-Regular,Menlo,Consolas,monospace" font-size="14"'
    sans = 'font-family="-apple-system,BlinkMacSystemFont,Segoe UI,Helvetica,Arial,sans-serif"'

    def box(x, cy, label, accent=False):
        w = box_w(label)
        fill, stroke = (t["accent_fill"], t["accent"]) if accent else (t["fill"], t["stroke"])
        add(f'<rect x="{x}" y="{cy - BOX_H / 2}" width="{w}" height="{BOX_H}" rx="8" '
            f'fill="{fill}" stroke="{stroke}" stroke-width="{2 if accent else 1.2}"/>')
        add(f'<text x="{x + w / 2}" y="{cy + 5}" text-anchor="middle" {mono} fill="{t["text"]}">{esc(label)}</text>')
        return x, x + w

    def arrow(d, color="muted", dash=False):
        m = "b" if color == "accent" else "a"
        extra = ' stroke-dasharray="6 5"' if dash else ""
        add(f'<path d="{d}" fill="none" stroke="{t[color]}" stroke-width="1.6"{extra} marker-end="url(#{m})"/>')

    def tag(x, y, label, color="muted"):
        w = len(label) * 7.2 + 12
        add(f'<rect x="{x - w / 2}" y="{y - 11}" width="{w}" height="18" rx="4" fill="{t["bg"]}"/>')
        add(f'<text x="{x}" y="{y + 2}" text-anchor="middle" {sans} font-size="12.5" fill="{t[color]}">{esc(label)}</text>')

    def stage(y, label):
        add(f'<text x="40" y="{y}" {sans} font-size="12" font-weight="600" letter-spacing="1.2" '
            f'fill="{t["muted"]}">{esc(label)}</text>')

    gap = 48
    r1, r2 = 96, 228

    stage(64, "1  GET AND PREPARE THE PROFILE")
    x = 40
    s0, s1 = box(x, r1, "pprof / Parca / file")
    c0, c1 = box(s1 + gap, r1, "pgoctl collect")
    # Wider gap here so the file-name tag fits between the boxes.
    v0, v1 = box(c1 + 2 * gap, r1, "pgoctl validate")
    m0, m1 = box(v1 + gap, r1, "pgoctl merge")
    arrow(f"M{s1},{r1} L{c0 - 2},{r1}")
    arrow(f"M{c1},{r1} L{v0 - 2},{r1}")
    tag((c1 + v0) / 2, r1 - 14, "cpu.pprof")
    arrow(f"M{v1},{r1} L{m0 - 2},{r1}")

    stage(170, "2  DECIDE")
    k0, k1 = box(40, r2, "pgoctl compare")
    l0, l1 = box(k1 + gap, r2, "pgoctl leverage-check", accent=True)
    mid = (m0 + m1) / 2
    turn = (r1 + BOX_H / 2 + r2 - BOX_H / 2) / 2 + 4
    kx = (k0 + k1) / 2
    arrow(f"M{mid},{r1 + BOX_H / 2} L{mid},{turn} L{kx},{turn} L{kx},{r2 - BOX_H / 2 - 2}")
    tag((mid + kx) / 2, turn, "default.pgo")
    arrow(f"M{k1},{r2} L{l0 - 2},{r2}")

    # Elbow connectors keep each verdict label above its own segment.
    jx, bx = l1 + 28, l1 + 128
    up, down = r2 - 30, r2 + 30
    b0, b1 = box(bx, up, "go build -pgo, then benchmark")
    box(bx, down, "skip the benchmark")
    arrow(f"M{l1},{r2} L{jx},{r2} L{jx},{up} L{bx - 2},{up}", color="accent")
    arrow(f"M{jx},{r2} L{jx},{down} L{bx - 2},{down}")
    tag((jx + bx) / 2, up - 13, "HIGH or LOW", color="accent")
    tag((jx + bx) / 2, down - 13, "NONE")

    # Feedback loop: out of the build box, round the top and down the left
    # margin into the source, clear of the stage captions.
    top, lx, rx = 22, 18, W - 18
    arrow(f"M{b1},{up} L{rx},{up} L{rx},{top} L{lx},{top} L{lx},{r1} L{s0 - 2},{r1}", dash=True)
    tag(W / 2, top, "deploy the build, then profile again")

    add("</svg>")
    return "\n".join(p) + "\n"


for name, theme in THEMES.items():
    (OUT / f"pipeline-{name}.svg").write_text(render(theme))
    print(f"wrote docs/images/pipeline-{name}.svg")
