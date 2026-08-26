# Contributing to pgoctl

Thanks for your interest in improving pgoctl! pgoctl is a Go CLI for continuous
profile-guided optimization. This guide covers how to report issues, set up a local
development environment, and submit changes.

## Reporting Bugs and Requesting Features

Please use our issue templates:

- **Bug reports:** [Open a bug report](https://github.com/better-go-labs/pgoctl/issues/new?template=bug_report.yml)
- **Feature requests:** [Open a feature request](https://github.com/better-go-labs/pgoctl/issues/new?template=feature_request.yml)

For questions and general help, please use
[GitHub Discussions](https://github.com/better-go-labs/pgoctl/discussions) rather than
opening an issue.

> **Security issues:** Do **not** open a public issue. See [SECURITY.md](SECURITY.md)
> for how to report vulnerabilities privately.

## Local Development Setup

pgoctl is a standard Go module. Clone the repo and build:

```bash
# Build all packages
go build ./...

# Or use the Makefile (build, lint, test targets)
make

# Run the test suite
go test ./...
```

You'll need a recent Go toolchain (see `go.mod` for the minimum supported version).

## Branch Naming

Use a descriptive branch name with one of these prefixes:

- `feature/` — new functionality
- `fix/` — bug fixes
- `chore/` — maintenance, tooling, docs, dependencies

Example: `feature/pprof-retry-backoff`, `fix/profile-parse-panic`.

## Pull Request Process

1. Fork the repository.
2. Create a branch off `main` using the naming convention above.
3. Make your change. Keep each PR to **one logical change** — smaller PRs are easier
   to review and merge.
4. Open a PR against `main` and fill out the pull request template.

## Commit Style

We prefer [Conventional Commits](https://www.conventionalcommits.org/). Use a type
prefix in your commit subject:

- `feat:` — a new feature
- `fix:` — a bug fix
- `chore:` — tooling, deps, or maintenance
- `docs:` — documentation only

Example: `fix: handle empty pprof profile without panic`.

## Code Style

Before submitting, make sure your code is clean:

```bash
# Format your code
gofmt -w .

# Vet for common mistakes
go vet ./...

# Lint (if golangci-lint is installed)
golangci-lint run
```

CI enforces `gofmt` and `go vet`; `golangci-lint` is run when available.

## Developer Certificate of Origin (DCO)

By submitting this PR, I agree to the
[Developer Certificate of Origin](https://developercertificate.org/). In short: you
certify that you wrote the contribution or otherwise have the right to submit it under
the project's license.
