# Benchmarks

> Numbers populated starting D4 (Aug 1) once the PGO build + comparison runs.

## Setup

- *Demo service:* Prometheus (latest stable) deployed via kube-prometheus-stack on kind
- *Cluster:* kind single-node (4 CPU, 8 GB RAM)
- *Load generator:* [hey](https://github.com/rakyll/hey) — 50 concurrent workers, 60s sustained
- *Profiling window:* 30s CPU profile captured under load
- *PGO flag:* `go build -pgo=auto`

## Baseline (no PGO)

| Metric | Value |
|--------|-------|
| CPU (req/s) | TBD |
| p99 latency | TBD |
| Binary size | TBD |

## PGO build (`-pgo=auto`)

| Metric | Value | Delta |
|--------|-------|-------|
| CPU (req/s) | TBD | TBD |
| p99 latency | TBD | TBD |
| Binary size | TBD | TBD |

## Reproduce

```bash
make kind-up            # cluster + Prometheus
make load-prometheus    # warm up + generate load
make collect-baseline   # capture 30s CPU profile
# D4: build Prometheus with -pgo=auto, re-run load, compare
```

## Signal threshold

Pass: ≥3% CPU reduction, no p99 regression.
