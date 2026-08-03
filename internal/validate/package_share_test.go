package validate

import (
	"os"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test-only helpers (syntheticCPUProfile, writeTempProfile, permissiveOptions)
// live in package_share_test_utils_test.go.

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
		assert.Equal(t, c.want, packageFromFunction(c.name), "function %q", c.name)
	}
}

func TestParsePackageShareGates(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		gates, err := ParsePackageShareGates([]string{"github.com/prometheus/prometheus/tsdb:5"})
		require.NoError(t, err)
		require.Len(t, gates, 1)
		assert.Equal(t, "github.com/prometheus/prometheus/tsdb", gates[0].Prefix)
		assert.Equal(t, 5.0, gates[0].MinPercent)
	})
	t.Run("repeated", func(t *testing.T) {
		gates, err := ParsePackageShareGates([]string{
			"github.com/prometheus/prometheus/tsdb:5",
			"github.com/prometheus/prometheus/promql:1.5",
		})
		require.NoError(t, err)
		require.Len(t, gates, 2)
		assert.Equal(t, 1.5, gates[1].MinPercent)
	})
	t.Run("comma separated", func(t *testing.T) {
		gates, err := ParsePackageShareGates([]string{
			"github.com/prometheus/prometheus/tsdb:5, github.com/prometheus/prometheus/promql:1.5",
		})
		require.NoError(t, err)
		require.Len(t, gates, 2)
	})
	t.Run("decimal percent", func(t *testing.T) {
		gates, err := ParsePackageShareGates([]string{"a/b:0.5"})
		require.NoError(t, err)
		assert.Equal(t, 0.5, gates[0].MinPercent)
	})
	t.Run("missing colon", func(t *testing.T) {
		_, err := ParsePackageShareGates([]string{"a/b"})
		assert.Error(t, err)
	})
	t.Run("bad percent", func(t *testing.T) {
		_, err := ParsePackageShareGates([]string{"a/b:xyz"})
		assert.Error(t, err)
	})
	t.Run("empty prefix", func(t *testing.T) {
		_, err := ParsePackageShareGates([]string{":5"})
		assert.Error(t, err)
	})
	t.Run("negative percent", func(t *testing.T) {
		_, err := ParsePackageShareGates([]string{"a/b:-1"})
		assert.Error(t, err)
	})
	t.Run("empty input", func(t *testing.T) {
		gates, err := ParsePackageShareGates(nil)
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
	shares, err := computePackageShares(p)
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
	_, err := computePackageShares(p)
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
	assert.Equal(t, 49.0, gatePackageShare(shares, "github.com/prometheus/prometheus/tsdb"))
	assert.Equal(t, 15.0, gatePackageShare(shares, "github.com/prometheus/prometheus/promql"))
	assert.Equal(t, 0.0, gatePackageShare(shares, "github.com/prometheus/prometheus/notpresent"))
}

// ---- end-to-end ValidateFile + PackageShareGates (negative + positive) ----

func TestValidateFile_PackageShareGate_Fails(t *testing.T) {
	// tsdb combined share is 30% of 1000 = 3.0% < 5.0% gate -> invalid.
	p := syntheticCPUProfile(t, map[string]int64{
		"github.com/prometheus/prometheus/tsdb.(*Head).Append": 30,
		"runtime.main": 970,
	})
	path := writeTempProfile(t, p)
	opts := permissiveOptions()
	opts.PackageShareGates = []PackageShareGate{
		{Prefix: "github.com/prometheus/prometheus/tsdb", MinPercent: 5.0},
	}

	report, err := ValidateFile(path, opts)
	require.NoError(t, err)
	assert.False(t, report.Valid, "gate below threshold must invalidate")
	assert.Contains(t, report.Errors, "package share too low: github.com/prometheus/prometheus/tsdb = 3.00% < 5.00%")
	assert.Equal(t, 3.0, report.PackageShares["github.com/prometheus/prometheus/tsdb"])
}

func TestValidateFile_PackageShareGate_Passes(t *testing.T) {
	// tsdb combined share 60% >= 5% -> valid unaffected.
	p := syntheticCPUProfile(t, map[string]int64{
		"github.com/prometheus/prometheus/tsdb.(*Head).Append": 600,
		"runtime.main": 400,
	})
	path := writeTempProfile(t, p)
	opts := permissiveOptions()
	opts.PackageShareGates = []PackageShareGate{
		{Prefix: "github.com/prometheus/prometheus/tsdb", MinPercent: 5.0},
	}

	report, err := ValidateFile(path, opts)
	require.NoError(t, err)
	assert.True(t, report.Valid)
	assert.Empty(t, report.Errors)
	assert.Equal(t, 60.0, report.PackageShares["github.com/prometheus/prometheus/tsdb"])
}

func TestValidateFile_PackageShareGate_SubpackageAggregation(t *testing.T) {
	// 30% tsdb + 20% tsdb/wlog -> combined tsdb-family 50% via ValidateFile.
	p := syntheticCPUProfile(t, map[string]int64{
		"github.com/prometheus/prometheus/tsdb.(*Head).Append":      300,
		"github.com/prometheus/prometheus/tsdb/wlog.(*Watcher).run": 200,
		"runtime.main": 500,
	})
	path := writeTempProfile(t, p)
	opts := permissiveOptions()
	opts.PackageShareGates = []PackageShareGate{
		{Prefix: "github.com/prometheus/prometheus/tsdb", MinPercent: 40.0},
	}

	report, err := ValidateFile(path, opts)
	require.NoError(t, err)
	assert.True(t, report.Valid)
	assert.Equal(t, 50.0, report.PackageShares["github.com/prometheus/prometheus/tsdb"])
}

func TestValidateFile_PackageShareGate_NoCPUSampleType(t *testing.T) {
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "alloc_space", Unit: "bytes"}},
		Sample:     []*profile.Sample{{Value: []int64{1}}},
	}
	path := writeTempProfile(t, p)
	opts := permissiveOptions()
	opts.PackageShareGates = []PackageShareGate{
		{Prefix: "github.com/prometheus/prometheus/tsdb", MinPercent: 5.0},
	}

	report, err := ValidateFile(path, opts)
	require.NoError(t, err)
	assert.False(t, report.Valid, "no cpu sample type must invalidate")
	assert.Contains(t, report.Errors, "no cpu sample type")
}

func TestValidateFile_PackageShareGate_MalformedFile(t *testing.T) {
	f, err := os.CreateTemp("", "bad*.pprof")
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(f.Name()) })
	_, err = f.WriteString("not a pprof file")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	opts := permissiveOptions()
	opts.PackageShareGates = []PackageShareGate{
		{Prefix: "github.com/prometheus/prometheus/tsdb", MinPercent: 5.0},
	}

	report, err := ValidateFile(f.Name(), opts)
	assert.Error(t, err, "parse failure must surface as an error, unchanged by gates")
	assert.Nil(t, report)
}

func TestValidateFile_PackageShareGate_NoGatesNoChange(t *testing.T) {
	// Without gates, a thin profile is still valid under permissive opts.
	p := syntheticCPUProfile(t, map[string]int64{
		"github.com/prometheus/prometheus/tsdb.(*Head).Append": 30,
		"runtime.main": 970,
	})
	path := writeTempProfile(t, p)

	report, err := ValidateFile(path, permissiveOptions())
	require.NoError(t, err)
	assert.True(t, report.Valid)
	assert.Nil(t, report.PackageShares, "no gates -> no package_shares in report")
}
