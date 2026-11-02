# Profile store end-to-end test

`e2e.yml` exercises the `pgoctl store` backends against a real repository, end to
end: it publishes to and restores from both GitHub Releases and GitHub Actions
Artifacts, checks that an empty store restores cleanly, and that the rolling
window prunes. It runs in a **scratch repository**, not in CI for this repo,
because it creates and deletes real releases and artifacts.

## What it covers

| Job | Backend | Checks |
|-----|---------|--------|
| `github-release` | GitHub Releases | publish then restore round-trip; optional immutable-release sequencing when `ADMIN_TOKEN` is set |
| `artifact` | Actions Artifacts | publish then restore round-trip (exposes the runner's `ACTIONS_RUNTIME_TOKEN` for the results service) |
| `restore-empty` | GitHub Releases | restoring an unused key exits 3 (no snapshot) rather than failing |
| `exclude-prune` | GitHub Releases | publishing two days then pruning to `max-profiles=1` removes the older day and keeps the day restore selects |

Each job builds `pgoctl` from the ref given in the `pgoctl-ref` dispatch input,
so it tests the exact revision under review.

## Scratch repository

The scratch repository is [`better-go-labs/gjson`](https://github.com/better-go-labs/gjson),
a public fork used only for this test. Because it is public, the Releases jobs
store profiles there deliberately; a consumer's own public repository would
instead default to the private artifact backend unless it sets
`allow-public-profiles: true`.

The repository keeps a `profile/demo/*` release as a fixture. **The cleanup steps
only ever delete tags under this run's own `profile/e2e-*-<run-id>` prefix and
refuse the `profile/demo/*` prefix outright**, so repeated runs never disturb the
fixture.

## Running it

1. Copy this workflow into the scratch repo's default branch as
   `.github/workflows/pgoctl-store-e2e.yml`.
2. Dispatch it with the ref to test:

   ```sh
   gh workflow run pgoctl-store-e2e.yml \
     -R better-go-labs/gjson \
     -f pgoctl-ref=<branch-or-tag>
   gh run watch -R better-go-labs/gjson
   ```

3. Expect all four jobs to pass (the immutable step inside `github-release` is
   skipped unless an `ADMIN_TOKEN` secret with admin rights on the scratch repo
   is present, since toggling immutable releases is an admin-only setting).

The helper scripts `collect.sh`, `cleanup-releases.sh`, and `cleanup-artifacts.sh`
are invoked from the checked-out `pgoctl` source, so the workflow needs no scripts
committed to the scratch repository.
