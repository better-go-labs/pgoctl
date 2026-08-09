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

func TestCollectFromParca_Success(t *testing.T) {
	fakeData := []byte("FAKE_PPROF_DATA")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(mergeResponse{
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
	result, err := CollectFromParca(opts)
	require.NoError(t, err)
	require.Equal(t, fakeData, result.Bytes)
	require.Equal(t, len(result.Bytes), result.SizeBytes)
}

func TestCollectFromParca_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("service unavailable"))
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
	_, err := CollectFromParca(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "503")
}

func TestCollectFromParca_EmptyProfile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(mergeResponse{Pprof: ""})
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
	_, err := CollectFromParca(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty profile")
}
