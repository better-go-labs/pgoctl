package ghrelease

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
)

// apiRelease is the subset of the GitHub Releases API response used here.
type apiRelease struct {
	ID          int64      `json:"id"`
	TagName     string     `json:"tag_name"`
	Name        string     `json:"name"`
	Draft       bool       `json:"draft"`
	Prerelease  bool       `json:"prerelease"`
	Immutable   bool       `json:"immutable"`
	UploadURL   string     `json:"upload_url"`
	Assets      []apiAsset `json:"assets"`
	PublishedAt *time.Time `json:"published_at"`
}

// apiAsset is an entry in the GitHub release assets list.
type apiAsset struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	UpdatedAt   time.Time `json:"updated_at"`
	ContentType string    `json:"content_type"`
}

// apiRepoResponse is the subset of GET /repos/{o}/{r} used here.
type apiRepoResponse struct {
	DefaultBranch string `json:"default_branch"`
	Visibility    string `json:"visibility"`
	Private       bool   `json:"private"`
}

func (c *client) do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return c.http.Do(req)
}

func (c *client) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

func (c *client) post(ctx context.Context, path string, body interface{}) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

func (c *client) patch(ctx context.Context, path string, body interface{}) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

func (c *client) delete(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

// checkResp drains and closes the body and returns a mapped error for non-2xx.
// method and path are used only in error messages.
func (c *client) checkResp(resp *http.Response, method, path string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return mapError(resp, method, path, body)
}

// mapError converts a non-2xx response to a sentinel error.
func mapError(resp *http.Response, method, path string, body []byte) error {
	code := resp.StatusCode

	switch {
	case code == 404:
		return fmt.Errorf("%s %s returned 404: %w", method, path, store.ErrNotFound)

	case isRateLimit(resp, body):
		reset := resp.Header.Get("Retry-After")
		if reset == "" {
			if ts := resp.Header.Get("X-RateLimit-Reset"); ts != "" {
				if n, err := strconv.ParseInt(ts, 10, 64); err == nil {
					reset = time.Unix(n, 0).UTC().Format(time.RFC3339)
				}
			}
		}
		if reset != "" {
			return fmt.Errorf("GitHub API rate limit exceeded; retry after %s", reset)
		}
		return fmt.Errorf("GitHub API rate limit exceeded")

	case code == 403 || (code == 404 && (method == http.MethodPost || method == http.MethodPatch || method == http.MethodDelete)):
		return fmt.Errorf("%s %s returned %d: the job token cannot write releases. Grant the publish job \"permissions: contents: write\": %w",
			method, path, code, store.ErrPermission)

	case code == 422 && strings.Contains(strings.ToLower(string(body)), "immutable"):
		return fmt.Errorf("%s %s returned 422: %w", method, path, store.ErrImmutable)

	case code == 401:
		return fmt.Errorf("GitHub rejected the token")

	default:
		preview := body
		if len(preview) > 200 {
			preview = preview[:200]
		}
		return fmt.Errorf("%s %s %d: %s", method, path, code, preview)
	}
}

// isRateLimit detects GitHub rate-limit 403 responses.
func isRateLimit(resp *http.Response, body []byte) bool {
	if resp.StatusCode == 429 {
		return true
	}
	if resp.StatusCode != 403 {
		return false
	}
	if resp.Header.Get("Retry-After") != "" {
		return true
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return true
	}
	return strings.Contains(strings.ToLower(string(body)), "rate limit")
}

func (c *client) getRepoInfo(ctx context.Context, owner, repo string) (*RepoInfo, error) {
	path := fmt.Sprintf("/repos/%s/%s", owner, repo)
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if err := c.checkResp(resp, http.MethodGet, path); err != nil {
		return nil, err
	}
	var r apiRepoResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("decoding repo response: %w", err)
	}
	vis := r.Visibility
	if vis == "" {
		if r.Private {
			vis = "private"
		} else {
			vis = "public"
		}
	}
	return &RepoInfo{DefaultBranch: r.DefaultBranch, Visibility: vis}, nil
}

