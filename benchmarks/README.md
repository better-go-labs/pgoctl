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

`index.md` and the per-run files are generated from the raw `*-stat.txt` and
`*-baseline.txt` artifacts produced by CI. They are committed by a person after
reviewing the run, not auto-pushed from CI (concurrent pushes to the same branch
conflict when multiple runs are in flight).

---

## What the harness does

Each CI run executes ten interleaved rounds. Within each round it runs the baseline
binary and the PGO binary back-to-back for every library, in the order goldmark →
gjson → go-json → snappy → fasthttp. This A/B interleaving within rounds is the
mechanism that cancels slow machine-state drift: if the machine gradually warms up
over the run, both arms warm together.

Compilation is separate from measurement: all ten binaries (five libraries × two
variants) are compiled before any timing starts. This keeps compiler overhead out of
the measurements.

Per-binary settings:
- `GOMAXPROCS=2` — fixed for the whole job
- `-benchtime=5s` per binary per round
- 90s hard timeout per binary invocation

---

## How to read a result row

The per-run table has six columns: Library, Baseline CV, PGO CV, Change, p-value, n.

Read them in this order:

1. **CV first.** The coefficient of variation (stddev ÷ mean, as a percentage) tells
   you how noisy the arm is. A high CV means the machine was not in a stable state for
   that arm; the change and p-value are less meaningful.

2. **The floor.** The smallest change that could be meaningful given the noise is
   approximately `3 × CV / sqrt(n)`. This is a convention, not a theorem — the `3` is
   a multiplier chosen to give a rough significance threshold. At `n = 10`,
   `sqrt(n) ≈ 3.16`, so the floor is close to the CV itself: a change smaller than the
   floor is inside the noise margin even if benchstat marks it significant.

3. **The change.** The signed percentage improvement reported by benchstat. A `~`
   means benchstat found no statistically significant difference at its threshold.

4. **The p-value.** The Wilcoxon signed-rank p-value from benchstat. A low p-value
   says the direction is consistent across the ten rounds; it does not say the
   magnitude is large relative to the floor.

5. **n.** Should always be 10 for complete runs. A lower value means some rounds were
   dropped (timeout or binary crash); treat such rows with extra caution.

---

## CPU model stratification

Results are grouped by CPU model in `index.md`. **Never average or combine rows from
different CPU models.** PGO gains are micro-architecture-specific: the profile was
collected on one machine, and the resulting inlining decisions interact with that
machine's branch predictor, cache geometry, and instruction decoder. The same binary
produces different gains on a different chip family.

---

## Known-bad patterns

### `± ∞` in benchstat output

Benchstat could not compute a confidence interval. This usually means one arm had
zero variance across rounds (all measurements identical), which happens when the
binary hit a timeout and produced no output for some rounds. The row is unreliable.

### Significant p-value in both directions across runs

If one run shows a statistically significant improvement and another shows a
statistically significant regression for the same library on the same CPU model, the
floor sits above the change. The noise is large enough to produce apparent significance
in either direction — the signal is not real.

### Lopsided CV pair (e.g., large baseline CV, small PGO CV)

This is not evidence that PGO stabilised the machine. It means something disturbed one
arm during that run — scheduling interference, thermal throttling, or a noisy neighbour
on the host. The low-CV arm's measurements are fine; the high-CV arm's are not. Do not
read stability into the PGO side.

---

## Provenance

Each per-run file names the GitHub Actions run ID and the commit SHA the workflow ran
against. The run ID is the stable reference: as long as the artifact has not expired,
the raw `.txt` files that produced the file can be retrieved from the Actions UI.

Benchstat statistics (change, p-value) come directly from benchstat output. CV,
standard error, and the floor are derived values computed from the raw benchmark
output files.
