# BP-64 LLD: `internal/store` profile storage

> Written by byte-bsl after reading issue #62 (HLD), `2026-10-29-profile-storage-design.md` (spec, source of truth), and `2026-10-30-profile-storage-plan.md` (12-task plan, deviations deliberate). No code has been written yet.

---

## 1. `internal/store` package

### File map (from the plan, unchanged)

| File | Responsibility |
|---|---|
| `date.go` | `Date`, a local UTC calendar day |
| `key.go` | `Key`, tag format and parsing, asset name helpers |
| `manifest.go` | `Manifest` types, encode and decode |
| `store.go` | `Store`, `Day`, `Asset`, sentinel errors |
| `window.go` | `SelectWindow` |
| `snapshot.go` | Unexported helpers shared by Publish and Exclude |
| `publish.go` | `Publish` |
| `restore.go` | `Restore` |
| `exclude.go` | `Exclude` |
| `prune.go` | `Prune` |

### `Date`

```go
type Date struct{ t time.Time } // always UTC midnight; == works
func ParseDate(s string) (Date, error)   // layout "2006-01-02"
func DateOf(t time.Time) Date            // truncates to UTC midnight
func (d Date) String() string
func (d Date) AddDays(n int) Date
func (d Date) Before(o Date) bool
func (d Date) After(o Date) bool
func (d Date) Time() time.Time
```

Local type (plan deviation 1): avoids adding the `cloud.google.com/go/civil` dependency.

### `Key`

```go
const DefaultPrefix = "profile"

type Key struct {
    Repo    string // "owner/name"
    Prefix  string
    Service string
}

func (k Key) Validate() error  // Service must not contain "/"; Prefix must be non-empty; Repo must contain "/"
func (k Key) Tag(d Date, seq int) string        // "<prefix>/[<service>/]<yyyy-mm-dd>.<N>"
func (k Key) ParseTag(tag string) (d Date, seq int, ok bool)
```

Asset name constants and helpers:

```go
const (
    AssetDaily    = "daily.pprof"
    AssetDefault  = "default.pgo"
    AssetManifest = "manifest.json"
)

func RunAsset(runID string) string            // "runs/<id>.pprof"
func RunIDFromAsset(name string) (string, bool)
func ValidRunID(id string) bool               // matches ^[0-9]+-[0-9]+$
```

### `Asset`

```go
type Asset struct {
    ID        string    // store handle: release asset ID, or a counter in Memory
    Name      string
    Size      int64
    UpdatedAt time.Time
}
```

### `Day`

```go
type Day struct {
    Key       Key
    Date      Date
    Seq       int
    Draft     bool
    Immutable bool      // GitHub locked this published release
    ID        string           // release ID or Memory key
    Assets    map[string]Asset // keyed by logical name, e.g. "runs/1-1.pprof"
}

func (d Day) Has(name string) bool
```

### `Store`

```go
// Store abstracts where daily profile snapshots live so Releases, Artifacts,
// and later S3 share the window, exclude and prune logic.
// ListDays returns only the days of key, newest date first, drafts included.
// BeginDay returns the day with that key/date/seq, creating it as a draft if needed.
// A write to a published immutable day returns ErrImmutable.
type Store interface {
    ListDays(ctx context.Context, key Key) ([]Day, error)
    ReadAsset(ctx context.Context, day Day, name string) (io.ReadCloser, error)
    BeginDay(ctx context.Context, key Key, date Date, seq int) (Day, error)
    PutAsset(ctx context.Context, day Day, name string, r io.Reader) error
    DeleteAsset(ctx context.Context, day Day, name string) error
    Publish(ctx context.Context, day Day) error
    DeleteDay(ctx context.Context, day Day) error
}
```

### Sentinel errors

```go
var (
    ErrNotFound         = errors.New("not found")
    ErrImmutable        = errors.New("release is immutable")
    ErrPermission       = errors.New("permission denied")
    ErrConcurrentUpdate = errors.New("concurrent update")
    ErrUnknownSchema    = errors.New("unknown manifest schema")
    ErrNoSnapshot       = errors.New("no usable snapshot")
)
```

### `Manifest` and supporting types

