# Benchmark Methodology

This folder holds per-run benchmark result files for the five libraries tested by the
`pgo-bench-real-repos` CI workflow. The structure is:

```
benchmarks/
  README.md          ← this file, methodology prose only, no numbers
  results/
    index.md         ← generated: all runs grouped by CPU model
    YYYY-MM-DD-<run_id>-<cpu-slug>.md  ← generated: one file per run
```

Files are generated from CI artifacts. A person commits them after review. Auto-pushing causes conflicts when multiple runs fly.

---

## What the harness does

Each run executes ten interleaved rounds. Each round runs baseline and PGO binaries back-to-back. This interleaving cancels machine warm-up drift.

Compilation is separate from measurement: all ten binaries (five libraries × two
variants) are compiled before any timing starts. This keeps compiler overhead out of
the measurements.

Per-binary settings:
- `GOMAXPROCS=2`: fixed for the whole job
- `-benchtime=5s` per binary per round
- 90s hard timeout per binary invocation

---

## How to read a result row

The per-run table has six columns: Library, Baseline CV, PGO CV, Change, p-value, n.

Read them in this order:

1. **CV first.** The coefficient of variation (stddev ÷ mean, as a percentage) tells
   you how noisy the arm is. A high CV means the machine was not in a stable state for
   that arm; the change and p-value are less meaningful.

2. **The floor.** The smallest meaningful change is roughly `3 × CV / sqrt(n)`. At n=10, the floor approaches CV: changes below it are noise.

3. **The change.** The signed percentage improvement reported by benchstat. A `~`
   means benchstat found no statistically significant difference at its threshold.

4. **The p-value.** The Wilcoxon signed-rank p-value from benchstat. A low p-value
   says the direction is consistent across the ten rounds; it does not say the
   magnitude is large relative to the floor.

5. **n.** Should always be 10 for complete runs. A lower value means some rounds were
   dropped (timeout or binary crash); treat such rows with extra caution.

---

## CPU model stratification

Results are grouped by CPU model in `index.md`. **Never average rows from different CPU models.** PGO gains are micro-architecture-specific. The same binary produces different gains on different chips.

---

## Known-bad patterns

### `± ∞` in benchstat output

Benchstat could not compute a confidence interval. This means one arm has zero variance (timeout or crash). The row is unreliable.

### Significant p-value in both directions across runs

If runs show opposite significant changes for the same library on the same CPU, noise exceeds signal. The signal is not real.

### Lopsided CV pair (e.g., large baseline CV, small PGO CV)

Something disturbed one arm during the run: scheduling, thermal throttling, or noise. Do not read stability into the high-CV arm.

---

## Provenance

Each file names the GitHub Actions run ID and commit SHA. The run ID is the stable reference. Raw `.txt` files stay in the Actions UI until expiry.

Benchstat statistics (change, p-value) come directly from benchstat output. CV,
standard error, and the floor are derived values computed from the raw benchmark
output files.
