# Benchmarks

pgoctl builds are benchmarked against the [Prometheus tsdb](https://github.com/prometheus/prometheus) package to measure the impact of Profile-Guided Optimization (PGO) on real-world Go workloads.

## Running benchmarks

### Manual trigger (recommended)

1. Go to **Actions → PGO Benchmark → Run workflow** in the GitHub UI.
2. Select the branch you want to benchmark (default: `main`).
3. Click **Run workflow**.

The workflow will fail fast with a clear message if `testdata/prometheus_cpu.pprof` is missing — run the `profile-collect` workflow first to generate it.

### Automatic trigger

The workflow also fires automatically on any PR that touches `testdata/*.pprof` or `.github/workflows/pgo-bench.yml`. It does **not** run on every PR.

## Methodology

- **Harness:** each benchmarked package is run for **10 rounds**, baseline vs. PGO-optimized build, on the same runner.
- **Aggregation:** results are reported as the **geomean** across a package's benchmarks; per-benchmark deltas use `benchstat` with significance at **p < 0.05**.
- **Primary metric:** `sec/op` (wall-clock time per operation). `B/op` and `allocs/op` are tracked as secondary metrics and watched for regressions.
- **Reporting:** the CI workflow runs the 10 rounds on each PR and posts a sticky comment with the delta table; the numbers below are the latest snapshot merged to `main`.

Lower is better throughout. Negative percentages mean the PGO build is faster / smaller.

## Latest snapshot

Commit `d626d60` (merged to `main`) — geomean `sec/op`, baseline → PGO:

| Package  | Baseline | PGO      | Δ sec/op   | Notes |
| -------- | -------- | -------- | ---------- | ----- |
| tsdb     | 79.38µ   | 78.39µ   | **−1.24%** |       |
| chunkenc | 1.998µ   | 1.970µ   | **−1.40%** |       |
| storage  | 257.0n   | 254.6n   | **−0.93%** |       |
| labels   | 259.3n   | 258.7n   | −0.23%     | flat  |
| promql   | 644.6n   | 645.2n   | +0.10%     | flat  |

**Notable individual wins:**
- `labels/String` — **−20.13%** (p < 0.05)
- `tsdb/HeadStripeSeriesCreate` — measurable improvement

Overall: the hot paths that dominate the production profile (tsdb, chunkenc, storage) show consistent single-digit-percent speedups, while promql and labels are flat within noise.

## What to watch

Two regressions are being tracked (see issue *"Track: memory B/op uptick and FastRegexMatcher regression from initial PGO run"*):

1. **`FastRegexMatcher/((fo(bar))|.+foo)` — +13.67% sec/op.** A regex pattern that was not hot in the production profile and appears to have been de-prioritized by the optimizer.
2. **Memory `B/op` uptick** — tsdb **+3.56%** and storage **+3.44%**. PGO traded some allocation footprint for speed on these paths.

Hypothesis for both: the affected patterns were cold in the profile used for this run. The expectation is that a broader / more representative profiling run stabilizes or reverses these. See the tracking issue for acceptance criteria.