```go
const SchemaVersion = 1

type Manifest struct {
    SchemaVersion int               `json:"schema_version"`
    Key           ManifestKey       `json:"key"`                    // {Service string `json:"service"`}
    Branch        string            `json:"branch"`
    Date          string            `json:"date"`
    Tag           string            `json:"tag"`
    Sequence      int               `json:"sequence"`
    Supersedes    []string          `json:"supersedes,omitempty"`   // earlier tags of this date
    DailyRun      string            `json:"daily_run,omitempty"`
    Runs          []Run             `json:"runs"`
    Window        Window            `json:"window"`
    Exclusions    []Exclusion       `json:"exclusions,omitempty"`
    Merge         MergeInfo         `json:"merge"`
    GoVersion     string            `json:"go_version"`
    PgoctlVersion string            `json:"pgoctl_version"`
    Assets        map[string]string `json:"assets"`                 // logical name -> sha256
}

type Run struct {
    ID           string     `json:"id"`
    Commit       string     `json:"commit"`
    CapturedAt   time.Time  `json:"captured_at"`
    Samples      int64      `json:"samples"`
    QualityScore float64    `json:"quality_score"`
    SHA256       string     `json:"sha256"`
    Excluded     bool       `json:"excluded"`
    ExcludedAt   *time.Time `json:"excluded_at,omitempty"`
    ExcludedBy   string     `json:"excluded_by,omitempty"`
    Reason       string     `json:"reason,omitempty"`
    Missing      bool       `json:"missing,omitempty"`
}

type Window struct {
    LookbackDays int      `json:"lookback_days"`
    MaxProfiles  int      `json:"max_profiles"`
    Dates        []string `json:"dates"` // newest first
}

type Exclusion struct {
    Date   string    `json:"date"`
    Run    string    `json:"run"`
    Reason string    `json:"reason"`
    By     string    `json:"by"`
    At     time.Time `json:"at"`
}

type MergeInfo struct {
    Strategy  string  `json:"strategy"`
    HalfLife  string  `json:"half_life"`
    MinWeight float64 `json:"min_weight"`
    Normalize bool    `json:"normalize"`
}

func DecodeManifest(r io.Reader) (*Manifest, error) // ErrUnknownSchema when schema_version != SchemaVersion
func (m *Manifest) Encode(w io.Writer) error         // two-space indent, trailing newline
func MergeInfoFrom(o merge.Options) MergeInfo        // HalfLife as "%dh"; Normalize always true
```

`Window.Dates` is newest-first, consistent with `SelectWindow`'s output and the spec's restore ordering.

---

## 2. Flow functions with signatures and store call order

### `SelectWindow`

```go
func SelectWindow(days []Date, today Date, lookbackDays, maxProfiles int) []Date
```

Pure function, no store calls. Steps: deduplicate and sort `days` descending; keep `d` where `today.AddDays(-lookbackDays).Before(d) && !d.After(today)` (lookback end is exclusive); truncate to `maxProfiles`; return nil for an empty result.

---

### `Publish`

```go
type PublishRequest struct {
    Key, Today, Branch, Run RunInfo, Profile []byte, LookbackDays, MaxProfiles int, GoVersion, PgoctlVersion string
}
type PublishResult struct {
    Manifest *Manifest
    Default  []byte   // nil when the window has no selected run
    Pruned   []Date
    PruneErr error    // a failed prune does not fail the publish
}

func Publish(ctx context.Context, s Store, req PublishRequest) (*PublishResult, error)
```

Store calls in order:

1. Parse `req.Profile` with `profile.ParseData`. If the parse fails, return an error wrapping `internerrors.ErrParseProfile`. If its CPU total is 0, return an input error. **No store calls yet.**
2. `s.ListDays(ctx, key)` -- record the `ID` and `UpdatedAt` of the manifest asset on the highest *published* sequence of today (or nil). Drafts are excluded here.
3. `openForWrite(ctx, s, key, days, today)` -- see below.
4. `s.PutAsset(day, RunAsset(req.Run.ID), ...)`.
5. `selectDaily(ctx, s, day, manifest)` -- `s.ReadAsset` for each run in the manifest.
6. `buildDefault(ctx, s, key, days, today, own, lookbackDays, maxProfiles)` -- `s.ReadAsset("daily.pprof")` for each window date.
7. `checkUnchanged(ctx, s, key, today, want)` -- `s.ListDays` again; compares manifest `ID` and `UpdatedAt`.
8. `writeSnapshot(ctx, s, day, manifest, daily, def)` -- `s.PutAsset("daily.pprof")`, `s.PutAsset("default.pgo")` (or `DeleteAsset` when nil), `s.PutAsset("manifest.json")` last.
9. If `day.Draft`: `s.Publish(ctx, day)`.
10. `Prune(ctx, s, key, today, lookbackDays, maxProfiles)` -- see below. Its error goes to `PruneErr`.

### `openForWrite(ctx, s, key, days []Day, d Date) (Day, *Manifest, error)`

Finds `cur` = day of `d` with the highest `Seq`, drafts included:

- **No day for `d`:** `s.BeginDay(ctx, key, d, 0)`, return a new manifest.
- **`cur.Draft` or not immutable:** write to `cur`. If the draft has no `manifest.json` (a previous run left an empty draft), read the manifest of the highest published lower sequence; copy its run assets via `s.ReadAsset` + `s.PutAsset`; set `Supersedes`.
- **`cur.Immutable`:** `s.BeginDay(ctx, key, d, cur.Seq+1)`. Copy each `runs/*.pprof` from `cur` via `s.ReadAsset` + `s.PutAsset`. Copy the manifest. Set `Sequence` and extend `Supersedes`.

