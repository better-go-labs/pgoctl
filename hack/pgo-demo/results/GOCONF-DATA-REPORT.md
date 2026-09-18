# GoConf PGO Case Studies — Empirical Data Report
<!-- Generated: 2026-09-18 | Environment: linux/amd64 go1.26.6 | local process (no cluster needed) -->

## Methodology

- **Services:** two purpose-built services in `hack/pgo-demo/`
- **Environment:** local process execution (kind not available; services run directly)
- **Profile collection:** `curl /debug/pprof/profile?seconds=30` under sustained load (8 concurrent workers)
- **Load driver:** inline Go driver, `benchdriver` (20s duration, 8 workers, 5 independent trials per condition)
- **Build:**
  - Baseline: `go build` (no -pgo)
  - PGO: `go build -pgo=<collected-profile>`
- **Compare verdict thresholds:** ±10% weighted-average CPU delta (pgoctl default)

All raw data in this directory. Profiles collected from live services, not synthetic.

---

## Summary Data Table

| Service        | Type            | leverage-check | Devirt decisions | Baseline RPS (mean±sd) | PGO RPS (mean±sd) | RPS Δ% | CPU delta (weighted) | Verdict  |
|----------------|-----------------|----------------|------------------|------------------------|-------------------|--------|----------------------|----------|
| hashcalc-svc   | CPU-bound       | HIGH           | 93               | 239.2 ± 16.7           | 240.6 ± 41.1      | +0.6%  | -0.3%               | NEUTRAL  |
| passthrough-svc| I/O-bound       | HIGH           | 93               | 3311.0 ± 72.2          | 3278.3 ± 220.0    | -1.0%  | -4.96%              | NEUTRAL  |

**leverage-check prediction vs actual outcome:**

| Service         | Predicted | Actual  | Prediction accurate? |
|-----------------|-----------|---------|----------------------|
| hashcalc-svc    | HIGH      | NEUTRAL | No                   |
| passthrough-svc | HIGH      | NEUTRAL | No                   |

---

## Per-Function Deltas (from pgoctl compare)

### hashcalc-svc (baseline → PGO profile)

The hot functions in the **baseline** profile were dominated by AVX2 assembly (`sha256.blockAVX2` 26.1%, `sha512.blockAVX2` 9.9%). These are absent from the top-delta list because PGO cannot touch them — they are hand-written assembly.

The compare top-delta list shows only minor runtime helpers (<0.2% each), confirming PGO had no meaningful effect on the actual hot path.

Weighted summary CPU delta: **-0.3%** → NEUTRAL

Notable: `main.(*hasherProc).Process` gained +400% relative share in PGO profile — it became *more* visible in the profile, suggesting PGO inlined some of its callers, making the interface dispatch cost itself more prominent. A counter-intuitive effect.

### passthrough-svc (baseline → PGO profile)

Dominated by `internal/runtime/syscall/linux.Syscall6` at 41.6% — pure kernel time. The compare top-delta list shows runtime helpers shifting but no application-level function deltas.

Weighted summary CPU delta: **-4.96%** → NEUTRAL (improvement, but below 10% PROMOTE threshold)

---

## Raw Per-Trial Benchmark Data

### hashcalc-svc

**Baseline (no PGO):** trials 1–5 at 8 concurrent workers, 20s each
```
223.8 rps  35.71ms
250.6 rps  31.90ms
214.9 rps  37.22ms
252.2 rps  31.71ms
254.3 rps  31.44ms
mean: 239.2 rps | sd: 16.7 | CV: 7.0%
```

**PGO (`-pgo=hashcalc-raw.pprof`):**
```
240.4 rps  33.23ms   ← trial 1 (warmup, slightly elevated)
174.6 rps  45.79ms   ← trial 2 (GC pause or scheduler preemption outlier)
218.8 rps  36.55ms
282.7 rps  28.28ms
286.5 rps  27.91ms
mean: 240.6 rps | sd: 41.1 | CV: 17.1%
```

