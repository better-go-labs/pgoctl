# pgoctl

> Continuous PGO and profile-guided optimization for Go workloads on Kubernetes.

`pgoctl` is the CLI backbone of GoOpt — a Kubernetes-native control plane that turns production Go profiles into optimized builds with measurable CPU and latency gains.

## What it does

Production profiles from your Go services → validated PGO artifacts → optimized builds → safe canary rollouts → cost reports.

```
pgoctl collect  --source=parca   --service=prometheus --window 72h
pgoctl validate cpu.pprof
pgoctl merge    profiles/*.pprof --out default.pgo
pgoctl explain  default.pgo
pgoctl compare  baseline.json candidate.json
```

## Status

🚧 **Sprint Day 1 — scaffold only.** CLI subcommands land in Week 2 (D6+). See [BENCHMARKS.md](BENCHMARKS.md) for PGO results as they land.

## Demo service

We benchmark against **Prometheus** — pure Go, pprof-enabled by default, widely deployed on Kubernetes. It gives a credible "before/after" story when we optimize a real production-grade Go service.

## Quick start

```bash
# 1. Spin up a local kind cluster with Prometheus
make kind-up

# 2. Capture a 30s CPU profile from Prometheus under load
make collect-baseline

# 3. Build pgoctl
go build -o bin/pgoctl ./cmd/pgoctl
```

## Configuration

All `pgoctl validate` flags can also be set from a config file or environment
variable instead of the command line — handy for keeping the growing flag set
(e.g. repeated `--min-package-share` gates) in one place.

| Source | Example |
|---|---|
| CLI flag | `pgoctl validate --min-score 0.9 cpu.pprof` |
| Env var | `PGOCTL_MIN_SCORE=0.9 pgoctl validate cpu.pprof` |
| Config file | `pgoctl.conf` (YAML) — see [pgoctl.conf.example](pgoctl.conf.example) |

Precedence: **CLI flag > env var > config file > built-in default**.

The config file is discovered as `pgoctl.conf` (or `pgoctl.yaml`) in the
current directory, then `~/.config/pgoctl/`, then `/etc/pgoctl/`; the first
match wins and a missing file is not an error. Env vars use the
`PGOCTL_<FLAG>` prefix with dashes as underscores
(e.g. `PGOCTL_MIN_PACKAGE_SHARE="tsdb:5,promql:1.5"`).

```yaml
# pgoctl.conf (example)
min-samples: 1000
min-score: 0.3
min-package-share:
  - github.com/prometheus/prometheus/tsdb:5.0
  - github.com/prometheus/prometheus/promql:1.5
```

## Requirements

- Go 1.23+
- [kind](https://kind.sigs.k8s.io/) + [kubectl](https://kubernetes.io/docs/tasks/tools/) + [helm](https://helm.sh/) (for local dev cluster)
- [hey](https://github.com/rakyll/hey) (load generator, optional)

## Project layout

```
cmd/
  pgoctl/     — CLI entry point (Cobra, wired in D6)
  baseline/   — standalone pprof collector for dev/baseline capture
internal/
  collector/  — profile fetch + metadata (used by pgoctl collect)
scripts/
  kind-prometheus.sh  — provision kind cluster + kube-prometheus-stack
testdata/           — captured .pprof files (gitignored)
BENCHMARKS.md      — before/after PGO numbers (populated from D4)
```

## Roadmap

| Phase | Focus | Target |
|-------|-------|--------|
| Week 1 (D1–D5) | Signal: prove PGO before/after on Prometheus | Aug 2 |
| Week 2 (D6–D10) | pgoctl CLI: all 5 subcommands | Aug 7 |
| Week 3 (D11–D15) | GitHub Action + Parca adapter + HN launch | Aug 12 |

## License

Apache 2.0 — see [LICENSE](LICENSE).
