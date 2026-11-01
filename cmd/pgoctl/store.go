package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/Better-Go-Labs/pgoctl/internal/store/artifact"
	"github.com/Better-Go-Labs/pgoctl/internal/store/ghrelease"
	"github.com/spf13/cobra"
)

// storeOverride is set by tests to bypass the real backend construction.
// When non-nil, makeStore returns it instead of building from flags.
var storeOverride store.Store

// storeFlags holds the persistent flags shared by all store subcommands.
type storeFlags struct {
	storeBackend   string
	repo           string
	tagPrefix      string
	service        string
	apiURL         string
	legacyArtifact string
	today          string // hidden; overrides UTC date for tests and e2e
}

func (f *storeFlags) key() (store.Key, error) {
	key := store.Key{Repo: f.repo, Prefix: f.tagPrefix, Service: f.service}
	if err := key.Validate(); err != nil {
		return store.Key{}, err
	}
	return key, nil
}

func (f *storeFlags) todayDate() (store.Date, error) {
	if f.today != "" {
		return store.ParseDate(f.today)
	}
	return store.DateOf(time.Now().UTC()), nil
}

func (f *storeFlags) token() string {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GH_TOKEN")
}

// makeStore builds the appropriate Store backend from persistent flags.
// When storeOverride is set (tests only), it is returned directly.
func (f *storeFlags) makeStore() (store.Store, error) {
	if storeOverride != nil {
		return storeOverride, nil
	}
	key, err := f.key()
	if err != nil {
		return nil, fmt.Errorf("key: %w", err)
	}
	switch f.storeBackend {
	case "github-release", "ghrelease":
		return ghrelease.New(ghrelease.Config{Key: key, Token: f.token(), BaseURL: f.apiURL})
	case "artifact":
		cfg := artifact.Config{
			Key:          key,
			Token:        f.token(),
			APIURL:       f.apiURL,
			LegacyName:   f.legacyArtifact,
			RuntimeToken: os.Getenv("ACTIONS_RUNTIME_TOKEN"),
			ResultsURL:   os.Getenv("ACTIONS_RESULTS_URL"),
		}
		return artifact.New(cfg), nil
	default:
		return nil, fmt.Errorf("unknown store %q: use github-release or artifact", f.storeBackend)
	}
}

func newStoreCmd() *cobra.Command {
	var sf storeFlags

	cmd := &cobra.Command{
		Use:   "store",
		Short: "Manage pgoctl profile storage",
	}

	repo := os.Getenv("GITHUB_REPOSITORY")
	apiURL := os.Getenv("GITHUB_API_URL")
	if apiURL == "" {
		apiURL = "https://api.github.com"
	}

	cmd.PersistentFlags().StringVar(&sf.storeBackend, "store", "github-release", "storage backend: github-release or artifact")
	cmd.PersistentFlags().StringVar(&sf.repo, "repo", repo, "repository owner/name ($GITHUB_REPOSITORY)")
	cmd.PersistentFlags().StringVar(&sf.tagPrefix, "tag-prefix", store.DefaultPrefix, "release tag prefix")
	cmd.PersistentFlags().StringVar(&sf.service, "service", "", "profile key service label")
	cmd.PersistentFlags().StringVar(&sf.apiURL, "api-url", apiURL, "GitHub API base URL ($GITHUB_API_URL)")
	cmd.PersistentFlags().StringVar(&sf.legacyArtifact, "legacy-artifact", "", "v0.1.x artifact name for restore fallback")
	cmd.PersistentFlags().StringVar(&sf.today, "today", "", "override today's date (YYYY-MM-DD, hidden)")
	_ = cmd.PersistentFlags().MarkHidden("today")

	cmd.AddCommand(newStoreResolveCmd(&sf))
	cmd.AddCommand(newStorePublishCmd(&sf))
	cmd.AddCommand(newStoreRestoreCmd(&sf))
	cmd.AddCommand(newStoreExcludeCmd(&sf))
	cmd.AddCommand(newStorePruneCmd(&sf))
	return cmd
}

