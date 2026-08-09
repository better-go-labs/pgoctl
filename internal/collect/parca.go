package collect

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type mergeRequest struct {
	Start      string `json:"start"`
	End        string `json:"end"`
	Query      string `json:"query"`
	ReportType string `json:"reportType"`
}

type mergeResponse struct {
	Pprof   string `json:"pprof"`   // base64-encoded bytes
	Code    string `json:"code"`    // present on error
	Message string `json:"message"` // present on error
}

// CollectFromParca fetches a merged CPU pprof from a Parca server using the
// Connect/gRPC-JSON MergeProfile endpoint.
func CollectFromParca(opts Options) (*Result, error) {
	end := opts.End
	if end.IsZero() {
		end = time.Now()
	}
	start := end.Add(-opts.Window)

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	reqBody := mergeRequest{
		Start:      start.UTC().Format(time.RFC3339Nano),
		End:        end.UTC().Format(time.RFC3339Nano),
		Query:      opts.Query,
		ReportType: "REPORT_TYPE_PPROF",
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("collect parca: marshal request: %w", err)
	}

	url := opts.ParcaAddr + "/parca.query.v1alpha1.QueryService/MergeProfile"
	httpClient := &http.Client{Timeout: timeout}
	resp, err := httpClient.Post(url, "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("collect parca: post %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("collect parca: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		snippet := respBytes
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("collect parca: server returned %d: %s", resp.StatusCode, snippet)
	}

	var mergeResp mergeResponse
	if err := json.Unmarshal(respBytes, &mergeResp); err != nil {
		return nil, fmt.Errorf("collect parca: decode response: %w", err)
	}

	if mergeResp.Code != "" {
		return nil, fmt.Errorf("collect parca: server error %s: %s", mergeResp.Code, mergeResp.Message)
	}

	profileBytes, err := base64.StdEncoding.DecodeString(mergeResp.Pprof)
	if err != nil {
		return nil, fmt.Errorf("collect parca: decode pprof base64: %w", err)
	}

	if len(profileBytes) == 0 {
		return nil, fmt.Errorf("collect parca: parca returned empty profile")
	}

	return &Result{
		Bytes:     profileBytes,
		Source:    SourceParca,
		Start:     start,
		End:       end,
		SizeBytes: len(profileBytes),
	}, nil
}