### `selectDaily(ctx, s, day Day, m *Manifest) ([]byte, string, error)`

`s.ReadAsset(day, RunAsset(id))` for each run in `m.Runs`. Sets `Missing=true` on each that returns `ErrNotFound`. From non-excluded, non-missing runs, picks the highest `QualityScore` (newer `CapturedAt` on tie). Returns the bytes of that run (unchanged) and its ID for `DailyRun`. Returns nil, "" when no run is left.

### `normalize(ps []*profile.Profile)`

No store calls. Computes the CPU total of each profile (sum of sample values at `pprofutil.CPUSampleIndex`). Scales each profile so its total equals the maximum. Scale factors are >= 1, so no count is set to 0 here.

### `buildDefault(ctx, s, key Key, days []Day, at Date, own []byte, lookbackDays, maxProfiles int) ([]byte, []Date, error)`

Returns nil when `own == nil` (the day at `at` has no selected run).

Store calls: for each non-draft day, take the highest-Seq published day per date. Call `SelectWindow`. For each window date: use `own` directly for `at`; for others, `s.ReadAsset(day, "daily.pprof")`. Dates where this returns `ErrNotFound` are silently dropped (the artifact store cannot enumerate zip contents). Parse each profile, set `CapturedAt` from the manifest of that day (a profile with `TimeNanos == 0` would otherwise receive the floor weight). Call `normalize`, then `merge.Profiles(inputs, merge.DefaultOptions())`.

### `checkUnchanged(ctx, s, key Key, d Date, want *Asset) error`

`s.ListDays(ctx, key)`. Finds the highest published sequence of `d`. If `want == nil`, any existing manifest is a conflict. Otherwise compares `ID` and `UpdatedAt`. On mismatch returns a wrapped `ErrConcurrentUpdate` with the message: `manifest of <tag> changed during publish; another run for this key is active (set concurrency: pgo-profile-<key>)`.

### `writeSnapshot(ctx, s, day Day, m *Manifest, daily, def []byte) error`

Store calls in order:

1. `s.PutAsset(day, "daily.pprof", ...)` or `s.DeleteAsset(day, "daily.pprof")` when `daily == nil`.
2. `s.PutAsset(day, "default.pgo", ...)` or `s.DeleteAsset(day, "default.pgo")` when `def == nil`.
3. Compute sha256 of daily and def; set `m.Assets`.
4. `s.PutAsset(day, "manifest.json", encoded)` -- written last so a partial write never exposes a stale manifest.

---

### `Restore`

```go
func Restore(ctx context.Context, s Store, key Key, w io.Writer) (*Manifest, error) // ErrNoSnapshot when none
func usable(d Day) bool // !Draft && Has("default.pgo") && Has("manifest.json")
```

Store calls in order:

1. `s.ListDays(ctx, key)`.
2. Group by date; take the highest-Seq published day per date (a lower sequence is never a fallback).
3. For each date, newest first:
   a. Skip when `!usable(day)`.
   b. `s.ReadAsset(day, "manifest.json")`. Skip on unknown schema.
   c. `s.ReadAsset(day, "default.pgo")`. Read all bytes.
   d. Compare sha256 with the manifest value. On mismatch or `ErrNotFound`, skip to the *next date* (not the next sequence of the same date, because a lower sequence can hold a run that a later exclude removed).
   e. Write to `w`, return the manifest.
4. Return `ErrNoSnapshot` if no date passes.

---

### `Exclude`

```go
type ExcludeRequest struct {
    Key Key, Date Date, RunID, Reason, Actor string, Now time.Time
}
type ExcludeResult struct {
    Rebuilt         []Date // oldest first
    AlreadyExcluded bool
    Skipped         bool   // day was pruned or never published
    PruneErr        error
}

func Exclude(ctx context.Context, s Store, req ExcludeRequest) (*ExcludeResult, error)
```

Store calls in order:

1. `s.ListDays(ctx, key)`. Find the day. If absent, return `Skipped: true` and write `::notice::<date> has no profile (pruned or never published); nothing to exclude`.
2. `s.ReadAsset(day, "manifest.json")`. If the run is not in the manifest, return wrapped `ErrNotFound` naming the run and tag. If already excluded, return `AlreadyExcluded: true`. No writes in either case.
3. Set `Excluded`, `ExcludedAt`, `ExcludedBy`, `Reason` on the run.
4. Compute the rebuild set: for each non-draft day with the highest Seq, `s.ReadAsset(day, "manifest.json")` to check `Window.Dates`. Collect days whose window contains the excluded date. Also include the excluded day itself.
5. For each day in the rebuild set, oldest first:
   a. `s.ReadAsset(excludedDay, "daily.pprof")` for `own`.
   b. `selectDaily` for the excluded day.
   c. `buildDefault` using `lookbackDays` and `maxProfiles` from that day's manifest.
   d. `checkUnchanged` + `openForWrite` for that date.
   e. `writeSnapshot`.
   f. `s.Publish` if the resulting day is a draft.
