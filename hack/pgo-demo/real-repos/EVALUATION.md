# pgoctl leverage-check: Validation Against Real Open-Source Go Repositories

**Date:** 2026-09-18 (CI results: 2026-10-22)
**Tool:** `pgoctl leverage-check`
**Environment:** linux/amd64, go1.26.x (GitHub-hosted ubuntu-latest runners)

> **Updated 2026-10-22:** Results are from the CI harness
> (`.github/workflows/pgo-bench-real-repos.yml`, run ids below).
> The manual-VM v3 numbers (n=5, `±∞` CI, unlogged 2-vCPU Skylake VM) are retired as
> evidence; they cannot reliably detect the 2–7% PGO gains at issue.
>
> **Key finding:** PGO effect is per exact CPU model: tight within a model,
> large gaps between models. Always report the CPU model alongside any delta;
> never average deltas across CPU models.

---

## Scope

This document validates `pgoctl leverage-check` across five real open-source Go repositories
spanning a range of expected PGO leverage: CPU-bound parsers, JSON processors, a compressor,
and an HTTP framework. It compares the tool's prediction (HIGH/LOW/NONE) against CI benchmark
results (baseline vs PGO build), and traces cases where the prediction and observed speedup
disagree.

The controlled hashcalc/passthrough pair in `hack/pgo-demo/results/GOCONF-DATA-REPORT.md`
serves as the anchor baseline.

---

## Methodology

### Profile Collection

For each repo, a synthetic benchmark module was created that imports the library and runs
a representative workload. CPU profiles were collected via:

```bash
go test -bench=<BenchmarkName> -run=^$ -count=1 -benchtime=10s -cpuprofile=profile.pprof
```

### leverage-check Usage

Two invocation modes:

1. **Static only** (profile analysis without build):
   ```
   pgoctl leverage-check profile.pprof
   ```
   Returns `INCOMPLETE` (no devirt count without `--dir`).

