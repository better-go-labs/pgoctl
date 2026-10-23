# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `leverage-check` command: static pre-check that compares a PGO build against a baseline build to count module-scoped devirtualization decisions and extra inlines; emits verdict NONE | LOW | HIGH | INCOMPLETE with exit codes 0/3/1 (#50)
- Action: optional `run-leverage-probe` input (default `false`) runs `leverage-check` before the benchmark cycle and exposes the result as the `leverage-verdict` output (`NONE` | `LOW` | `HIGH` | `INCOMPLETE`); a `NONE` verdict emits a `::warning::` annotation and never fails the job; pair with `leverage-probe-target` (default `./...`) to scope the build to the binary the profile came from (#51)

### Fixed

- `merge`: weighted strategy no longer doubles sample counts when merging a single profile. The newest profile now always receives scale factor 1.0; older profiles decay to `--min-weight` (default 0.125) over time. `--recency-weight` is deprecated - it had no effect on decay after this fix and is replaced by the explicit `--min-weight` floor (#56)
- `leverage-check`: module vs dependency origin of each compiler decision now comes from `go list -deps` main-module import paths, with both builds run under `-trimpath` so positions no longer depend on `--dir` or on cached build output. The previous `./` path prefix test counted every subpackage as a dependency
- `leverage-check`: `devirt_decisions` is module-scoped like `pgo_extra_inlines`; the full count stays in `pgo_devirts` (JSON) and the std/deps share is in the new `pgo_devirts_deps`. A profile whose only PGO devirts are in dependencies now gets verdict NONE (exit 3)
- `leverage-check`: compiler lines are matched on their message, so escape-analysis lines that quote source text containing a marker string are no longer counted as PGO decisions
- `leverage-check`: with `--dir`, function names resolve to real import paths from `go list -deps`, so dotted packages like `github.com/satori/go.uuid` no longer truncate to `github.com/satori/go`; without `--dir` the string heuristic still applies


## [0.1.3] - 2026-09-02

### Fixed

- `action`: profile summary splits `quality score` and `valid` into separate table rows (#49)


## [0.1.2] - 2026-09-02

### Fixed

- `collect`: HTTP timeout derived from `window + buffer`; buffer configurable via `--timeout-buffer` flag and `timeout-buffer` action input (default 30 s, floor 120 s)
- `action`: profile report splits samples and unique stacks into separate table columns


## [0.1.0] - 2026-09-01

### Added

#### GitHub Action

- `action.yml` at repo root — users can now reference `uses: better-go-labs/pgoctl@v0.1.0` directly without a subdirectory path
- pgo-action composite action with bootstrap and steady-state profile accumulation modes
- End-to-end CI coverage for pgo-action: 4 gating jobs covering collect → validate → merge → build-with-pgo flow

#### Parca Adapter

- Native Parca OSS profile source via Connect-JSON / gRPC-gateway REST (`pgoctl collect --source=parca`)
- `--window` flag for time-windowed profile queries; `--query` flag with sensible defaults

#### Test Coverage

- In-process CLI testability scaffold + Makefile coverage target (Task 0+A)
- In-process CLI tests for collect, validate, merge, explain, compare (Tasks B–E)
- Error-path tests filling gap to 92.7% line coverage (Tasks F–I, up from 85.2%)
- Codecov integration with coverage badge

### Changed

- Dependency bumps: `github.com/spf13/cobra` 1.8.1 → 1.10.2, `github.com/prometheus/prometheus` (#35), `github.com/stretchr/testify` 1.11.1 → 1.12.1
- Actions bumps: `actions/checkout` v4 → v7, `actions/setup-go` v5 → v7, `actions/upload-artifact` v4 → v7, `actions/cache` v4 → v6, `marocchino/sticky-pull-request-comment` v2 → v3
- CI split into focused workflows; golangci-lint v2, govulncheck, and goreleaser CI hardening added
- Go toolchain bumped to 1.26 across all CI jobs

## [0.1.0-alpha] - 2026-08-19

### Added

#### CLI Subcommands

- `pgoctl collect` — fetch CPU profiles from pprof (`/debug/pprof/profile`) or Parca endpoints; configurable service name, time window, and output path
- `pgoctl validate` — validate a `.pprof` file against quality gates: minimum sample count, aggregate score, and per-package CPU coverage thresholds; supports `--format json` for machine-readable output and all flags settable via env var or config file
- `pgoctl merge` — merge one or more `.pprof` files into a single `default.pgo` artifact ready for `go build -pgo`
- `pgoctl explain` — analyse a `.pgo` file and display the top-N hottest CPU functions grouped by package, with a PGO readiness verdict; supports `--format json`
- `pgoctl compare` — diff two benchmark result files (JSON) and report per-function CPU deltas, surfacing regressions and improvements
- `--version` flag on all subcommands with consistent exit codes: `0` success, `1` validation failure, `2` usage error

#### Parca Adapter

- Native Parca OSS profile source for `pgoctl collect`: authenticate, query by service label and time window, and write the resulting `.pprof` to disk

#### Configuration System

- All `pgoctl validate` flags configurable via CLI flag, environment variable (`PGOCTL_<FLAG>`), or `pgoctl.conf` / `pgoctl.yaml` config file; precedence is CLI > env > file > built-in default
- Config file discovery: current directory → `~/.config/pgoctl/` → `/etc/pgoctl/`; first match wins, missing file is not an error

#### CI & Benchmarking Workflows

- `ci.yml` — GitHub Actions workflow: build, vet, and test on every push and pull request
- `pgo-bench.yml` — 10-round PGO benchmark workflow: pre-compiles baseline and PGO binaries once, runs compute-bound micro-benchmarks across `tsdb`, `promql`, `storage`, and `tsdb/chunkenc`, and posts a benchstat summary as a sticky PR comment
- `profile-collect.yml` — end-to-end profile-collection workflow: provisions a kind cluster with Prometheus, drives load via the built-in load generator, captures a CPU profile, validates coverage gates, and merges into `default.pgo`


[Unreleased]: https://github.com/better-go-labs/pgoctl/compare/v0.1.3...HEAD
[0.1.3]: https://github.com/better-go-labs/pgoctl/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/better-go-labs/pgoctl/compare/v0.1.0...v0.1.2
[0.1.0]: https://github.com/better-go-labs/pgoctl/compare/v0.1.0-alpha...v0.1.0
[0.1.0-alpha]: https://github.com/better-go-labs/pgoctl/releases/tag/v0.1.0-alpha