6. `Prune(...)`. Error goes to `ExcludeResult.PruneErr`.

---

### `Prune`

```go
func Prune(ctx context.Context, s Store, key Key, today Date, lookbackDays, maxProfiles int) ([]Date, error)
```

Store calls in order:

1. `Restore(ctx, s, key, io.Discard)`. Captures the restore target's date. `ErrNoSnapshot` means no day is protected.
2. `s.ListDays(ctx, key)`.
3. Compute `inWindow`: dates `d` where `today.AddDays(-lookbackDays) < d <= today` and the manifest of the highest published sequence has `DailyRun != ""`.
4. Compute the delete set:
   - If `len(inWindow) >= maxProfiles`: delete days older than the `maxProfiles`-th newest date in `inWindow`, plus all days outside the lookback (drafts included in both cases).
   - If `len(inWindow) < maxProfiles`: delete only superseded days (lower Seq of a date that has a published higher Seq).
   - In both cases: always add superseded days; always remove the restore target from the delete set.
5. `s.DeleteDay(ctx, day)` for each day in the delete set.
6. Return the unique deleted dates.

---

## 3. `ghrelease`

### Endpoint table

| Method | Path | Use |
|---|---|---|
| `GET` | `/repos/{o}/{r}` | `Repo` |
| `GET` | `/repos/{o}/{r}/releases?per_page=100` | `ListDays`. Follow `Link: rel="next"`. Map `immutable` field to `Day.Immutable` |
| `GET` | `/repos/{o}/{r}/releases/{id}` | Refresh asset list after a `PutAsset` (to get the new asset ID for the rename) |
| `POST` | `/repos/{o}/{r}/releases` | `BeginDay`. Body: `{"tag_name":"<tag>","name":"<tag>","draft":true,"prerelease":true,"make_latest":"false"}` |
| `PATCH` | `/repos/{o}/{r}/releases/{id}` | `Publish`. Body: `{"draft":false,"prerelease":true,"make_latest":"false"}` |
| `POST` | `<upload_url>?name=<wire>` | `PutAsset` upload step. `Content-Type: application/octet-stream`. Strip `{?name,label}` from the template |
| `PATCH` | `/repos/{o}/{r}/releases/assets/{id}` | Rename `tmp-<wire>` to `<wire>` |
| `DELETE` | `/repos/{o}/{r}/releases/assets/{id}` | `DeleteAsset`, and the old asset during a replace |
| `GET` | `/repos/{o}/{r}/releases/assets/{id}` | `ReadAsset`. `Accept: application/octet-stream`. Follow redirect. Drop `Authorization` on a redirect to a different host |
| `DELETE` | `/repos/{o}/{r}/releases/{id}` | `DeleteDay` step 1 |
| `DELETE` | `/repos/{o}/{r}/git/refs/tags/{tag}` | `DeleteDay` step 2. Skip for drafts. Ignore a 404 |

All requests: `Authorization: Bearer <token>`, `Accept: application/vnd.github+json`, `X-GitHub-Api-Version: 2022-11-28`.

### Replace sequence for `PutAsset` on an existing asset

1. Delete stale `tmp-<wire>` if it exists (cleanup from a previous failed replace).
2. `POST <upload_url>?name=tmp-<wire>`.
3. `DELETE` the old `<wire>` asset.
4. `PATCH` the new asset to rename it from `tmp-<wire>` to `<wire>`.

`ListDays` hides assets whose name starts with `tmp-` from `Day.Assets`. This prevents `checkUnchanged` from seeing a phantom asset as the manifest during a mid-replace window.

### Asset name mapping

Logical `runs/<id>.pprof` becomes wire `runs-<id>.pprof`. Only the `runs/` prefix maps to `runs-`, not each `/`, because the run ID already contains `-`. All other logical names are unchanged on the wire.

### Error mapping

| Condition | Result |
|---|---|
| 404 on a read | Wrap `ErrNotFound` |
| 429, or 403 with `Retry-After`, or 403 with `X-RateLimit-Remaining: 0`, or 403 with body containing "rate limit" | Error message: `GitHub API rate limit exceeded; retry after <Retry-After else X-RateLimit-Reset as RFC 3339>`. Rate-limit check precedes the general-403 check because GitHub sends 403 for both primary and secondary limits |
| Any other 403, or 404 on a write (POST/PATCH/DELETE) | Wrap `ErrPermission`. Message: `<method> <path> returned <code>: the job token cannot write releases. Grant the publish job "permissions: contents: write"` |
| 422 on a write with body containing "immutable" | Wrap `ErrImmutable` |
| 401 | Message: `GitHub rejected the token` |
| Other non-2xx | Message: `<method> <path> <code>: <first 200 bytes of body>` |

