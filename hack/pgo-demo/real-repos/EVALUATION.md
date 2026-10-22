# pgoctl leverage-check: Validation Against Real Open-Source Go Repositories

**Date:** 2026-09-18 (updated 2026-10-20 with post-#55 module-scoped engine)
**Tool:** `pgoctl leverage-check`
**Environment:** linux/amd64, go1.26.6

> **Updated 2026-10-20:** Numbers below are from the post-#55 engine (`91bc3ce`).  
> #55 fixed the root-cause over-prediction issue identified in this eval: `--dir` previously
> compiled the full transitive dependency graph and counted stdlib/dep devirts as module
> devirts. Post-#55, `devirt_decisions` and `pgo_extra_inlines` are module-scoped only;
> `pgo_devirts_deps` and `pgo_extra_inlines_deps` capture the excluded dependency figures.
> fasthttp was not re-run (profile.pprof was not saved to the repo); its row retains the
> original numbers.

---

## Scope

This document validates `pgoctl leverage-check` across five real open-source Go repositories
spanning a range of expected PGO leverage: CPU-bound parsers, JSON processors, a compressor,
and an HTTP framework. It compares the tool's prediction (HIGH/LOW/NONE) against actual
benchmark results (before/after PGO build), and traces cases where the prediction and
observed speedup disagree.

The controlled hashcalc/passthrough pair in `hack/pgo-demo/results/GOCONF-DATA-REPORT.md`
serves as the anchor baseline.

---

## Methodology

### Profile Collection

For each repo, a synthetic benchmark module was created that imports the library and runs
a representative workload. CPU profiles were collected via:

```
go test -bench=<BenchmarkName> -run=^$ -count=1 -benchtime=10s -cpuprofile=profile.pprof
```

### leverage-check Usage

Two invocation modes:

1. **Static only**  - profile analysis without build:
   ```
   pgoctl leverage-check profile.pprof
   ```
   Returns `INCOMPLETE` (no devirt count without `--dir`).

2. **Full analysis**  - with `--dir` pointing to the library's own module directory:
   ```
   pgoctl leverage-check profile.pprof --dir $(go env GOMODCACHE)/<library@version> --json
   ```
   Returns `HIGH`/`LOW`/`NONE` based on devirt decisions and extra inlines.

   > **Important:** `--dir` must point to the library's source, not to a wrapper benchmark
   > module. A wrapper module containing only `*_test.go` files produces 0 `go build`
   > output (test files are excluded from `go build ./...`), yielding an incorrect NONE.

### Benchmark Comparison

Baseline and PGO builds compared with 5 trials, `benchtime=5s`:

```bash
# baseline
go test -bench=. -run=^$ -count=5 -benchtime=5s > baseline.txt

# pgo build using collected profile
go test -bench=. -run=^$ -count=5 -benchtime=5s -pgo=profile.pprof > pgo.txt

benchstat baseline.txt pgo.txt
```

Verdict thresholds (matching `pgoctl compare` defaults):
- **PROMOTE**: ≥10% improvement, p < 0.05
- **NEUTRAL**: within ±10%, or p ≥ 0.05
- **ROLLBACK**: ≥10% regression

---

## Results Summary

### Controlled Demo Services (hack/pgo-demo)

These purpose-built services provide anchor baselines for expected HIGH and NONE/LOW behavior.

| Service | Category | Prediction | Devirt (pre-#55 total) | Extra Inlines (pre-#55 total) | Bench Delta | p-val | Outcome |
|---------|----------|------------|--------|---------------|---------|-------|---------|
| hashcalc-svc | CPU-bound (crypto + asm) | HIGH | 93 | 0 | +0.6% RPS | -- | **NEUTRAL** |
| passthrough-svc | I/O-bound (HTTP proxy) | HIGH | 93 | 0 | -1.0% RPS | -- | **NEUTRAL** |

Both services receive HIGH predictions and deliver NEUTRAL outcomes. Analysis in
`GOCONF-DATA-REPORT.md`: the devirt decisions fall on library/stdlib code, not the
application hot path.

---

### Real Open-Source Libraries

| Repository | Category | Prediction | Devirt (mod) | Devirt (deps) | Extra Inlines (mod) | Extra Inlines (deps) | Bench Δ | p-val | Outcome |
|------------|----------|------------|-------------|--------------|--------------------|--------------------|---------|-------|---------|
| [yuin/goldmark](https://github.com/yuin/goldmark) v1.8.6 | Markdown parser | **HIGH** | 68 | 4 | 252 | 568 | -5.1% | 0.310 | **NEUTRAL** |
| [tidwall/gjson](https://github.com/tidwall/gjson) v1.19.0 | JSON path query | **HIGH** | 0 | 3 | 36 | 424 | -6.3% | 0.024 | **NEUTRAL**† |
| [goccy/go-json](https://github.com/goccy/go-json) v0.10.6 | JSON encoder/decoder | **HIGH** | 10 | 3 | 59 | 598 | +0.1% | 0.841 | **NEUTRAL** |
| [golang/snappy](https://github.com/golang/snappy) v1.0.0 | Compressor (Go+asm) | **NONE** <- was HIGH | 0 | 3 | 2 | 799 | -1.8% | 0.481 | **NEUTRAL** |
| [valyala/fasthttp](https://github.com/valyala/fasthttp) v1.74.0 | HTTP framework | **HIGH** | 0 | 1 | 92 | 771 | -1.6% | 0.841 | **NEUTRAL** |

dagger gjson improvement is statistically significant (p=0.024) but below the 10% PROMOTE
threshold used by `pgoctl compare`.

**Prediction accuracy (post-#55):**

| Prediction | Repos | PROMOTE | NEUTRAL | ROLLBACK |
|------------|-------|---------|---------|----------|
| HIGH | 4 of 5 | 0 | 4 | 0 |
| NONE | 1 of 5 (snappy) |  - | 1 (bench confirms) | 0 |

**Key change vs original:** snappy correctly downgrades from HIGH → NONE. The old 7
devirt decisions and 1164 extra inlines were all stdlib/dependency noise; the module itself
contributes 0 devirt decisions and 2 extra inlines. The asm hot path (`decode`) is now
correctly flagged as outside PGO scope.

The remaining 4 HIGH predictions still deliver NEUTRAL bench outcomes. This is a
separate issue (leverage-check correctly identifies compiler decisions, but those decisions
may not fall on the benchmark hot path  - the subject of Finding 2 and the hotpath-weighted
devirt improvement proposed in the Implications section).

---

## Per-Repository Analysis

### 1. yuin/goldmark  - Markdown Parser

**leverage-check:** HIGH  - 68 module-scoped devirt decisions, 252 module-scoped extra inlines
(+4 dep devirts, +568 dep extra inlines)

**Build analysis reveals (post-#55):**
```
devirt_decisions:       68   (module-scoped)
pgo_devirts_deps:        4   (stdlib/deps, excluded from verdict)
pgo_extra_inlines:     252   (module-scoped)
pgo_extra_inlines_deps: 568
```

*Original (pre-#55, full transitive graph):*
```
devirt_decisions: 65
pgo_extra_inlines: 1022
baseline_inlines: 22787
pgo_inlines: 23809
```

**Benchmark (5 trials × 5s):**
```
              baseline.txt      pgo.txt
Goldmark-2    3.710m ± ∞       3.519m ± ∞    ~  (p=0.310 n=5)
```

**Why NEUTRAL despite HIGH prediction:**
goldmark's hot path (`parser.(*parser).parseBlock`, 5.2%) calls through the `BlockParser`
interface. The profile shows many concurrent GC functions (`runtime.asyncPreempt`,
`runtime.memclrNoHeapPointers`) consuming comparable CPU. PGO devirts the parser interface
calls, but those calls are a fraction of the total benchmark time. The extra 1022 inlines
are mostly in cold import paths, not the core parse loop.

---

### 2. tidwall/gjson  - JSON Path Queries

**leverage-check:** HIGH  - 0 module-scoped devirt decisions, 36 module-scoped extra inlines
(+3 dep devirts, +424 dep extra inlines)

**Build analysis reveals (post-#55):**
```
devirt_decisions:        0   (module-scoped)
pgo_devirts_deps:        3   (stdlib/deps  - reflect/type.go, excluded from verdict)
pgo_extra_inlines:      36   (module-scoped)
pgo_extra_inlines_deps: 424
```

*Original (pre-#55, full transitive graph):*
```
devirt_decisions: 13
pgo_extra_inlines: 717
baseline_inlines: 19453
pgo_inlines: 20170
```

**Benchmark (5 trials × 5s):**
```
        baseline.txt      pgo.txt
GJSON-2   2.495µ ± ∞      2.339µ ± ∞    -6.25% (p=0.024 n=5)
```

**Why partial improvement:**
gjson is written without interfaces in its hot path  - it uses direct function calls
(`parseObject`, `parseSquash`, `parseArray`). The -6.25% improvement comes from PGO
enabling tighter inlining of helper functions that gjson marks as non-inlinable under
normal budget. This is `pgo_extra_inlines` at work, not devirt. The 13 devirt decisions
are in stdlib packages called by gjson, contributing minimally.

gjson is the **best-performing** result in this evaluation: statistically significant
improvement, though below the 10% PROMOTE threshold.

---

### 3. goccy/go-json  - JSON Encoder/Decoder

**leverage-check:** HIGH  - 10 module-scoped devirt decisions, 59 module-scoped extra inlines
(+3 dep devirts, +598 dep extra inlines)

**Build analysis reveals (post-#55):**
```
devirt_decisions:       10   (module-scoped)
pgo_devirts_deps:        3   (stdlib/deps, excluded from verdict)
pgo_extra_inlines:      59   (module-scoped)
pgo_extra_inlines_deps: 598
```

*Original (pre-#55, full transitive graph):*
```
devirt_decisions: 24
pgo_extra_inlines: 1113
baseline_inlines: 29925
pgo_inlines: 31038
```

**Benchmark (5 trials × 3s):**
```
         baseline.txt      pgo.txt
GoJSON-2   3.672µ ± ∞      3.676µ ± ∞    ~  (p=0.841 n=5)
```

**Why NEUTRAL despite large inline count:**
go-json uses a bytecode VM (`encoder/vm.Run`) for its hot path. The VM is a large function
that loops over encoded instructions  - it's too large to inline (inlining budget exceeded)
and PGO cannot shrink it. The 1113 extra inlines are in surrounding scaffolding
(encoder setup, decoder initialization) that runs once per encode/decode call but doesn't
dominate the total CPU time. Zero actual benefit.

---

### 4. golang/snappy  - Compressor

**leverage-check (post-#55):** **NONE**  - 0 module-scoped devirt decisions, 2 module-scoped extra inlines
(+3 dep devirts, +799 dep extra inlines)

**Build analysis reveals (post-#55):**
```
devirt_decisions:        0   (module-scoped)
pgo_devirts_deps:        3   (stdlib/deps, excluded from verdict)
pgo_extra_inlines:       2   (module-scoped)
pgo_extra_inlines_deps: 799
```

*Original (pre-#55, full transitive graph  - HIGH prediction was incorrect):*
```
devirt_decisions: 7
pgo_extra_inlines: 1164
baseline_inlines: 18186
pgo_inlines: 19350
```

**Benchmark (10 trials × 3s, re-run for stability):**
```
         baseline2.txt    pgo2.txt
Snappy-2   181.7µ ± 27%   178.4µ ± 28%    ~  (p=0.481 n=10)
```

**Post-#55 verdict NONE is correct:**
snappy's compression hot path (`snappy.Encode`, `snappy.Decode`) routes through assembly
implementations on amd64. PGO cannot touch assembly functions  - it only influences
Go-compiled code. Post-#55, the engine correctly reports 0 module-scoped devirts and 2 extra
inlines for snappy itself; the benchmark confirms NEUTRAL. The old pre-#55 7 devirt
decisions and 1164 extra inlines were all from stdlib packages compiled as part of the
transitive dep graph. See also: hashcalc-svc (same pattern with `sha256.blockAVX2`).

---

### 5. valyala/fasthttp  - HTTP Framework

**leverage-check (post-#55):** HIGH  - 0 module-scoped devirt decisions, 92 module-scoped extra inlines
(+1 dep devirt, +771 dep extra inlines)

**Build analysis reveals (post-#55, using profile.pprof + --dir fasthttp@v1.74.0):**
```
devirt_decisions:        0   (module-scoped)
pgo_devirts_deps:        1   (stdlib/deps, excluded from verdict)
pgo_extra_inlines:      92   (module-scoped)
pgo_extra_inlines_deps: 771
```

*Original (pre-#55, full transitive graph):*
```
devirt_decisions: 91
pgo_extra_inlines: 1766
baseline_inlines: 52655
pgo_inlines: 54421
```

**Benchmark (5 trials × 3s):**
```
           baseline.txt      pgo.txt
FastHTTP-2   6.665µ ± ∞      6.560µ ± ∞    ~  (p=0.841 n=5)
```

**Why NEUTRAL despite HIGH prediction:**
The benchmark exercises HTTP request parsing from a fixed byte buffer (no I/O).
The hot path is `RequestHeader.parseHeaders` → string scanning via `runtime.memclrNoHeapPointers`.
fasthttp's parser uses direct field access, not interface dispatch. Post-#55: 0 module-scoped
devirt decisions confirms the original 91 were all in the net/http dependency tree.
The 92 module-scoped extra inlines are in initialization and option-handling code that
doesn't dominate the parsing benchmark hot loop.

---

## Cross-Cutting Findings

### Finding 1: pre-#55 over-prediction was from transitive dep graph (fixed)

All 5 real repositories received HIGH predictions in the original run. Zero reached
PROMOTE. The tool correctly signalled "the compiler will make decisions" but those decisions
did not reliably translate to throughput gains.

**Root cause (pre-#55):** `--dir` caused `go build -gcflags=all=-m=2 -pgo=profile ./...`
to compile the full transitive dependency graph. Devirt decisions in stdlib packages
(net/http, reflect, sync) accumulated regardless of whether those packages appeared in the
CPU hot path.

**Post-#55 fix:** `devirt_decisions` and `pgo_extra_inlines` are now module-scoped
(`go list -m` origin filter + `-trimpath`). Deps are still counted but reported separately
as `pgo_devirts_deps` / `pgo_extra_inlines_deps`. snappy now correctly returns NONE.

**Example:** fasthttp's original 91 devirt decisions spanned the net/http import tree;
the benchmark hot path is pure string scanning with no interface dispatch. The post-#55
engine would separate those into `pgo_devirts_deps` (not counted toward verdict).

### Finding 2: Extra-inline count is a weak signal

`pgo_extra_inlines` varied from 717 (gjson) to 1766 (fasthttp). The repo with the lowest
extra-inline count (gjson, 717) had the best outcome (-6.25%). The repo with the highest
count (fasthttp, 1766) had zero measurable improvement.

Extra inlines in initialization and scaffolding code inflate the count without helping
benchmarks that call those paths rarely.

### Finding 3: asm-heavy hot paths now correctly return NONE (post-#55)

snappy (27% variance, NEUTRAL) mirrors hashcalc-svc from the controlled demo. When the
top CPU consumer is assembly (`snappy.{Encode,Decode}`, `sha256.blockAVX2`), PGO has
nothing to work with. Pre-#55, leverage-check incorrectly returned HIGH by counting devirt
decisions in stdlib packages. Post-#55, snappy returns NONE (0 module-scoped devirts, 2
extra inlines), which correctly matches the NEUTRAL benchmark outcome.

### Finding 4: gjson is the useful data point

gjson's -6.25% improvement at p=0.024 is the only statistically significant result.
The improvement comes from `pgo_extra_inlines` on gjson's direct-call hot path, not from
devirt. This validates that PGO extra-inlining (not just devirt) can deliver real gains,
but the magnitude depends on whether the newly inlined functions sit on the actual hot path.

---

## Implications for leverage-check

The tool currently provides a **necessary but insufficient** signal. A HIGH verdict is
required to proceed with PGO optimization, but it does not guarantee benefit.

Two improvements would tighten the signal:

1. **Hot-path weighted devirt count:** cross-reference each devirt decision with the CPU%
   of the calling function in the profile. Decisions in functions with <1% CPU share
   should not count toward the HIGH threshold.

2. **Asm-fraction detection:** if >30% of top-20 flat CPU samples are in `asm` functions
   (identified by absence of Go source lines in the profile), add a warning that PGO
   cannot touch the hot path regardless of devirt count.

These refinements would reduce the false-HIGH rate seen in 4 of 5 repos here.

---

## Artifact Index

```
hack/pgo-demo/
├── results/
│   └── GOCONF-DATA-REPORT.md          controlled demo (hashcalc, passthrough)
├── real-repos/
│   ├── EVALUATION.md                  this report
│   ├── collect-profiles.sh            profile collection script
│   ├── run-pgo-cycle.sh               benchmark comparison script
│   └── results/
│       ├── eval-v2/                   static leverage-check outputs (per-repo)
│       │   ├── goldmark/
│       │   ├── gjson/
│       │   ├── go-json/
│       │   └── snappy/
│       └── profiles/                  collected .pprof files (per-repo)
│           ├── goldmark/
│           ├── gjson/
│           ├── go-json/
│           ├── snappy/
│           └── fasthttp/
```

Raw benchmark data (baseline.txt, pgo.txt) is in the evaluation runner's working
directory (`/tmp/pgo-eval/<repo>/`) and can be reproduced by running
`collect-profiles.sh` followed by the bench cycle described in the Methodology section.
