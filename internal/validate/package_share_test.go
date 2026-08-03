package validate_test

import (
	"testing"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Better-Go-Labs/pgoctl/internal/validate"
)

// syntheticCPUProfile builds an in-memory CPU profile with the given
// leafFunction -> sampleValue (cpu nanoseconds) attribution.
func syntheticCPUProfile(t *testing.T, funcs map[string]int64) *profile.Profile {
	t.Helper()
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
	}
	fns := make(map[string]*profile.Function)
	for name := range funcs {
		fns[name] = &profile.Function{Name: name}
	}
	for name, val := range funcs {
		fn := fns[name]
		p.Sample = append(p.Sample, &profile.Sample{
			Value:    []int64{val},
			Location: []*profile.Location{{Line: []profile.Line{{Function: fn}}}},
		})
	}
	return p
}

func TestPackageFromFunction(t *testing.T) {
	cases := []struct{ name, want string }{
		{"github.com/prometheus/prometheus/tsdb.(*Head).Append", "github.com/prometheus/prometheus/tsdb"},
		{"github.com/prometheus/prometheus/tsdb/wlog.(*Watcher).run", "github.com/prometheus/prometheus/tsdb/wlog"},
		{"github.com/prometheus/prometheus/tsdb/chunkenc.(*XorChunk).Appender", "github.com/prometheus/prometheus/tsdb/chunkenc"},
		{"github.com/prometheus/prometheus/tsdb/index.(*Reader).Symbols", "github.com/prometheus/prometheus/tsdb/index"},
		{"github.com/prometheus/prometheus/promql.(*Engine).exec", "github.com/prometheus/prometheus/promql"},
		{"github.com/prometheus/prometheus/model/labels.NewBuilder", "github.com/prometheus/prometheus/model/labels"},
		{"runtime.main", "runtime"},
		{"main.main", "main"},
		{"testing.(*T).Run", "testing"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, validate.PackageFromFunction(c.name), "function %q", c.name)
	}
}

func TestParsePackageShareGates(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		gates, err := validate.ParsePackageShareGates([]string{"github.com/prometheus/prometheus/tsdb:5"})
		require.NoError(t, err)
		require.Len(t, gates, 1)
		assert.Equal(t, "github.com/prometheus/prometheus/tsdb", gates[0].Prefix)
		assert.Equal(t, 5.0, gates[0].MinPercent)
	})
	t.Run("repeated", func(t *testing.T) {
		gates, err := validate.ParsePackageShareGates([]string{
			"github.com/prometheus/prometheus/tsdb:5",
			"github.com/prometheus/prometheus/promql:1.5",
		})
		require.NoError(t, err)
		require.Len(t, gates, 2)
		assert.Equal(t, 1.5, gates[1].MinPercent)
	})
	t.Run("comma separated", func(t *testing.T) {
		gates, err := validate.ParsePackageShareGates([]string{
			"github.com/prometheus/prometheus/tsdb:5, github.com/prometheus/prometheus/promql:1.5",
		})
		require.NoError(t, err)
		require.Len(t, gates, 2)
	})
	t.Run("decimal percent", func(t *testing.T) {
		gates, err := validate.ParsePackageShareGates([]string{"a/b:0.5"})
		require.NoError(t, err)
		assert.Equal(t, 0.5, gates[0].MinPercent)
	})
	t.Run("missing colon", func(t *testing.T) {
		_, err := validate.ParsePackageShareGates([]string{"a/b"})
		assert.Error(t, err)
	})
	t.Run("bad percent", func(t *testing.T) {
		_, err := validate.ParsePackageShareGates([]string{"a/b:xyz"})
		assert.Error(t, err)
	})
	t.Run("empty prefix", func(t *testing.T) {
		_, err := validate.ParsePackageShareGates([]string{":5"})
		assert.Error(t, err)
	})
	t.Run("negative percent", func(t *testing.T) {
		_, err := validate.ParsePackageShareGates([]string{"a/b:-1"})
		assert.Error(t, err)
	})
	t.Run("empty input", func(t *testing.T) {
		gates, err := validate.ParsePackageShareGates(nil)
		require.NoError(t, err)
		assert.Len(t, gates, 0)
	})
}

func TestComputePackageShares(t *testing.T) {
	p := syntheticCPUProfile(t, map[string]int64{
		"github.com/prometheus/prometheus/tsdb.(*Head).Append":      600,
		"github.com/prometheus/prometheus/tsdb/wlog.(*Watcher).run": 200,
		"github.com/prometheus/prometheus/promql.(*Engine).exec":    100,
		"runtime.main": 100,
	})
	shares, err := validate.ComputePackageShares(p)
	require.NoError(t, err)
	assert.Equal(t, 60.0, shares["github.com/prometheus/prometheus/tsdb"])
	assert.Equal(t, 20.0, shares["github.com/prometheus/prometheus/tsdb/wlog"])
	assert.Equal(t, 10.0, shares["github.com/prometheus/prometheus/promql"])
	assert.Equal(t, 10.0, shares["runtime"])
	// total must not exceed 100
	var total float64
	for _, s := range shares {
		total += s
	}
	assert.Equal(t, 100.0, total)
}

func TestComputePackageShares_NoCPUSampleType(t *testing.T) {
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "alloc_space", Unit: "bytes"}},
		Sample:     []*profile.Sample{{Value: []int64{1}}},
	}
	_, err := validate.ComputePackageShares(p)
	assert.Error(t, err)
}

func TestGatePackageShare_SubpackagesIncluded(t *testing.T) {
	shares := map[string]float64{
		"github.com/prometheus/prometheus/tsdb":          40.0,
		"github.com/prometheus/prometheus/tsdb/wlog":     7.0,
		"github.com/prometheus/prometheus/tsdb/chunkenc": 2.0,
		"github.com/prometheus/prometheus/promql":        15.0,
		"github.com/prometheus/prometheus/model/labels":  1.0,
		"runtime": 35.0,
	}
	assert.Equal(t, 49.0, validate.GatePackageShare(shares, "github.com/prometheus/prometheus/tsdb"))
	assert.Equal(t, 15.0, validate.GatePackageShare(shares, "github.com/prometheus/prometheus/promql"))
	assert.Equal(t, 0.0, validate.GatePackageShare(shares, "github.com/prometheus/prometheus/notpresent"))
}