// listDays fetches all releases matching key, across all pages.
func (c *client) listDays(ctx context.Context, key store.Key, owner, repo string) ([]store.Day, error) {
	var all []apiRelease
	path := fmt.Sprintf("/repos/%s/%s/releases?per_page=100", owner, repo)
	for path != "" {
		// nextLink returns a full URL; only prepend baseURL for relative paths.
		var fullURL string
		if strings.HasPrefix(path, "http") {
			fullURL = path
		} else {
			fullURL = c.baseURL + path
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			return nil, fmt.Errorf("building request: %w", err)
		}
		resp, err := c.do(req)
		if err != nil {
			return nil, fmt.Errorf("GET %s: %w", path, err)
		}
		if err := func() error {
			defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
			if err := c.checkResp(resp, http.MethodGet, path); err != nil {
				return err
			}
			var page []apiRelease
			if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
				return fmt.Errorf("decoding releases: %w", err)
			}
			all = append(all, page...)
			path = nextLink(resp.Header.Get("Link"))
			return nil
		}(); err != nil {
			return nil, err
		}
	}

	var out []store.Day
	for _, rel := range all {
		d, seq, ok := key.ParseTag(rel.TagName)
		if !ok {
			continue
		}
		assets := make(map[string]store.Asset)
		for _, a := range rel.Assets {
			if strings.HasPrefix(a.Name, "tmp-") {
				continue
			}
			logical := wireToLogical(a.Name)
			assets[logical] = store.Asset{
				ID:        strconv.FormatInt(a.ID, 10),
				Name:      logical,
				Size:      a.Size,
				UpdatedAt: a.UpdatedAt,
			}
		}
		day := store.Day{
			Key:       key,
			Date:      d,
			Seq:       seq,
			Draft:     rel.Draft,
			Immutable: rel.Immutable,
			ID:        strconv.FormatInt(rel.ID, 10),
			Assets:    assets,
		}
		out = append(out, day)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date.After(out[j].Date)
		}
		return out[i].Seq > out[j].Seq
	})
	return out, nil
}

// nextLink extracts the URL of the "next" relation from a GitHub Link header.
func nextLink(header string) string {
	if header == "" {
		return ""
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		segments := strings.Split(part, ";")
		if len(segments) < 2 {
			continue
		}
		url := strings.TrimSpace(segments[0])
		url = strings.Trim(url, "<>")
		for _, seg := range segments[1:] {
			seg = strings.TrimSpace(seg)
			if seg == `rel="next"` {
				return url
			}
		}
	}
	return ""
}

// readAsset fetches asset content. It follows one redirect and drops the
// Authorization header when the redirect goes to a different host.
func (c *client) readAsset(ctx context.Context, owner, repo, assetID string) (io.ReadCloser, error) {
	path := fmt.Sprintf("/repos/%s/%s/releases/assets/%s", owner, repo, assetID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	// Use a client that drops Authorization on cross-host redirects.
	noAuthRedirect := &http.Client{
		Transport: c.http.Transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 0 {
				origHost := via[0].URL.Host
				if req.URL.Host != origHost {
					req.Header.Del("Authorization")
				}
			}
			return nil
		},
	}
	resp, err := noAuthRedirect.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reading asset %s: %w", assetID, err)
	}
	if err := c.checkResp(resp, http.MethodGet, path); err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// beginDay creates or returns the day for key/date/seq.
func (c *client) beginDay(ctx context.Context, key store.Key, owner, repo string, date store.Date, seq int) (store.Day, error) {
	// Check if a release with this tag already exists.
	tag := key.Tag(date, seq)
	days, err := c.listDays(ctx, key, owner, repo)
	if err != nil {
		return store.Day{}, err
	}
	for _, d := range days {
		if d.Date == date && d.Seq == seq {
			return d, nil
		}
	}

	// Create a new draft release.
	body := map[string]interface{}{
		"tag_name":    tag,
		"name":        tag,
		"draft":       true,
		"prerelease":  true,
		"make_latest": "false",
	}
	path := fmt.Sprintf("/repos/%s/%s/releases", owner, repo)
	resp, err := c.post(ctx, path, body)
	if err != nil {
		return store.Day{}, fmt.Errorf("POST %s: %w", path, err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if err := c.checkResp(resp, http.MethodPost, path); err != nil {
		return store.Day{}, err
	}
	var rel apiRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return store.Day{}, fmt.Errorf("decoding release: %w", err)
	}
	return store.Day{
		Key:    key,
		Date:   date,
		Seq:    seq,
		Draft:  true,
		ID:     strconv.FormatInt(rel.ID, 10),
		Assets: map[string]store.Asset{},
	}, nil
}

// putAsset uploads r as name in the release identified by day.
func (c *client) putAsset(ctx context.Context, owner, repo string, day store.Day, name string, r io.Reader) error {
	wire := logicalToWire(name)
	tmpWire := "tmp-" + wire

	// Read all data upfront to allow replays for the replace sequence.
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("reading asset data: %w", err)
	}

	// Get the current release to check if the asset already exists.
	rel, err := c.getRelease(ctx, owner, repo, day.ID)
	if err != nil {
		return fmt.Errorf("refreshing release: %w", err)
	}

	// Find existing wire and tmp wire assets.
	var existingID, tmpID string
	for _, a := range rel.Assets {
		if a.Name == wire {
			existingID = strconv.FormatInt(a.ID, 10)
		}
		if a.Name == tmpWire {
			tmpID = strconv.FormatInt(a.ID, 10)
		}
	}

	// Step 1: delete stale tmp asset if it exists.
	if tmpID != "" {
		if err := c.deleteAsset(ctx, owner, repo, tmpID); err != nil {
			return fmt.Errorf("deleting stale tmp asset: %w", err)
		}
	}

	// Step 2: upload as tmp-<wire>.
	uploadURL := stripUploadTemplate(rel.UploadURL)
	newID, err := c.uploadAsset(ctx, uploadURL, tmpWire, data)
	if err != nil {
		return fmt.Errorf("uploading %s: %w", tmpWire, err)
	}

	// Step 3: delete the old <wire> asset.
	if existingID != "" {
		if err := c.deleteAsset(ctx, owner, repo, existingID); err != nil {
			return fmt.Errorf("deleting old asset %s: %w", wire, err)
		}
	}

	// Step 4: rename tmp-<wire> to <wire>.
	path := fmt.Sprintf("/repos/%s/%s/releases/assets/%s", owner, repo, newID)
	resp, err := c.patch(ctx, path, map[string]string{"name": wire})
	if err != nil {
		return fmt.Errorf("PATCH %s: %w", path, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return c.checkResp(resp, http.MethodPatch, path)
}

// uploadAsset POSTs data to the release upload URL.
func (c *client) uploadAsset(ctx context.Context, uploadURL, name string, data []byte) (string, error) {
	url := uploadURL + "?name=" + name
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", url, err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", mapError(resp, http.MethodPost, url, body)
	}
	var a apiAsset
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		return "", fmt.Errorf("decoding upload response: %w", err)
	}
	return strconv.FormatInt(a.ID, 10), nil
}

