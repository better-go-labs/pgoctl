package collect

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFromParca_ConnectProtocolHeader(t *testing.T) {
	fakeData := []byte("FAKE_PPROF_DATA")
	var gotAccept, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mergeResponse{
			Pprof: base64.StdEncoding.EncodeToString(fakeData),
		})
	}))
	defer srv.Close()

	opts := Options{
		Source:    SourceParca,
		ParcaAddr: srv.URL,
		Query:     "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
		Window:    5 * time.Minute,
		End:       time.Now(),
		Timeout:   5 * time.Second,
	}
	_, err := FromParca(opts)
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, gotMethod)
	require.Equal(t, "application/json", gotAccept)
}

func TestFromParca_Success(t *testing.T) {
	fakeData := []byte("FAKE_PPROF_DATA")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "MODE_MERGE", r.URL.Query().Get("mode"))
		require.NotEmpty(t, r.URL.Query().Get("merge.query"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mergeResponse{
			Pprof: base64.StdEncoding.EncodeToString(fakeData),
		})
	}))
	defer srv.Close()

	opts := Options{
		Source:    SourceParca,
		ParcaAddr: srv.URL,
		Query:     "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
		Window:    5 * time.Minute,
		End:       time.Now(),
		Timeout:   5 * time.Second,
	}
	result, err := FromParca(opts)
	require.NoError(t, err)
	require.Equal(t, fakeData, result.Bytes)
	require.Equal(t, len(result.Bytes), result.SizeBytes)
}

func TestFromParca_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("service unavailable"))
	}))
	defer srv.Close()

	opts := Options{
		Source:    SourceParca,
		ParcaAddr: srv.URL,
		Query:     "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
		Window:    5 * time.Minute,
		End:       time.Now(),
		Timeout:   5 * time.Second,
	}
	_, err := FromParca(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "503")
}

func TestFromParca_EmptyProfile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mergeResponse{Pprof: ""})
	}))
	defer srv.Close()

	opts := Options{
		Source:    SourceParca,
		ParcaAddr: srv.URL,
		Query:     "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
		Window:    5 * time.Minute,
		End:       time.Now(),
		Timeout:   5 * time.Second,
	}
	_, err := FromParca(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty profile")
}

func TestFromParca_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("invalid json {"))
	}))
	defer srv.Close()

	opts := Options{
		Source:    SourceParca,
		ParcaAddr: srv.URL,
		Query:     "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
		Window:    5 * time.Minute,
		End:       time.Now(),
		Timeout:   5 * time.Second,
	}
	_, err := FromParca(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode response")
}

func TestFromParca_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mergeResponse{
			Code:    "INVALID_QUERY",
			Message: "query failed",
		})
	}))
	defer srv.Close()

	opts := Options{
		Source:    SourceParca,
		ParcaAddr: srv.URL,
		Query:     "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
		Window:    5 * time.Minute,
		End:       time.Now(),
		Timeout:   5 * time.Second,
	}
	_, err := FromParca(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "INVALID_QUERY")
}

func TestFromParca_BadBase64(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mergeResponse{
			Pprof: "not-base64!!!",
		})
	}))
	defer srv.Close()

	opts := Options{
		Source:    SourceParca,
		ParcaAddr: srv.URL,
		Query:     "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
		Window:    5 * time.Minute,
		End:       time.Now(),
		Timeout:   5 * time.Second,
	}
	_, err := FromParca(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "decode pprof base64")
}

func TestFromParca_BadAddr(t *testing.T) {
	opts := Options{
		Source:    SourceParca,
		ParcaAddr: "http://[invalid",
		Query:     "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
		Window:    5 * time.Minute,
		End:       time.Now(),
		Timeout:   5 * time.Second,
	}
	_, err := FromParca(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse addr")
}

func TestFromParca_ZeroEnd(t *testing.T) {
	fakeData := []byte("FAKE_PPROF_DATA")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mergeResponse{
			Pprof: base64.StdEncoding.EncodeToString(fakeData),
		})
	}))
	defer srv.Close()

	opts := Options{
		Source:    SourceParca,
		ParcaAddr: srv.URL,
		Query:     "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
		Window:    5 * time.Minute,
		End:       time.Time{}, // zero time should use time.Now()
		Timeout:   5 * time.Second,
	}
	result, err := FromParca(opts)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, fakeData, result.Bytes)
}

func TestFromParca_ZeroTimeout(t *testing.T) {
	fakeData := []byte("FAKE_PPROF_DATA")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(mergeResponse{
			Pprof: base64.StdEncoding.EncodeToString(fakeData),
		})
	}))
	defer srv.Close()

	opts := Options{
		Source:    SourceParca,
		ParcaAddr: srv.URL,
		Query:     "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
		Window:    5 * time.Minute,
		End:       time.Now(),
		Timeout:   0, // zero timeout should use default
	}
	result, err := FromParca(opts)
	require.NoError(t, err)
	require.NotNil(t, result)
}
