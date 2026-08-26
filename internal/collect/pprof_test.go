package collect

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/require"
)

func buildProfileBytes(t *testing.T, sampleCount int) []byte {
	t.Helper()
	fn := &profile.Function{ID: 1, Name: "main.hotPath"}
	p := &profile.Profile{
		SampleType:    []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
		PeriodType:    &profile.ValueType{Type: "cpu", Unit: "nanoseconds"},
		Period:        10000000,
		DurationNanos: 30 * 1e9,
		TimeNanos:     time.Now().UnixNano(),
		Function:      []*profile.Function{fn},
	}
	for i := 0; i < sampleCount; i++ {
		loc := &profile.Location{
			ID:   uint64(i + 1),
			Line: []profile.Line{{Function: fn, Line: int64(i + 1)}},
		}
		p.Location = append(p.Location, loc)
		p.Sample = append(p.Sample, &profile.Sample{
			Location: []*profile.Location{loc},
			Value:    []int64{1000000},
		})
	}
	var buf bytes.Buffer
	require.NoError(t, p.Write(&buf))
	return buf.Bytes()
}

func TestFromPprof_Success(t *testing.T) {
	profileData := buildProfileBytes(t, 100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(profileData)
	}))
	defer srv.Close()

	opts := Options{
		URL:     srv.URL + "/debug/pprof/profile",
		Window:  5 * time.Second,
		Timeout: 5 * time.Second,
	}
	result, err := FromPprof(opts)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, SourcePprof, result.Source)
	require.Equal(t, len(profileData), result.SizeBytes)
	require.Equal(t, profileData, result.Bytes)
}

func TestFromPprof_WithWindow(t *testing.T) {
	profileData := buildProfileBytes(t, 100)
	var gotSeconds string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSeconds = r.URL.Query().Get("seconds")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(profileData)
	}))
	defer srv.Close()

	opts := Options{
		URL:     srv.URL + "/debug/pprof/profile",
		Window:  10 * time.Second,
		Timeout: 5 * time.Second,
	}
	_, err := FromPprof(opts)
	require.NoError(t, err)
	require.Equal(t, "10", gotSeconds)
}

func TestFromPprof_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	opts := Options{
		URL:     srv.URL + "/debug/pprof/profile",
		Timeout: 5 * time.Second,
	}
	_, err := FromPprof(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
}

func TestFromPprof_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte{})
	}))
	defer srv.Close()

	opts := Options{
		URL:     srv.URL + "/debug/pprof/profile",
		Timeout: 5 * time.Second,
	}
	_, err := FromPprof(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty profile")
}

func TestFromPprof_BadURL(t *testing.T) {
	opts := Options{
		URL:     "http://[invalid-url",
		Timeout: 5 * time.Second,
	}
	_, err := FromPprof(opts)
	require.Error(t, err)
}

func TestFromPprof_ConnectionError(t *testing.T) {
	opts := Options{
		URL:     "http://localhost:1",
		Timeout: 100 * time.Millisecond,
	}
	_, err := FromPprof(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "get")
}

func TestFromPprof_ReadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Close the connection without writing full data
		w.(http.Flusher).Flush()
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()

	opts := Options{
		URL:     srv.URL + "/debug/pprof/profile",
		Timeout: 5 * time.Second,
	}
	_, err := FromPprof(opts)
	require.Error(t, err)
}