// getRelease fetches a single release by its numeric ID.
func (c *client) getRelease(ctx context.Context, owner, repo, id string) (*apiRelease, error) {
	path := fmt.Sprintf("/repos/%s/%s/releases/%s", owner, repo, id)
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if err := c.checkResp(resp, http.MethodGet, path); err != nil {
		return nil, err
	}
	var rel apiRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decoding release: %w", err)
	}
	return &rel, nil
}

// deleteAsset deletes a release asset by its ID string.
func (c *client) deleteAsset(ctx context.Context, owner, repo, id string) error {
	path := fmt.Sprintf("/repos/%s/%s/releases/assets/%s", owner, repo, id)
	resp, err := c.delete(ctx, path)
	if err != nil {
		return fmt.Errorf("DELETE %s: %w", path, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return c.checkResp(resp, http.MethodDelete, path)
}

// publishDay marks the release as published (draft=false, prerelease=true).
func (c *client) publishDay(ctx context.Context, owner, repo string, day store.Day) error {
	path := fmt.Sprintf("/repos/%s/%s/releases/%s", owner, repo, day.ID)
	body := map[string]interface{}{
		"draft":       false,
		"prerelease":  true,
		"make_latest": "false",
	}
	resp, err := c.patch(ctx, path, body)
	if err != nil {
		return fmt.Errorf("PATCH %s: %w", path, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return c.checkResp(resp, http.MethodPatch, path)
}

// deleteDay removes the release and its tag ref.
func (c *client) deleteDay(ctx context.Context, owner, repo string, day store.Day) error {
	path := fmt.Sprintf("/repos/%s/%s/releases/%s", owner, repo, day.ID)
	resp, err := c.delete(ctx, path)
	if err != nil {
		return fmt.Errorf("DELETE %s: %w", path, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if err := c.checkResp(resp, http.MethodDelete, path); err != nil {
		return err
	}

	// Skip tag deletion for drafts; they have no tag ref.
	if day.Draft {
		return nil
	}

	// Delete the tag ref. Ignore 404.
	tag := day.Key.Tag(day.Date, day.Seq)
	tagPath := fmt.Sprintf("/repos/%s/%s/git/refs/tags/%s", owner, repo, tag)
	tagResp, err := c.delete(ctx, tagPath)
	if err != nil {
		return fmt.Errorf("DELETE %s: %w", tagPath, err)
	}
	_, _ = io.Copy(io.Discard, tagResp.Body)
	_ = tagResp.Body.Close()
	if tagResp.StatusCode == 404 {
		return nil
	}
	return c.checkResp(tagResp, http.MethodDelete, tagPath)
}

// stripUploadTemplate removes the {?name,label} template suffix from an upload URL.
func stripUploadTemplate(u string) string {
	if i := strings.Index(u, "{"); i >= 0 {
		return u[:i]
	}
	return u
}