func newStoreResolveCmd(sf *storeFlags) *cobra.Command {
	var mode string
	var branch string
	var publishBranch string
	var allowPublicProfiles bool

	cmd := &cobra.Command{
		Use:   "resolve",
		Short: "Determine the profile store backend and whether this run publishes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := context.Background()

			key, err := sf.key()
			if err != nil {
				return &exitError{2, err}
			}

			// With --store artifact, skip the visibility check, but still decide
			// publish the same way as Releases: resolve the repo default branch
			// when --publish-branch is empty.
			if sf.storeBackend == "artifact" {
				defaultBranch := publishBranch
				if defaultBranch == "" {
					s, err := ghrelease.New(ghrelease.Config{Key: key, Token: sf.token(), BaseURL: sf.apiURL})
					if err != nil {
						return &exitError{2, err}
					}
					info, err := s.Repo(ctx)
					if err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", err)
						return &exitError{1, err}
					}
					defaultBranch = info.DefaultBranch
				}
				return resolveOutput(cmd, "artifact", resolvePublish(mode, branch, publishBranch, defaultBranch))
			}

			// GitHub Releases backend: check repo visibility.
			s, err := ghrelease.New(ghrelease.Config{Key: key, Token: sf.token(), BaseURL: sf.apiURL})
			if err != nil {
				return &exitError{2, err}
			}
			info, err := s.Repo(ctx)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", err)
				return &exitError{1, err}
			}

			backend := "github-release"
			if info.Visibility == "public" && !allowPublicProfiles {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"::warning::%s is public; storing profiles in GitHub Actions Artifacts to keep them private. Pass --allow-public-profiles to use Releases.\n",
					sf.repo)
				backend = "artifact"
			}

			pb := publishBranch
			if pb == "" {
				pb = info.DefaultBranch
			}
			publish := resolvePublish(mode, branch, publishBranch, pb)
			if !publish && mode == "collect" {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"::notice::Branch %s is not the publish branch %s. This run does not publish a profile.\n",
					branch, pb)
			}
			return resolveOutput(cmd, backend, publish)
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "collect", "operation mode: collect or restore")
	cmd.Flags().StringVar(&branch, "branch", "", "current branch")
	cmd.Flags().StringVar(&publishBranch, "publish-branch", "", "branch that publishes (empty = default branch)")
	cmd.Flags().BoolVar(&allowPublicProfiles, "allow-public-profiles", false, "use Releases even for public repos")
	return cmd
}

// resolvePublish returns true when mode is collect and branch matches the effective publish branch.
func resolvePublish(mode, branch, publishBranch, defaultBranch string) bool {
	if mode != "collect" {
		return false
	}
	pb := publishBranch
	if pb == "" {
		pb = defaultBranch
	}
	if pb == "" {
		return false
	}
	return branch == pb
}

func resolveOutput(cmd *cobra.Command, backend string, publish bool) error {
	fmt.Fprintf(cmd.OutOrStdout(), "store=%s\n", backend)
	fmt.Fprintf(cmd.OutOrStdout(), "publish=%v\n", publish)
	return nil
}