---

## 4. `internal/store/artifact`

### Artifact name mapping

Tag `profile/2026-10-26.0` -> name `profile_2026-10-26.0`. Tag `profile/api/2026-10-26.0` -> `profile_api_2026-10-26.0`. Each `/` becomes `_`. `ParseName` uses the key's `Prefix` and `Service` to filter, as `ParseTag` does, so the plain key's listing never includes the `api` key's artifacts.

### In-process draft

`BeginDay` creates a staging directory with `os.MkdirTemp`. `PutAsset` writes a file there. `DeleteAsset` removes a file. `Publish` zips the directory, uploads via `results.go`, and removes the staging directory. `ListDays` includes the in-process draft (with `Draft=true`) only within the same process. Because an uploaded artifact cannot change, every published day has `Immutable=true` and every later write to a date creates the next sequence.

### Results-service upload (`results.go`)

Protocol: `actions/toolkit` results service (snake_case twirp JSON; the source commit is named in the code comment, because GitHub does not document this API).

1. Validate `RuntimeToken` and `ResultsURL` are non-empty; return `"the artifact store needs ACTIONS_RUNTIME_TOKEN and ACTIONS_RESULTS_URL; the Action exports them before the publish"` otherwise.
2. Decode `RuntimeToken` JWT payload without a signature check. Extract `workflow_run_backend_id` and `workflow_job_run_backend_id` from the `scp` claim (`Actions.Results:<wf>:<job>`).
3. `POST <origin of ResultsURL>/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact` with `Content-Type: application/json`. Body fields: `workflow_run_backend_id`, `workflow_job_run_backend_id`, `name`, `mime_type`, `version` (7). Response: `ok`, `signed_upload_url`, `artifact_id`.
4. Zip the staging directory.
5. `PUT <signed_upload_url>` with `x-ms-blob-type: BlockBlob`, `Content-Type: application/zip`. One PUT, not a block-upload sequence.
6. `POST .../FinalizeArtifact` with the two backend IDs, `name`, `size` (decimal string), `hash` (`sha256:<hex>`).

### `RestoreLegacy`

```go
func (s *Store) RestoreLegacy(ctx context.Context, w io.Writer) error
```

Lists artifacts for `s.cfg.LegacyName` via `GET /repos/{o}/{r}/actions/artifacts`. If none, returns `ErrNoSnapshot`. If found, downloads the zip and extracts `default.pgo` into `w`. Called by `pgoctl store restore` only when the key has no day (the v0.1.x migration path).

### How `ListDays` handles duplicate names

Two runs can race and upload the same artifact name before the second run's `openForWrite` creates a `.1` sequence. `ListDays` keeps the entry with the newer `created_at` for each name. `Prune` then deletes the older duplicate because it is a superseded sequence (a lower Seq with a published higher Seq for the same date).

---

## 5. `pgoctl store` commands, flags and exit codes

### Shared persistent flags on `store`

| Flag | Default | Notes |
|---|---|---|
| `--store` | `github-release` | or `artifact` |
| `--repo` | `$GITHUB_REPOSITORY` | |
| `--tag-prefix` | `profile` | |
| `--service` | empty | |
| `--api-url` | `$GITHUB_API_URL` else `https://api.github.com` | |
| `--legacy-artifact` | empty | v0.1.x artifact name for restore fallback |
| `--today` | UTC date now | hidden; for tests and e2e |

Token: `$GITHUB_TOKEN` else `$GH_TOKEN`.

### Subcommands

**`pgoctl store resolve --mode collect|restore --branch --publish-branch --allow-public-profiles`**
Outputs `store=<github-release|artifact>` and `publish=<true|false>` to stdout. With `--store artifact`, skips the visibility check entirely and outputs `store=artifact`. Otherwise calls `repoInfo`; if `Visibility=="public"` and the flag is not set, outputs `store=artifact` and writes `::warning::<repo> is public...` to stderr. In `collect` mode, `publish = (branch == publish-branch)`. An empty `--publish-branch` resolves to `DefaultBranch`. When `publish=false`, writes `::notice::Branch <b> is not the publish branch <p>. This run does not publish a profile.`

**`pgoctl store publish --profile (required) --out default.pgo --run-id --commit --branch --samples 0 --quality-score --go-version --lookback-days 14 --max-profiles 7`**
`--samples 0` means compute with `pprofutil.SampleCount`. `CapturedAt` from profile `TimeNanos`, else `time.Now()`. Writes result to `--out` via temp-file-then-rename. Stderr: `published <tag>: window <n> days (<dates>)`. `PruneErr` gives `::warning::prune: <err>` and exits 0. With `--json` (root flag), prints the manifest on stdout.

