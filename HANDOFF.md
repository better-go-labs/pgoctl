# Handoff Log

Append-only. Every agent writes here on task completion.

---

## 2026-08-19 Dev → docs and release artifacts (pgoctl public-readiness)

Status: done
Output:
- `CHANGELOG.md` committed to main (Keep a Changelog format, `[0.1.0-alpha] - 2026-08-19` section covering all subcommands, Parca adapter, config system, and CI/bench workflows; no internal D-numbers in visible text)
- `README.md` badge row added (CI, Go Report Card, pkg.go.dev, License, Latest Release — in spec order, with pre-launch activation note)
- Annotated tag `v0.1.0-alpha` on main HEAD `501acef` — <https://github.com/better-go-labs/pgoctl/releases/tag/v0.1.0-alpha>

Notes: All three artifacts share the same version string (`v0.1.0-alpha`). Release is marked pre-release. PR chain #12→#22 not touched. Badges that depend on public visibility will be inactive until Gyanesh flips repo visibility.

---

## 2026-09-05 Dev → BP-24: pgoctl leverage-check command

Status: done
Output: PR #50 (feat/leverage-check branch) — https://github.com/better-go-labs/pgoctl/pull/50

- `cmd/pgoctl/leverage.go` — cobra command with --dir flag, text/JSON output formats
- `internal/leverage/leverage.go` — core logic: pprof parsing, hot function detection, PGO build analysis, verdict generation
- `internal/leverage/leverage_test.go` — comprehensive table-driven unit tests
- `testdata/cpu_valid.pprof` — valid gzipped pprof test fixture (committed directly, excluded from LFS in .gitattributes)

CI Status (head SHA 8ac496ef):
- Build #103 ✅
- golangci-lint #102 ✅
- Smoke #205 ✅
- vulncheck #98 ✅
- e2e workflows ✅

Notes: Feature-complete pre-flight PGO viability check. Two commits (e49e446 + 8ac496e) fixed all three CI failures mentioned in verification: (1) valid pprof fixture generation + LFS exclusion, (2) all exported types/functions have required doc comments, (3) gofmt and revive lint issues resolved. Exit codes: 0=leverage found/profile-only, 2=no leverage, 1=error. Ready for PM re-verification and merge.