func newStorePublishCmd(sf *storeFlags) *cobra.Command {
	var profilePath string
	var outPath string
	var runID string
	var commit string
	var branch string
	var samples int64
	var qualityScore float64
	var goVersion string
	var lookbackDays int
	var maxProfiles int

	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish a CPU pprof to the profile store",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if profilePath == "" {
				return &exitError{2, fmt.Errorf("--profile is required")}
			}
			if !store.ValidRunID(runID) {
				return &exitError{2, fmt.Errorf("invalid --run-id %q", runID)}
			}
			if lookbackDays < 1 {
				return &exitError{2, fmt.Errorf("--lookback-days must be >= 1")}
			}
			if maxProfiles < 1 {
				return &exitError{2, fmt.Errorf("--max-profiles must be >= 1")}
			}
			key, err := sf.key()
			if err != nil {
				return &exitError{2, err}
			}
			today, err := sf.todayDate()
			if err != nil {
				return &exitError{2, fmt.Errorf("--today: %w", err)}
			}

			profileData, err := os.ReadFile(profilePath) //nolint:gosec
			if err != nil {
				return &exitError{2, fmt.Errorf("reading profile: %w", err)}
			}

			s, err := sf.makeStore()
			if err != nil {
				return &exitError{2, err}
			}

			req := store.PublishRequest{
				Key:    key,
				Today:  today,
				Branch: branch,
				Run: store.RunInfo{
					ID:           runID,
					Commit:       commit,
					Samples:      samples,
					QualityScore: qualityScore,
				},
				Profile:       profileData,
				LookbackDays:  lookbackDays,
				MaxProfiles:   maxProfiles,
				GoVersion:     goVersion,
				PgoctlVersion: version,
			}

			ctx := context.Background()
			result, err := store.Publish(ctx, s, req)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", err)
				return &exitError{1, err}
			}
			if result.PruneErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "::warning::prune: %s\n", result.PruneErr)
			}

			m := result.Manifest
			fmt.Fprintf(cmd.ErrOrStderr(), "published %s: window %d days (%s)\n",
				m.Tag, len(m.Window.Dates), strings.Join(m.Window.Dates, ", "))

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				_ = enc.Encode(m)
			}

			if result.Default != nil && outPath != "" {
				if err := writeTempThenRename(outPath, result.Default); err != nil {
					return &exitError{1, fmt.Errorf("writing %s: %w", outPath, err)}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&profilePath, "profile", "", "path to cpu.pprof (required)")
	cmd.Flags().StringVar(&outPath, "out", "default.pgo", "output path for default.pgo")
	cmd.Flags().StringVar(&runID, "run-id", "", "GitHub Actions run ID (required, format: <run>-<attempt>)")
	cmd.Flags().StringVar(&commit, "commit", "", "commit SHA")
	cmd.Flags().StringVar(&branch, "branch", "", "branch name")
	cmd.Flags().Int64Var(&samples, "samples", 0, "sample count (0 = auto)")
	cmd.Flags().Float64Var(&qualityScore, "quality-score", 0, "quality score")
	cmd.Flags().StringVar(&goVersion, "go-version", "", "Go version")
	cmd.Flags().IntVar(&lookbackDays, "lookback-days", 14, "window lookback in days")
	cmd.Flags().IntVar(&maxProfiles, "max-profiles", 7, "maximum profiles in window")
	return cmd
}

func newStoreRestoreCmd(sf *storeFlags) *cobra.Command {
	var outPath string

	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore the most recent default.pgo from the profile store",
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, err := sf.key()
			if err != nil {
				return &exitError{2, err}
			}
			s, err := sf.makeStore()
			if err != nil {
				return &exitError{2, err}
			}
			ctx := context.Background()

			var buf bytes.Buffer
			m, err := store.Restore(ctx, s, key, &buf)
			if err != nil {
				if errors.Is(err, store.ErrNoSnapshot) {
					// Try legacy artifact fallback when configured.
					if as, ok := s.(*artifact.Store); ok && sf.legacyArtifact != "" {
						var legacyBuf bytes.Buffer
						if lerr := as.RestoreLegacy(ctx, &legacyBuf); lerr == nil {
							if outPath != "" {
								if werr := writeTempThenRename(outPath, legacyBuf.Bytes()); werr != nil {
									return &exitError{1, fmt.Errorf("writing %s: %w", outPath, werr)}
								}
							}
							fmt.Fprintf(cmd.ErrOrStderr(), "restored legacy artifact %s\n", sf.legacyArtifact)
							if jsonOutput {
								fmt.Fprintf(cmd.OutOrStdout(), "{\"store\":\"artifact\"}\n")
							}
							return nil
						}
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "error: no usable snapshot: %s\n", err)
					return &exitError{3, err}
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", err)
				return &exitError{1, err}
			}

			if outPath != "" {
				if err := writeTempThenRename(outPath, buf.Bytes()); err != nil {
					return &exitError{1, fmt.Errorf("writing %s: %w", outPath, err)}
				}
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "restored %s (window: %d days)\n",
				m.Tag, len(m.Window.Dates))
			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				_ = enc.Encode(m)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "default.pgo", "output path for default.pgo")
	return cmd
}