**`pgoctl store restore --out default.pgo`**
Writes via temp-file-then-rename. Stderr: `restored <tag> (window: <n> days)`. With `--store artifact --legacy-artifact <name>`: on `ErrNoSnapshot`, tries `RestoreLegacy`; on success writes `restored legacy artifact <name>` and exits 0. `profile-tag` output is empty for a legacy artifact.

**`pgoctl store exclude --date --run --reason --actor` or `--pairs "<date>:<run>[,...]"`**
`--pairs` is exclusive with `--date`/`--run`. Accepts commas or whitespace between pairs. Prints `excluded <run> on <date>; rebuilt <dates>` or `<run> on <date> already excluded`.

**`pgoctl store prune --lookback-days 14 --max-profiles 7`**

### Exit codes

| Code | Condition |
|---|---|
| 0 | Success |
| 1 | `ErrPermission`, `ErrConcurrentUpdate`, `ErrNotFound`, or any other runtime/API error. Wrapped in `&exitError{1, err}` so `main.go`'s plain-error path does not exit 2. The CLI appends `grant the publish job "permissions: contents: write" (or actions: write for the artifact store)` to each `ErrPermission` |
| 2 | Input validation fails: `lookback-days < 1`, `max-profiles < 1`, `!ValidRunID`, bad date, bad pair, `Key.Validate` error |
| 3 | `ErrNoSnapshot` on restore |

---

## 6. `action.yml` step list

### `collect` mode

Steps 3-11 carry `if: inputs.profile-mode == 'collect'` in addition to any per-step guard.

| # | Step (`id:`) | Additional `if:` guard | Notes |
|---|---|---|---|
| 1 | Install | (none) | No change |
| 2 | Resolve profile store (`store`) | (none) | `pgoctl store resolve --mode collect ... >> "$GITHUB_OUTPUT"`. Input-presence check (pprof-url or parca-url required) moves here. Sub-step 2b (`if: steps.store.outputs.store == 'artifact' && steps.store.outputs.publish == 'true'`): runs `actions/github-script@f28e40c7f34bde8b3046d885e986cb6290c5673b # v7.1.0`; calls `core.setSecret(token)` then `core.exportVariable('ACTIONS_RUNTIME_TOKEN', token)` and `core.exportVariable('ACTIONS_RESULTS_URL', url)` |
| 3 | Collect | (none) | `continue-on-error: true` |
| 4 | Validate | (none) | Also writes `gate=pass\|fail`. When `steps.collect.outcome != 'success'`, skips and writes `gate=fail` |
| 5 | Restore previous | (none) | `pgoctl store restore [--store artifact] --out previous/default.pgo`. Exit 3 is not an error. `previous-profile` input copies to `previous/default.pgo` instead. Removes the `dawidd6/action-download-artifact` step |
| 6 | Exclude runs | `steps.store.outputs.publish == 'true' && steps.validate.outputs.gate == 'pass' && inputs.exclude-runs != ''` | `pgoctl store exclude --pairs "$EXCLUDE_RUNS" --reason "exclude-runs input"` |
| 7 | Build default.pgo (`merge`) | (none) | Four branches: (a) `gate=fail` + cpu.pprof present: copy previous or `pgoctl merge`; (b) `gate=fail` + no cpu.pprof: `has-profile=false`; (c) `publish=true`: `pgoctl store publish ... --json`, sets `profile-tag` from JSON; (d) `publish=false`: `pgoctl merge cpu.pprof` |
| 8 | Paths, leverage, compare, explain | (none) | No change |
| 9 | Upload artifact (`artifact-name`) | `steps.validate.outputs.gate == 'pass'` | Adds gate check to the existing `if:` |
| 10 | Comment | (none) | No change |
| 11 | Gate result | `steps.validate.outputs.gate == 'fail' && inputs.on-gate-failure == 'fail'` | `exit 1` |

### `restore` mode

Steps 1 and 2 run without a `profile-mode` guard.

| # | Step | `if:` | Notes |
|---|---|---|---|
| 1 | Install | (none) | No change |
| 2 | Resolve profile store | (none) | Same as collect |
| 3 | Restore profile | `inputs.profile-mode == 'restore'` | `pgoctl store restore ... --out default.pgo`. Exit 0: `has-profile=true`, `profile-tag` from JSON. Exit 3: `has-profile=false`, appends "No usable PGO profile. This build runs without PGO." to `$GITHUB_STEP_SUMMARY` |

---

## 7. Test list

### `internal/store`

Plan minimum tests are listed as-is; added tests are marked **(+)** with the reason.

**`key_test.go`:** `TestKey_Tag`, `TestKey_ParseTag_StaysInsideKey`, `TestKey_Validate`, `TestRunAsset_RoundTrip`

**`date_test.go`:** parse round-trip, UTC truncation (non-UTC input), `AddDays(-14)`, invalid month
**(+) `TestDate_EqualityIsUTCMidnight`:** two `DateOf` calls on the same UTC day from different `time.Location` values must be `==`. Guards the "always UTC midnight" invariant of the struct.

