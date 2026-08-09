package collect

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func buildTestProfile(t *testing.T) []byte {
	t.Helper()
	p := &profile.Profile{
		SampleType:    []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
		DurationNanos: 5 * 1e9,
	}
	fn := &profile.Function{ID: 1, Name: "pkg.Fn"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn}}}
	p.Function = append(p.Function, fn)
	p.Location = append(p.Location, loc)
	p.Sample = append(p.Sample, &profile.Sample{
		Location: []*profile.Location{loc},
		Value:    []int64{1000},
	})
	var buf bytes.Buffer
	require.NoError(t, p.Write(&buf))
	return buf.Bytes()
}

func TestCollect_Parca_OK(t *testing.T) {
	pprofData := buildTestProfile(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/debug/pprof/profile", r.URL.Path)
		assert.Equal(t, "5", r.URL.Query().Get("seconds"))
		w.WriteHeader(http.StatusOK)
		w.Write(pprofData)
	}))
	defer srv.Close()

	opts := Options{
		Source:   SourceParca,
		URL:      srv.URL,
		Duration: 5 * time.Second,
		Timeout:  10 * time.Second,
	}
	data, err := Collect(context.Background(), opts)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// Must be a valid pprof.
	p, err := profile.ParseData(data)
	require.NoError(t, err)
	assert.Equal(t, int64(5*1e9), p.DurationNanos)
}

func TestCollect_Parca_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := Collect(context.Background(), Options{
		Source:   SourceParca,
		URL:      srv.URL,
		Duration: 5 * time.Second,
		Timeout:  10 * time.Second,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
}

func TestCollect_Parca_NotPprof(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not a pprof file"))
	}))
	defer srv.Close()

	_, err := Collect(context.Background(), Options{
		Source:   SourceParca,
		URL:      srv.URL,
		Duration: 5 * time.Second,
		Timeout:  10 * time.Second,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid pprof")
}

func TestCollect_UnknownSource(t *testing.T) {
	_, err := Collect(context.Background(), Options{
		Source: "prometheus",
		URL:    "http://localhost:9090",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown source")
}

func TestCollect_InvalidURL(t *testing.T) {
	_, err := Collect(context.Background(), Options{
		Source: SourceParca,
		URL:    "://bad-url",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid URL")
}