func newStoreExcludeCmd(sf *storeFlags) *cobra.Command {
	var dateStr string
	var runID string
	var reason string
	var actor string
	var pairs string

	cmd := &cobra.Command{
		Use:   "exclude",
		Short: "Exclude a run from the profile window",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if pairs != "" && dateStr != "" {
				return &exitError{2, fmt.Errorf("--pairs is exclusive with --date")}
			}
			key, err := sf.key()
			if err != nil {
				return &exitError{2, err}
			}
			s, err := sf.makeStore()
			if err != nil {
				return &exitError{2, err}
			}
			ctx := context.Background()

			type pairReq struct {
				date  store.Date
				runID string
			}

			var reqs []pairReq
			if pairs != "" {
				for _, pair := range splitPairs(pairs) {
					colon := strings.Index(pair, ":")
					if colon < 0 {
						return &exitError{2, fmt.Errorf("invalid pair %q: expected date:run-id", pair)}
					}
					d, err := store.ParseDate(pair[:colon])
					if err != nil {
						return &exitError{2, fmt.Errorf("invalid date in pair %q: %w", pair, err)}
					}
					id := pair[colon+1:]
					if !store.ValidRunID(id) {
						return &exitError{2, fmt.Errorf("invalid run ID %q in pair %q", id, pair)}
					}
					reqs = append(reqs, pairReq{date: d, runID: id})
				}
			} else {
				if dateStr == "" {
					return &exitError{2, fmt.Errorf("--date is required")}
				}
				if !store.ValidRunID(runID) {
					return &exitError{2, fmt.Errorf("invalid --run %q", runID)}
				}
				d, err := store.ParseDate(dateStr)
				if err != nil {
					return &exitError{2, err}
				}
				reqs = append(reqs, pairReq{date: d, runID: runID})
			}

			for _, r := range reqs {
				req := store.ExcludeRequest{
					Key:    key,
					Date:   r.date,
					RunID:  r.runID,
					Reason: reason,
					Actor:  actor,
					Now:    time.Now().UTC(),
				}
				result, err := store.Exclude(ctx, s, req)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", err)
					return &exitError{1, err}
				}
				if result.PruneErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "::warning::prune: %s\n", result.PruneErr)
				}
				if result.Skipped {
					fmt.Fprintf(cmd.OutOrStdout(),
						"::notice::%s has no profile (pruned or never published); nothing to exclude\n",
						r.date)
					continue
				}
				if result.AlreadyExcluded {
					fmt.Fprintf(cmd.OutOrStdout(), "%s on %s already excluded\n", r.runID, r.date)
					continue
				}
				var rebuilt []string
				for _, d := range result.Rebuilt {
					rebuilt = append(rebuilt, d.String())
				}
				if len(rebuilt) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "excluded %s on %s; rebuilt %s\n",
						r.runID, r.date, strings.Join(rebuilt, ", "))
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "excluded %s on %s\n", r.runID, r.date)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dateStr, "date", "", "date of the run to exclude (YYYY-MM-DD)")
	cmd.Flags().StringVar(&runID, "run", "", "run ID to exclude")
	cmd.Flags().StringVar(&reason, "reason", "", "exclusion reason")
	cmd.Flags().StringVar(&actor, "actor", "", "actor performing the exclusion")
	cmd.Flags().StringVar(&pairs, "pairs", "", "comma-separated date:run-id pairs")
	return cmd
}

func newStorePruneCmd(sf *storeFlags) *cobra.Command {
	var lookbackDays int
	var maxProfiles int

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove profile days outside the rolling window",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if lookbackDays < 1 {
				return &exitError{2, fmt.Errorf("--lookback-days must be >= 1")}
			}
			if maxProfiles < 1 {
				return &exitError{2, fmt.Errorf("--max-profiles must be >= 1")}
			}
			key, err := sf.key()
			if err != nil {
				return &exitError{2, err}
			}
			today, err := sf.todayDate()
			if err != nil {
				return &exitError{2, fmt.Errorf("--today: %w", err)}
			}
			s, err := sf.makeStore()
			if err != nil {
				return &exitError{2, err}
			}
			ctx := context.Background()

			pruned, err := store.Prune(ctx, s, key, today, lookbackDays, maxProfiles)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", err)
				return &exitError{1, err}
			}
			if len(pruned) > 0 {
				var dates []string
				for _, d := range pruned {
					dates = append(dates, d.String())
				}
				fmt.Fprintf(cmd.OutOrStdout(), "pruned: %s\n", strings.Join(dates, ", "))
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&lookbackDays, "lookback-days", 14, "window lookback in days")
	cmd.Flags().IntVar(&maxProfiles, "max-profiles", 7, "maximum profiles in window")
	return cmd
}

// splitPairs splits a string of date:run-id pairs separated by commas or whitespace.
func splitPairs(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	}) {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// writeTempThenRename writes data to a temp file in the same directory as dst,
// then renames it to dst.
func writeTempThenRename(dst string, data []byte) error {
	dir := "."
	if i := strings.LastIndex(dst, "/"); i >= 0 {
		dir = dst[:i]
	}
	f, err := os.CreateTemp(dir, ".pgoctl-tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}