**`window_test.go`:** `TestSelectWindow` (seven subtests from plan)
**(+) `TestSelectWindow_LookbackEdgeInclusive`:** a day exactly 13 days ago is inside the window (today-13); paired with the plan's "14 days old is out" case to make both edges explicit.

**`manifest_test.go`:** `TestManifest_RoundTrip`, `TestManifest_UnknownSchema`, `TestMergeInfoFrom_Defaults`

**`publish_test.go`:** all fourteen tests from the plan (`TestPublish_FirstRun` through `TestPublish_MissingRun`)
**(+) `TestPublish_CapturedAtZeroFallsBackToNow`:** `req.Run.Profile` has `TimeNanos == 0`. The manifest `CapturedAt` is within 5 s of `time.Now()`. A zero timestamp causes the floor weight in the merge; this test makes the fallback contract explicit and detectable.
**(+) `TestPublish_WindowDropsMissingDailyPprof`:** a published day has `Day.Has("daily.pprof") == true` (the artifact store always reports it), but `ReadAsset` returns `ErrNotFound` at build time. That date is silently dropped from the window, and `Publish` returns no error. Covers the artifact-store path described in the `buildDefault` spec.
**(+) `TestNormalize_SingleProfile`:** `normalize` on a slice of one profile is a no-op (scale factor 1.0, no mutation). Guards against accidentally inflating a single-element slice.
**(+) `TestNormalize_EqualTotals`:** all profiles have the same CPU total. All scale factors are 1.0. Guards against unnecessary mutation when no normalization is needed.

**`restore_test.go`:** all eight tests from the plan.

**`exclude_test.go`:** all eight tests from the plan.
**(+) `TestExclude_SliceHasOneEntry`:** call `Exclude` twice for the same run. The second returns `AlreadyExcluded`. The manifests of the rebuilt snapshots each still have exactly one `Exclusion` entry. The plan's `_Idempotent` covers write idempotency; this test makes the slice-length invariant explicit and independently findable.

**`prune_test.go`:** all seven tests from the plan.

---

### `internal/store/storetest`

Plan minimum tests: `RunContract` subtests, `TestMemory_Contract`, `TestProfile_Valid`.

**(+) `TestMemory_ImmutableOption`:** `NewMemory(storetest.Immutable())`. After `Publish`, `Day.Immutable == true`. A `PutAsset` or `DeleteAsset` on that day returns `ErrImmutable`. `BeginDay(d, seq+1)` succeeds and creates a new draft. Validates the option that Tasks 3 and 5 depend on.
**(+) `TestMemory_FailRead_Transient`:** `FailRead` makes one read return `ErrNotFound`; the next read of the same name succeeds. Guards the single-shot contract that `TestPublish_AssetVanishes` depends on.

---

### `internal/store/ghrelease`

Plan minimum tests: `TestContract`, `TestListDays_Pagination`, `TestBeginDay_DraftThenPublish`, `TestPutAsset_Replace`, `TestRunAssetWireName`, `TestListDays_Immutable`, `TestPutAsset_Immutable`, `TestForbidden`, `TestRateLimited`, `TestPublish_ConcurrentUpdate`, `TestReadAsset_FollowsRedirect`, `TestRepo`.

**(+) `TestReadAsset_DropAuthOnRedirect`:** the `ReadAsset` redirect goes to the storage host (a different origin). The storage request must not carry the `Authorization` header. Prevents accidental token leakage to a third-party host.
**(+) `TestDeleteDay_DraftSkipsTagDelete`:** `DeleteDay` on a draft must not call `DELETE /git/refs/tags/{tag}`. The fake records all requests; this test asserts the tag-ref call is absent.
**(+) `TestDeleteDay_Ignores404OnTagRef`:** after the release is deleted, the tag-ref `DELETE` returns 404. `DeleteDay` returns nil. Covers the "Ignore a 404 from the ref call" rule.
**(+) `TestListDays_HidesTmpAssets`:** a release that has a `tmp-daily.pprof` asset in its asset list does not expose it in `Day.Assets`. Guards `checkUnchanged` from seeing a phantom manifest during a mid-replace window.

---

### `internal/store/artifact`

Plan minimum tests: `TestContract`, `TestUpload_Protocol`, `TestUpload_NoRuntimeToken`, `TestUpload_BadScope`, `TestListDays_NameMapping`, `TestListDays_SkipsExpired`, `TestReadAsset_NotInZip`, `TestDeleteDay_Forbidden`, `TestPublish_SecondRunMakesNextSequence`, `TestRestore_LegacyArtifact`.

