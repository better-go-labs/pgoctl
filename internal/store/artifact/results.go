package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// upload publishes name from stagingDir using the actions/toolkit results service.
// The protocol is a snake_case Twirp JSON protocol; the source commit for the
// toolkit is: actions/toolkit@main (results-service protocol, as of 2026-10).
func upload(ctx context.Context, cfg Config, hc *http.Client, name, stagingDir string) error {
	if cfg.RuntimeToken == "" || cfg.ResultsURL == "" {
		return fmt.Errorf("the artifact store needs ACTIONS_RUNTIME_TOKEN and ACTIONS_RESULTS_URL; the Action exports them before the publish")
	}

	wfRunID, wfJobID, err := extractBackendIDs(cfg.RuntimeToken)
	if err != nil {
		return fmt.Errorf("extracting backend IDs from token: %w", err)
	}

	origin, err := resultsOrigin(cfg.ResultsURL)
	if err != nil {
		return fmt.Errorf("parsing ResultsURL: %w", err)
	}

	// Step 3: CreateArtifact.
	createURL := origin + "/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact"
	createBody := map[string]interface{}{
		"workflow_run_backend_id":     wfRunID,
		"workflow_job_run_backend_id": wfJobID,
		"name":                        name,
		"mime_type":                   "application/zip",
		"version":                     7,
	}
	createResp, err := postJSON(ctx, hc, createURL, cfg.RuntimeToken, createBody)
	if err != nil {
		return fmt.Errorf("CreateArtifact: %w", err)
	}
	signedUploadURL, ok := createResp["signed_upload_url"].(string)
	if !ok || signedUploadURL == "" {
		return fmt.Errorf("CreateArtifact: no signed_upload_url in response")
	}

	// Step 4: zip the staging directory.
	zipData, err := zipDir(stagingDir)
	if err != nil {
		return fmt.Errorf("zipping staging dir: %w", err)
	}

	// Step 5: PUT to the signed upload URL (one PUT, not block upload).
	if err := putBlob(ctx, hc, signedUploadURL, zipData); err != nil {
		return fmt.Errorf("uploading artifact: %w", err)
	}

	// Step 6: FinalizeArtifact.
	sum := sha256.Sum256(zipData)
	hash := fmt.Sprintf("sha256:%x", sum)
	finalizeURL := origin + "/twirp/github.actions.results.api.v1.ArtifactService/FinalizeArtifact"
	finalizeBody := map[string]interface{}{
		"workflow_run_backend_id":     wfRunID,
		"workflow_job_run_backend_id": wfJobID,
		"name":                        name,
		"size":                        fmt.Sprintf("%d", len(zipData)),
		"hash":                        hash,
	}
	if _, err := postJSON(ctx, hc, finalizeURL, cfg.RuntimeToken, finalizeBody); err != nil {
		return fmt.Errorf("FinalizeArtifact: %w", err)
	}
	return nil
}

// extractBackendIDs decodes the JWT payload (without verification) and extracts
// workflow_run_backend_id and workflow_job_run_backend_id from the scp claim,
// which has the form "Actions.Results:<wf>:<job>".
func extractBackendIDs(token string) (wfRunID, wfJobID string, err error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("token is not a JWT (expected at least header.payload)")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", fmt.Errorf("decoding JWT payload: %w", err)
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", "", fmt.Errorf("parsing JWT payload: %w", err)
	}
	scp, _ := claims["scp"].(string)
	// scp format: "Actions.Results:<wf_run_id>:<wf_job_run_id>"
	for _, field := range strings.Fields(scp) {
		if strings.HasPrefix(field, "Actions.Results:") {
			rest := strings.TrimPrefix(field, "Actions.Results:")
			colon := strings.Index(rest, ":")
			if colon < 0 {
				return "", "", fmt.Errorf("scp claim %q: expected Actions.Results:<wf>:<job>", field)
			}
			return rest[:colon], rest[colon+1:], nil
		}
	}
	return "", "", fmt.Errorf("scp claim %q does not contain Actions.Results scope", scp)
}

// resultsOrigin returns the origin (scheme+host) of the results URL.
func resultsOrigin(resultsURL string) (string, error) {
	u, err := url.Parse(resultsURL)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s://%s", u.Scheme, u.Host), nil
}

// postJSON sends a JSON POST and returns the decoded response body.
func postJSON(ctx context.Context, hc *http.Client, endpoint, token string, body interface{}) (map[string]interface{}, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", endpoint, err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, fmt.Errorf("POST %s returned %d: %s", endpoint, resp.StatusCode, body)
	}
	var out map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return out, nil
}

// putBlob uploads data to url as a block blob in a single PUT.
func putBlob(ctx context.Context, hc *http.Client, endpoint string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("x-ms-blob-type", "BlockBlob")
	req.Header.Set("Content-Type", "application/zip")
	req.ContentLength = int64(len(data))
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("PUT %s: %w", endpoint, err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("PUT %s returned %d: %s", endpoint, resp.StatusCode, body)
	}
	return nil
}
