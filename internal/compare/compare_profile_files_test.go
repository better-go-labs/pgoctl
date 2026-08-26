package compare_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Better-Go-Labs/pgoctl/internal/compare"
)

func TestProfileFiles_Success(t *testing.T) {
	base := buildTwoFnProfile(t, "main.hotFn", "main.otherFn", 1000, 0)
	cand := buildTwoFnProfile(t, "main.hotFn", "main.otherFn", 700, 300)

	tmpDir := t.TempDir()
	baseFile := filepath.Join(tmpDir, "base.pprof")
	candFile := filepath.Join(tmpDir, "cand.pprof")

	require.NoError(t, os.WriteFile(baseFile, base, 0o644))
	require.NoError(t, os.WriteFile(candFile, cand, 0o644))

	rpt, err := compare.ProfileFiles(baseFile, candFile, compare.DefaultGateConfig())
	require.NoError(t, err)
	require.NotNil(t, rpt)
	require.Equal(t, compare.Promote, rpt.Verdict)
}

func TestProfileFiles_BaseNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	candFile := filepath.Join(tmpDir, "cand.pprof")
	cand := buildTwoFnProfile(t, "main.fn", "main.other", 500, 500)
	require.NoError(t, os.WriteFile(candFile, cand, 0o644))

	_, err := compare.ProfileFiles(filepath.Join(tmpDir, "nonexistent.pprof"), candFile, compare.DefaultGateConfig())
	require.Error(t, err)
}

func TestProfileFiles_CandidateNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	baseFile := filepath.Join(tmpDir, "base.pprof")
	base := buildTwoFnProfile(t, "main.fn", "main.other", 500, 500)
	require.NoError(t, os.WriteFile(baseFile, base, 0o644))

	_, err := compare.ProfileFiles(baseFile, filepath.Join(tmpDir, "nonexistent.pprof"), compare.DefaultGateConfig())
	require.Error(t, err)
}

func TestProfileFiles_MalformedBase(t *testing.T) {
	tmpDir := t.TempDir()
	baseFile := filepath.Join(tmpDir, "base.pprof")
	candFile := filepath.Join(tmpDir, "cand.pprof")
	cand := buildTwoFnProfile(t, "main.fn", "main.other", 500, 500)

	require.NoError(t, os.WriteFile(baseFile, []byte("invalid pprof data"), 0o644))
	require.NoError(t, os.WriteFile(candFile, cand, 0o644))

	_, err := compare.ProfileFiles(baseFile, candFile, compare.DefaultGateConfig())
	require.Error(t, err)
}

func TestProfileFiles_MalformedCandidate(t *testing.T) {
	tmpDir := t.TempDir()
	baseFile := filepath.Join(tmpDir, "base.pprof")
	candFile := filepath.Join(tmpDir, "cand.pprof")
	base := buildTwoFnProfile(t, "main.fn", "main.other", 500, 500)

	require.NoError(t, os.WriteFile(baseFile, base, 0o644))
	require.NoError(t, os.WriteFile(candFile, []byte("invalid pprof data"), 0o644))

	_, err := compare.ProfileFiles(baseFile, candFile, compare.DefaultGateConfig())
	require.Error(t, err)
}