**(+) `TestListDays_DuplicateName`:** two artifacts have the same name (two concurrent runs uploaded before the `.1` sequence was created). `ListDays` returns one entry with the newer `created_at`. The plan states "keep the newest `created_at`"; this test makes the choice explicit and findable.
**(+) `TestUpload_PutIsOneRequest`:** the blob PUT happens exactly once. Guards against accidentally switching to a multi-part block upload, which the plan says is not the path other Go clients use.

---

### `cmd/pgoctl`

Plan minimum tests: `TestStoreResolve_OtherBranch`, `_DefaultBranch`, `_PublicRepo`, `TestStorePublishRestore_RoundTrip`, `TestStoreRestore_Empty`, `TestStorePublish_BadFlags`, `TestStorePublish_Permission`, `TestStorePublish_RuntimeErrorIsOne`, `TestStoreExclude_Pairs`, `TestStoreArtifact_PublishRestore`.

**(+) `TestStoreResolve_ArtifactStoreSkipsVisibility`:** `--store artifact` never calls `repoInfo` and always outputs `store=artifact`. The plan states this behavior; there is no test for it in the plan's list.
**(+) `TestStorePublish_JSON`:** `--json` writes valid JSON to stdout containing `"tag"` and `"schema_version": 1`. Guards against the root-flag hiding issue noted in the plan ("do not define `--json` again on a store subcommand").
**(+) `TestStoreRestore_LegacyArtifactCLI`:** end-to-end through the CLI with `--store artifact --legacy-artifact pgo-profiles`. Key has no day; legacy artifact exists. Exit 0, `--out` file is written. The artifact package covers the protocol; this CLI test confirms the flag routing and exit-code contract.
**(+) `TestStoreExclude_SkippedDayWritesNotice`:** `--date 2026-09-01` (no day). Exit 0, stderr contains `::notice::`. Confirms the CLI contract; the logic is tested in `exclude_test.go` but the `pgoctl` command test confirms the notice text and the exit-code mapping.

---

## 8. Open questions

1. **`immutable` field in the Releases API response.** The plan notes that the GitHub docs do not show this field. The `ghrelease` tests fake it. Task 11 step 5 is supposed to confirm it on a real repository. Before I write `client.go`, can you confirm the exact JSON field name? If it is unknown, I will add a probe step to the e2e workflow and treat the `ErrImmutable` path as unverified until Task 11 runs.

2. **`actions/github-script` pin for the runtime-token export step.** *Answered in section 9:* pin to `actions/github-script@f28e40c7f34bde8b3046d885e986cb6290c5673b # v7.1.0`, the latest v7 release tag commit at the time of writing this document.

3. **Scratch repository for Task 11.** The plan names `github.com/better-go-labs/gjson` as the scratch repository. Is that still the right repository, and is it private?

4. **`pprofutil.SampleCount` vs `pprofutil.CPUSampleIndex` (for visibility).** I will use `pprofutil.CPUSampleIndex` for the CPU total throughout (normalize, validate, explain, leverage). `SampleCount` counts unique stacks on a Parca `cpu/nanoseconds` profile and is not used for the normalization total. No action needed; listed so the distinction is on record.

---

## 9. Review (2026-10-31)

**LLD review: approved.** Signatures, store call order, error mapping, exit codes, Action steps and test lists agree with the plan. Keep all nine added tests.

**Answers to the four open questions**

1. `ACTIONS_RESULTS_URL`: origin only. If Task 11 step 6 shows a path prefix, keep the path up to and excluding `/twirp`, and record it in the code comment.
2. Release list limit: document it in the README for v0.2.0. A search-based `ListDays` is a follow-up issue.
3. `actions/toolkit` pin: choose the commit during Task 8, from `main` at that time. Put the SHA in the comment at the top of `results.go` and in the PR 2 description.
4. `quality_score`: the Action already exports `steps.validate.outputs.score` and `.samples`. Step 7 passes `--quality-score "$SCORE" --samples "$SAMPLES"`. The `0` default is for direct CLI use only.

**Answer to open question 8.2 (added after review)**

Pin to `actions/github-script@f28e40c7f34bde8b3046d885e986cb6290c5673b # v7.1.0`, the latest v7 release tag commit at the time of writing. Use `uses: actions/github-script@f28e40c7f34bde8b3046d885e986cb6290c5673b # v7.1.0` in step 2b.

**Four notes**

1. Section 6 step 2b: the `if:` must read `steps.store.outputs.store == 'artifact' && steps.store.outputs.publish == 'true'` (applied above).
2. `Prune` calls `Restore` first. Keep `Restore` as the single source of the protected date; do not add a second "newest" rule.
3. Artifact store: the in-process draft must appear in `ListDays` before `Publish`, so `openForWrite` and `checkUnchanged` see it. The contract test `BeginDay_CreatesDraftOnce` must pass on the artifact store with the fake.
4. Commits and PR descriptions: git author only. No `Co-Authored-By`, no "Generated with Claude Code", no em dashes.