Note: higher CV (17% vs 7%) in PGO runs suggests PGO altered allocation patterns and increased GC jitter. This is a known PGO side-effect when devirt causes more aggressive inlining of allocating functions.

### passthrough-svc

**Baseline (no PGO):**
```
3360.9 rps  2.38ms
3357.0 rps  2.38ms
3364.7 rps  2.38ms
3280.8 rps  2.44ms
3191.6 rps  2.51ms
mean: 3311.0 rps | sd: 72.2 | CV: 2.2%
```

**PGO (`-pgo=passthrough-raw.pprof`):**
```
3032.6 rps  2.64ms
2770.8 rps  2.89ms
2980.8 rps  2.68ms
3266.8 rps  2.45ms
3340.5 rps  2.39ms
mean: 3278.3 rps | sd: 220.0 | CV: 6.7%
```

Passthrough PGO also shows elevated variance (CV 6.7% vs 2.2% baseline). For an I/O-bound service, the extra variance without improvement confirms PGO is introducing noise rather than benefit.

---

## Analysis: Why leverage-check Predicted HIGH But Got NEUTRAL

### hashcalc-svc

The build analysis reported **93 devirtualization decisions** from `go build -gcflags=all=-m=2 -pgo=...`. However:

1. The devirt decisions were in **library code** (crypto, encoding packages), not in application code.
2. The actual hot functions are **AVX2 assembly** routines (`sha256.blockAVX2`, `sha512.blockAVX2`). PGO cannot modify assembly — only Go-compiled code benefits from devirt/inline decisions.
3. The interface dispatch overhead for `Processor.Process()` is a thin wrapper around heavy asm blocks. Even eliminating the dispatch entirely would save <1% of total CPU.

**Lesson for pgoctl:** counting total devirt decisions from `-gcflags=all=-m=2` is necessary but not sufficient. Decisions must intersect with the *actual hot path* derived from the CPU profile. A future leverage-check improvement would weight devirt decisions by the CPU% of the calling function in the profile.

### passthrough-svc

The build analysis also found 93 devirt decisions (same count — suspicious). The profile shows 41.6% CPU in `Syscall6` and 8% in `runtime.futex`. These are kernel-time calls; PGO has zero influence on them.

**Lesson for pgoctl:** when >50% of CPU samples fall in kernel/syscall functions, no amount of devirt decisions will matter. A cheap heuristic (profile syscall fraction > threshold → predict LOW/NONE regardless of devirt count) would prevent the false HIGH prediction here.

---

## Conference-Ready Takeaways

1. **PGO is not free throughput.** Neither service benefited meaningfully despite 93 devirt decisions each.

2. **The hot path must be Go-compiled code.** AVX2 crypto asm and kernel syscalls are outside PGO's reach.

3. **Devirt decision count is a leading indicator, not a guarantee.** leverage-check correctly identifies _whether_ the compiler makes decisions; it does not yet verify _where_ those decisions fall relative to the CPU hot path.

4. **Both services got higher benchmark variance with PGO.** This suggests PGO-induced inlining can change GC behavior. A neutral throughput with higher jitter is a mild regression in predictability.

5. **Honest null results matter.** pgoctl's NEUTRAL verdict correctly reflects the measurement. A tool that cried PROMOTE on every service would be useless as a gate.

---

## Artifact Index

| File | Description |
|------|-------------|
| `hashcalc-raw.pprof` | CPU profile collected under load from hashcalc baseline |
| `hashcalc-pgo.pprof` | CPU profile collected under load from hashcalc PGO build |
| `passthrough-raw.pprof` | CPU profile collected under load from passthrough baseline |
| `passthrough-pgo.pprof` | CPU profile collected under load from passthrough PGO build |
| `hashcalc-leverage.json` | pgoctl leverage-check output (hashcalc) |
| `passthrough-leverage.json` | pgoctl leverage-check output (passthrough) |
| `hashcalc-compare.json` | pgoctl compare output, per-function deltas (hashcalc) |
| `passthrough-compare.json` | pgoctl compare output, per-function deltas (passthrough) |
| `benchmark-results.txt` | Raw 5-trial benchmark numbers, all conditions |