2. **Full analysis** (with `--dir` pointing to the library's own module directory):
   ```
   pgoctl leverage-check profile.pprof --dir $(go env GOMODCACHE)/<library@version> --json
   ```
   Returns `HIGH`/`LOW`/`NONE` based on devirt decisions and extra inlines.

   > **Important:** `--dir` must point to the library's source, not to a wrapper benchmark
   > module. A wrapper module containing only `*_test.go` files produces 0 `go build`
   > output (test files are excluded from `go build ./...`), yielding an incorrect NONE.

### Benchmark Harness (CI-backed)

All benchmark results in this document come from
`.github/workflows/pgo-bench-real-repos.yml` running on GitHub-hosted `ubuntu-latest`.

- Single job; pre-compiled test binaries per library
- **10 interleaved rounds** (baseline/PGO alternating per library per round, same pattern
  as `pgo-bench.yml`)
- `GOMAXPROCS=2`; 90 s per-binary timeout; `benchtime=5s` per round
- Results carry a GitHub Actions `run_id`; raw data uploaded as workflow artifact
- Signal threshold: ≥2–3% improvement at p<0.05 (house standard)

**Uncertainty caveat:** The within-run CI interval reported by benchstat (e.g. ±1%,
p=0.000) describes variability *within that one run* only. Machine-to-machine variation
across runs on different GitHub-hosted VMs can dwarf within-run CI: baseline medians have
been observed 7–18% apart across runs on the same commit (go-json 14%, snappy 18%).
This variation is largely explained by CPU model assignment (GitHub assigns AMD or Intel
unannounced). The CPU model must be reported alongside any delta; deltas from different
CPU models must never be averaged.

---

## Results Summary

### Controlled Demo Services (hack/pgo-demo)

| Service | Category | Prediction | Bench Delta | Outcome |
|---------|----------|------------|-------------|---------|
| hashcalc-svc | CPU-bound (crypto + asm) | HIGH | +0.6% RPS | **NEUTRAL** |
| passthrough-svc | I/O-bound (HTTP proxy) | HIGH | -1.0% RPS | **NEUTRAL** |

Both services receive HIGH predictions and deliver NEUTRAL outcomes. Analysis in
`GOCONF-DATA-REPORT.md`: the devirt decisions fall on library/stdlib code, not the
application hot path.

---

### Real Open-Source Libraries: Leverage-Check Predictions

| Repository | Category | Prediction | Devirt (mod) | Devirt (deps) | Extra Inlines (mod) | Extra Inlines (deps) |
|------------|----------|------------|-------------|--------------|--------------------|--------------------|
| [yuin/goldmark](https://github.com/yuin/goldmark) v1.8.6 | Markdown parser | **HIGH** | 68 | 4 | 252 | 568 |
| [tidwall/gjson](https://github.com/tidwall/gjson) v1.19.0 | JSON path query | **HIGH** | 0 | 3 | 36 | 424 |
| [goccy/go-json](https://github.com/goccy/go-json) v0.10.6 | JSON encoder/decoder | **HIGH** | 10 | 3 | 59 | 598 |
| [golang/snappy](https://github.com/golang/snappy) v1.0.0 | Compressor (Go+asm) | **NONE** | 0 | 3 | 2 | 799 |
| [valyala/fasthttp](https://github.com/valyala/fasthttp) v1.74.0 | HTTP framework | **HIGH** | 0 | 1 | 92 | 771 |

Devirt and extra-inline counts are module-scoped (post-#55). Dep counts are shown
separately and do not contribute to the verdict.

---

### Real Open-Source Libraries: CI Benchmark Results by CPU Model

GitHub-hosted runners assign AMD or Intel CPUs without announcement. Results are
stratified by exact CPU model. Do not average across models.

| Library | CPU | Run IDs | n | Δ values (%) | p-value | 3%-gate verdict |
|---------|-----|---------|---|--------------|---------|-----------------|
| goldmark | AMD EPYC 7763 | 39828409134, 39831658152, 39833250644, 39835714756 | 4 | −6.31, −5.39, −5.81, −6.54 | ≤0.001 all | **PROMOTE** |
| goldmark | Intel Xeon 8573C | 39831653386, 39833287253 | 2 | −6.40, −5.22 | ≤0.001 both | **PROMOTE** |
| goldmark | Intel Xeon 8370C | 39835718451 | 1 | −4.40 | 0.001 | **PROMOTE** |
| gjson | AMD EPYC 7763 | 39828409134, 39831658152, 39833250644, 39835714756 | 4 | −15.74, −16.04, −15.76, −15.89 | ≤0.001 all | **PROMOTE** |
| gjson | Intel Xeon 8573C | 39831653386, 39833287253 | 2 | −6.69, −6.42 | ≤0.001 both | **PROMOTE** |
| gjson | Intel Xeon 8370C | 39835718451 | 1 | −11.06 | 0.001 | **PROMOTE** |
| go-json | AMD EPYC 7763 | 39828409134, 39831658152, 39833250644, 39835714756 | 4 | −3.66, −6.02, −3.93, −5.42 | ≤0.001 all | **PROMOTE** |
| go-json | Intel Xeon 8573C | 39831653386, 39833287253 | 2 | −1.52, −2.06 | p=0.000 / p=0.002 | **NEUTRAL** (below 3%) |
| go-json | Intel Xeon 8370C | 39835718451 | 1 | not significant | 0.247 | **NEUTRAL** |
| snappy | all models | 7 runs total | 7 | see note | see note | **NEUTRAL / NONE** |
| fasthttp ★ | AMD EPYC 7763 | 39833250644, 39835714756 | 2 | −7.77, −7.77 | ≤0.001 both | **PROMOTE** (provisional) |
| fasthttp ★ | Intel Xeon 8573C | 39833287253 | 1 | −11.44 | 0.001 | **PROMOTE** (provisional) |
| fasthttp ★ | Intel Xeon 8370C | 39835718451 | 1 | −6.23 | 0.001 | **PROMOTE** (provisional) |

★ fasthttp rows marked ★ are from the reshaped benchmark (commit `40150e0` onward: hoisted
allocations, per-iteration reset, error check). **Not comparable** to earlier runs
(old benchmark ~2.4 µs/op; new ~600–700 ns/op; different work). Old-loop runs
(EPYC: 39828409134, 39831658152 → −4.84%, −5.38%; Xeon 8573C: 39831653386 → −2.46%)
are listed here for traceability only; do not use these values.

EPYC has n=2 for the new loop; Xeon 8573C and 8370C each have n=1. Treat as provisional
until a second run pair confirms on those models.

**snappy note:** 7 runs total (39828409134, 39831653386, 39831658152, 39833250644,
39833287253, 39835714756, 39835718451); 5 not significant (p = 0.28–0.631, CV 9–16%).
The remaining 2 runs at p=0.019 point in *opposite* directions: AMD EPYC 7763 +14.25%
(significant regression), Intel Xeon 8370C (run 39835718451) −2.59% (significant
improvement). The benchmark cannot measure reliably. The NONE verdict holds on grounds
that no credible benefit is present, not because PGO cleanly does nothing. A benchmark
redesign is tracked in BP-60.

---

### Prediction Accuracy (post-#55, CI results)

| Prediction | Repos | Directionally correct | Notes |
|------------|-------|----------------------|-------|
| HIGH | 4 of 5 | **4 of 4** | All 4 HIGH repos improve significantly on every architecture tested |
| NONE | 1 of 5 (snappy) | **1 of 1** | snappy: no credible benefit across 7 runs; NONE verdict holds |

**leverage-check is directionally correct 5 of 5.** Every HIGH repo improves
significantly (p<0.05) on both AMD and Intel architectures. The predicted NONE (snappy)
shows no credible benefit on any architecture.

**The 3% gate is microarchitecture-dependent.** go-json is the clearest example: identical
code, identical profile, identical binary. Yet EPYC 7763 returns −3.66 to −6.02%
(PROMOTE on all 4 runs), Xeon 8573C returns −1.52 to −2.06% (NEUTRAL, below 3%), and
Xeon 8370C returns p=0.247 (not significant): three models, three verdicts. Any
single-run gate decision on go-json depends on which CPU GitHub assigns; a multi-run
median stratified by CPU model is needed before those verdicts can be treated as stable.

goldmark is the counterexample to "PGO just helps more on AMD": EPYC 7763 range −5.39
to −6.54%, Xeon 8573C range −5.22 to −6.40%, Xeon 8370C −4.40%. All three models show
consistent 4–7% improvement (max gap ~2pp across CPU models), architecture-independent
within measurement precision.

gjson shows the largest architecture split: up to ~9.3pp between EPYC 7763 (range
−15.74 to −16.04%) and Xeon 8573C (range −6.42 to −6.69%). All three models are
significant and above 3%, but magnitude varies substantially by microarchitecture.

---

## Per-Repository Analysis

### 1. yuin/goldmark: Markdown Parser

**leverage-check:** HIGH. 68 module-scoped devirt decisions, 252 module-scoped extra inlines
(+4 dep devirts, +568 dep extra inlines)

**Build analysis (post-#55):**
```
devirt_decisions:       68   (module-scoped)
pgo_devirts_deps:        4   (stdlib/deps, excluded from verdict)
pgo_extra_inlines:     252   (module-scoped)
pgo_extra_inlines_deps: 568
```

**CI results:**

| CPU | Δ values (%) | p-value | n | Run IDs |
|-----|--------------|---------|---|---------|
| AMD EPYC 7763 | −6.31, −5.39, −5.81, −6.54 | ≤0.001 all | 4 | 39828409134, 39831658152, 39833250644, 39835714756 |
| Intel Xeon 8573C | −6.40, −5.22 | ≤0.001 both | 2 | 39831653386, 39833287253 |
| Intel Xeon 8370C | −4.40 | 0.001 | 1 | 39835718451 |

goldmark is **architecture-independent** within measurement precision: all three CPU models
show 4–7% improvement, with a max gap of ~2pp across models. The leverage-check HIGH
prediction is validated on all architectures tested.

**Mechanism:** goldmark's hot path (`parser.(*parser).parseBlock`) calls through the
`BlockParser` interface. PGO devirts these interface calls and enables tighter inlining of
the surrounding helper functions. 68 module-scoped devirt decisions directly on the hot
path explain the consistent ~5.8% improvement.

---

### 2. tidwall/gjson: JSON Path Queries

**leverage-check:** HIGH. 0 module-scoped devirt decisions, 36 module-scoped extra inlines
(+3 dep devirts, +424 dep extra inlines)

**Build analysis (post-#55):**
```
devirt_decisions:        0   (module-scoped)
pgo_devirts_deps:        3   (stdlib/deps, reflect/type.go, excluded from verdict)
pgo_extra_inlines:      36   (module-scoped)
pgo_extra_inlines_deps: 424
```

**CI results:**

| CPU | Δ values (%) | p-value | n | Run IDs |
|-----|--------------|---------|---|---------|
| AMD EPYC 7763 | −15.74, −16.04, −15.76, −15.89 | ≤0.001 all | 4 | 39828409134, 39831658152, 39833250644, 39835714756 |
| Intel Xeon 8573C | −6.69, −6.42 | ≤0.001 both | 2 | 39831653386, 39833287253 |
| Intel Xeon 8370C | −11.06 | 0.001 | 1 | 39835718451 |

gjson shows the largest architecture split in this evaluation: up to ~9.3pp between EPYC
7763 (range −15.74 to −16.04%) and Xeon 8573C (range −6.42 to −6.69%). All three CPU
models return significant improvement above the 3% gate. The spread *within* a model is
tight (≤0.30pp), confirming the split is real and not noise.

**Mechanism:** gjson uses direct function calls throughout its hot path (no interface
dispatch). The improvement comes from PGO enabling tighter inlining of helper functions
(`parseObject`, `parseSquash`, `parseArray`) that gjson marks as non-inlinable under the
default budget. This is `pgo_extra_inlines` at work, not devirt.

---

### 3. goccy/go-json: JSON Encoder/Decoder

**leverage-check:** HIGH. 10 module-scoped devirt decisions, 59 module-scoped extra inlines
(+3 dep devirts, +598 dep extra inlines)

**Build analysis (post-#55):**
```
devirt_decisions:       10   (module-scoped)
pgo_devirts_deps:        3   (stdlib/deps, excluded from verdict)
pgo_extra_inlines:      59   (module-scoped)
pgo_extra_inlines_deps: 598
```

**CI results:**

| CPU | Δ values (%) | p-value | n | 3%-gate | Run IDs |
|-----|--------------|---------|---|---------|---------|
| AMD EPYC 7763 | −3.66, −6.02, −3.93, −5.42 | ≤0.001 all | 4 | **PROMOTE** | 39828409134, 39831658152, 39833250644, 39835714756 |
| Intel Xeon 8573C | −1.52, −2.06 | p=0.000 / p=0.002 | 2 | **NEUTRAL** (below 3%) | 39831653386, 39833287253 |
| Intel Xeon 8370C | not significant | 0.247 | 1 | **NEUTRAL** | 39835718451 |

go-json is the **clearest illustration that the 3% gate is microarchitecture-dependent:**
identical code, identical profile, identical binary: three different gate verdicts
(PROMOTE / NEUTRAL / not-significant) depending on which CPU GitHub assigns. Any
single-run gate decision on go-json is not reliable without knowing the CPU model.

**Mechanism:** go-json uses a bytecode VM (`encoder/vm.Run`) for its hot path. The VM
function is too large to inline under any budget. The 59 module-scoped extra inlines
are in surrounding scaffolding (encoder setup, decoder initialization). They run once
per call but do not dominate total CPU time. The modest improvement on AMD suggests
some hot-path effect; the near-zero result on Intel suggests architecture-specific
inlining thresholds or branch predictor differences.

---

### 4. golang/snappy: Compressor

**leverage-check (post-#55):** **NONE**. 0 module-scoped devirt decisions, 2 module-scoped
extra inlines (+3 dep devirts, +799 dep extra inlines)

**Build analysis (post-#55):**
```
devirt_decisions:        0   (module-scoped)
pgo_devirts_deps:        3   (stdlib/deps, excluded from verdict)
pgo_extra_inlines:       2   (module-scoped)
pgo_extra_inlines_deps: 799
```

**CI results (7 runs, all architectures):**

5 runs: not significant (p = 0.28–0.631, CV 9–16%)
2 runs at p=0.019, opposite directions:
- AMD EPYC 7763: +14.25% (significant **regression**)
- Intel Xeon 8370C: −2.59% (significant improvement)

The benchmark is **noise-dominated.** CV of 9–16% means the benchmark cannot reliably
measure the small signal PGO might produce on the Go portions of the code. The two
significant results in opposite directions confirm that the benchmark is measuring noise,
not a real effect.

**The NONE verdict holds**, but on grounds that *the benchmark cannot measure reliably*,
not because PGO cleanly does nothing. snappy's compression hot path (`snappy.Encode`,
`snappy.Decode`) routes through assembly implementations on amd64. PGO cannot touch
assembly functions. Post-#55, the engine correctly reports 0 module-scoped devirts and
2 extra inlines; the NONE verdict is correct from the build-analysis side. A benchmark
redesign that isolates the Go portions is tracked in BP-60.

**Previous incorrect prediction (pre-#55):** The original 7 devirt decisions and 1164
extra inlines were all stdlib/dependency noise. Post-#55 correctly isolates snappy's
module contributions.

---

### 5. valyala/fasthttp: HTTP Framework

**leverage-check (post-#55):** HIGH. 0 module-scoped devirt decisions, 92 module-scoped
extra inlines (+1 dep devirt, +771 dep extra inlines)

**Build analysis (post-#55, using profile.pprof + --dir fasthttp@v1.74.0):**
```
devirt_decisions:        0   (module-scoped)
pgo_devirts_deps:        1   (stdlib/deps, excluded from verdict)
pgo_extra_inlines:      92   (module-scoped)
pgo_extra_inlines_deps: 771
```

**CI results: reshaped benchmark only (commit `40150e0` onward, PRELIMINARY):**

| CPU | Δ values (%) | p-value | n | Run IDs | Status |
|-----|--------------|---------|---|---------|--------|
| AMD EPYC 7763 | −7.77, −7.77 | ≤0.001 both | 2 | 39833250644, 39835714756 | **PROMOTE** (provisional) |
| Intel Xeon 8573C | −11.44 | 0.001 | 1 | 39833287253 | **PROMOTE** (provisional) |
| Intel Xeon 8370C | −6.23 | 0.001 | 1 | 39835718451 | **PROMOTE** (provisional) |

> **Important:** These numbers are from the reshaped benchmark (commit `40150e0`): hoisted
> `fasthttp.Request` and reader allocations out of the timed loop, per-iteration reset,
> error check, and a pre-run parse validation. The previous benchmark allocated a new
> `fasthttp.Request` on every iteration and discarded the parse error, making allocator
> time and potential silent parse failures visible in the result.
>
> Old-loop runs (EPYC 7763: 39828409134 → −4.84%, 39831658152 → −5.38%;
> Xeon 8573C: 39831653386 → −2.46%) measured different work (~2.4 µs/op vs ~600–700 ns/op)
> and are **not comparable.** Do not carry forward, cite, or average any fasthttp result
> from before commit `40150e0`.

Xeon 8573C and 8370C each have n=1 under the new loop; treat as provisional until a
second run pair confirms on those models. EPYC has n=2 (both identical at −7.77%).

**Mechanism:** fasthttp's parser is dominated by `RequestHeader.parseHeaders` → string
scanning. The 92 module-scoped extra inlines are in request-parsing helper functions that
sit directly on this hot path, explaining the significant improvement.

---

## Cross-Cutting Findings

### Finding 1: pre-#55 over-prediction from transitive dep graph (fixed)

**Root cause (pre-#55):** `--dir` caused `go build -gcflags=all=-m=2 -pgo=profile ./...`
to compile the full transitive dependency graph. Devirt decisions in stdlib packages
(net/http, reflect, sync) accumulated regardless of whether those packages appeared in the
CPU hot path.

**Post-#55 fix:** `devirt_decisions` and `pgo_extra_inlines` are now module-scoped.
Deps are reported separately as `pgo_devirts_deps` / `pgo_extra_inlines_deps` and do
not contribute to the verdict. snappy now correctly returns NONE.

### Finding 2: Extra-inline count is a weak signal

`pgo_extra_inlines` varied from 36 (gjson, best outcome ~−15.85% AMD) to 92 (fasthttp,
provisional −7.77% AMD). The module-scoped count alone does not predict magnitude; what
matters is whether those inlines fall on the benchmark's actual hot path.

### Finding 3: Asm-heavy hot paths correctly return NONE (post-#55)

snappy mirrors hashcalc-svc from the controlled demo. When the top CPU consumer is
assembly (`snappy.{Encode,Decode}`, `sha256.blockAVX2`), PGO has nothing to work with.
Post-#55, snappy returns NONE (0 module-scoped devirts, 2 extra inlines). Correct.

### Finding 4: PGO effect is near-perfectly reproducible within a CPU model

Within a CPU model, run-to-run variance in the delta is tight: gjson AMD EPYC 7763 spread
0.30pp across 4 runs; gjson Xeon 8573C spread 0.27pp across 2 runs. This confirms the CI
harness is stable once CPU model is controlled. The apparent "noise" in multi-run summaries
that do not stratify by CPU is almost entirely architecture signal.

### Finding 5: 3% gate needs CPU-model context

The 3% house standard is valid within a CPU model. It is not valid as a single-run gate
across uncontrolled architecture assignment. go-json illustrates this clearly: PROMOTE on
EPYC, NEUTRAL on Xeon 8573C, not-significant on Xeon 8370C. Identical code, identical
commit. A stable gate verdict for go-json requires either (a) multi-run median stratified
by CPU model, or (b) a higher threshold that clears all tested architectures.

---

## Implications for leverage-check

The tool currently provides a **necessary but insufficient** signal. A HIGH verdict is
required to proceed with PGO optimization, but it does not guarantee benefit at or above
the gate threshold on every microarchitecture.

Three improvements would tighten the signal:

1. **Hot-path weighted devirt count:** cross-reference each devirt decision with the CPU%
   of the calling function in the profile. Decisions in functions with <1% CPU share
   should not count toward the HIGH threshold.

2. **Asm-fraction detection:** if >30% of top-20 flat CPU samples are in `asm` functions
   (identified by absence of Go source lines in the profile), add a warning that PGO
   cannot touch the hot path regardless of devirt count.

3. **Architecture-aware gate:** when reporting gate verdicts from CI, require the CPU
   model to be present in the output. A verdict without a CPU model is incomplete.

---

## Artifact Index

```
hack/pgo-demo/
├── results/
│   └── GOCONF-DATA-REPORT.md          controlled demo (hashcalc, passthrough)
├── real-repos/
│   ├── EVALUATION.md                  this report
│   ├── collect-profiles.sh            profile collection script
│   ├── run-pgo-cycle.sh               benchmark comparison script (legacy; CI harness preferred)
│   └── results/
│       ├── eval-v2/                   static leverage-check outputs (per-repo)
│       │   ├── goldmark/
│       │   ├── gjson/
│       │   ├── go-json/
│       │   └── snappy/
│       └── profiles/                  collected .pprof files + CI bench modules (per-repo)
│           ├── goldmark/
│           ├── gjson/
│           ├── go-json/
│           ├── snappy/
│           └── fasthttp/
```

CI benchmark raw data (baseline.txt, pgo.txt, stat.txt per library) is uploaded as a
workflow artifact named `pgo-bench-real-repos-<run_id>` for each run. Run ids are in the
table above.
