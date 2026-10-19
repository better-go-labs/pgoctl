# pgoctl leverage-check: Validation Against Real Open-Source Go Repositories

**Date:** 2026-09-18
**Tool:** `pgoctl leverage-check`
**Environment:** linux/amd64, go1.26.6

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

1. **Static only** — profile analysis without build:
   ```
   pgoctl leverage-check profile.pprof
   ```
   Returns `INCOMPLETE` (no devirt count without `--dir`).

2. **Full analysis** — with `--dir` pointing to the library's own module directory:
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

| Service | Category | Prediction | Devirt | Extra Inlines | Bench Δ | p-val | Outcome |
|---------|----------|------------|--------|---------------|---------|-------|---------|
| hashcalc-svc | CPU-bound (crypto + asm) | HIGH | 93 | 0 | +0.6% RPS | — | **NEUTRAL** |
| passthrough-svc | I/O-bound (HTTP proxy) | HIGH | 93 | 0 | -1.0% RPS | — | **NEUTRAL** |

Both services receive HIGH predictions and deliver NEUTRAL outcomes. Analysis in
`GOCONF-DATA-REPORT.md`: the devirt decisions fall on library/stdlib code, not the
application hot path.

---

### Real Open-Source Libraries

| Repository | Category | Prediction | Devirt | Extra Inlines | Bench Δ | p-val | Outcome |
|------------|----------|------------|--------|---------------|---------|-------|---------|
| [yuin/goldmark](https://github.com/yuin/goldmark) v1.8.6 | Markdown parser | **HIGH** | 65 | 1022 | -5.1% | 0.310 | **NEUTRAL** |
| [tidwall/gjson](https://github.com/tidwall/gjson) v1.19.0 | JSON path query | **HIGH** | 13 | 717 | -6.3% | 0.024 | **NEUTRAL**† |
| [goccy/go-json](https://github.com/goccy/go-json) v0.10.6 | JSON encoder/decoder | **HIGH** | 24 | 1113 | +0.1% | 0.841 | **NEUTRAL** |
| [golang/snappy](https://github.com/golang/snappy) v1.0.0 | Compressor (Go+asm) | **HIGH** | 7 | 1164 | -1.8% | 0.481 | **NEUTRAL** |
| [valyala/fasthttp](https://github.com/valyala/fasthttp) v1.74.0 | HTTP framework | **HIGH** | 91 | 1766 | -1.6% | 0.841 | **NEUTRAL** |

† gjson improvement is statistically significant (p=0.024) but below the 10% PROMOTE
threshold used by `pgoctl compare`.

**Prediction accuracy:**

| Prediction | Repos | PROMOTE | NEUTRAL | ROLLBACK |
|------------|-------|---------|---------|----------|
| HIGH | 5 of 5 | 0 | 5 | 0 |

---

## Per-Repository Analysis

### 1. yuin/goldmark — Markdown Parser

**leverage-check:** HIGH — 65 devirt decisions, 1022 extra inlines

**Build analysis reveals:**
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

### 2. tidwall/gjson — JSON Path Queries

**leverage-check:** HIGH — 13 devirt decisions, 717 extra inlines

**Build analysis reveals:**
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
gjson is written without interfaces in its hot path — it uses direct function calls
(`parseObject`, `parseSquash`, `parseArray`). The -6.25% improvement comes from PGO
enabling tighter inlining of helper functions that gjson marks as non-inlinable under
normal budget. This is `pgo_extra_inlines` at work, not devirt. The 13 devirt decisions
are in stdlib packages called by gjson, contributing minimally.

gjson is the **best-performing** result in this evaluation: statistically significant
improvement, though below the 10% PROMOTE threshold.

---

### 3. goccy/go-json — JSON Encoder/Decoder

**leverage-check:** HIGH — 24 devirt decisions, 1113 extra inlines

**Build analysis reveals:**
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
that loops over encoded instructions — it's too large to inline (inlining budget exceeded)
and PGO cannot shrink it. The 1113 extra inlines are in surrounding scaffolding
(encoder setup, decoder initialization) that runs once per encode/decode call but doesn't
dominate the total CPU time. Zero actual benefit.

---

### 4. golang/snappy — Compressor

**leverage-check:** HIGH — 7 devirt decisions, 1164 extra inlines

**Build analysis reveals:**
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

**Why NEUTRAL despite HIGH prediction:**
snappy's compression hot path (`snappy.Encode`, `snappy.Decode`) routes through assembly
implementations on amd64. PGO cannot touch assembly functions — it only influences
Go-compiled code. The 7 devirt decisions are in the Go error-handling wrapper around the
asm calls. High benchmark variance (CV ≈ 27%) from GC and memory allocation dominated
by the encode/decode buffer lifecycle. See also: hashcalc-svc (same pattern with
`sha256.blockAVX2`).

---

### 5. valyala/fasthttp — HTTP Framework

**leverage-check:** HIGH — 91 devirt decisions, 1766 extra inlines

**Build analysis reveals:**
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

**Why NEUTRAL despite highest devirt count (91):**
The benchmark exercises HTTP request parsing from a fixed byte buffer (no I/O).
The hot path is `RequestHeader.parseHeaders` → `headerScanner.next` → string scanning via
`runtime.memclrNoHeapPointers`. fasthttp's parser uses direct field access, not interface
dispatch. The 91 devirt decisions are in the net/http dependency tree and error-handling
paths, not in the benchmark hot loop. Large extra-inline count (1766) concentrates in
the framework's initialization and option-handling code.

---

## Cross-Cutting Findings

### Finding 1: leverage-check consistently over-predicts HIGH

All 5 real repositories received HIGH predictions. Zero reached PROMOTE under standard
benchmark conditions. The tool correctly signals "the compiler will make decisions" but
those decisions do not reliably translate to throughput gains.

**Root cause:** `--dir` causes `go build -gcflags=all=-m=2 -pgo=profile ./...` to compile
the full transitive dependency graph. Devirt decisions in stdlib packages (net/http, sync,
runtime) accumulate regardless of whether those packages appear in the CPU hot path.

**Example:** fasthttp's 91 devirt decisions span the net/http import tree;
the benchmark hot path is pure string scanning with no interface dispatch.

### Finding 2: Extra-inline count is a weak signal

`pgo_extra_inlines` varied from 717 (gjson) to 1766 (fasthttp). The repo with the lowest
extra-inline count (gjson, 717) had the best outcome (-6.25%). The repo with the highest
count (fasthttp, 1766) had zero measurable improvement.

Extra inlines in initialization and scaffolding code inflate the count without helping
benchmarks that call those paths rarely.

### Finding 3: asm-heavy hot paths are invisible to PGO

snappy (27% variance, NEUTRAL) mirrors hashcalc-svc from the controlled demo. When the
top CPU consumer is assembly (`snappy.{Encode,Decode}`, `sha256.blockAVX2`), PGO has
nothing to work with. leverage-check does not detect this case — it counts devirt
decisions in the rest of the build regardless.

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
