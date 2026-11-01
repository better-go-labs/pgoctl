package store_test

import (
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKey_Tag(t *testing.T) {
	d, err := store.ParseDate("2026-10-26")
	require.NoError(t, err)

	tests := []struct {
		name string
		key  store.Key
		seq  int
		want string
	}{
		{
			name: "plain key omits the service segment",
			key:  store.Key{Repo: "o/r", Prefix: "profile"},
			seq:  0,
			want: "profile/2026-10-26.0",
		},
		{
			name: "service key inserts the service segment",
			key:  store.Key{Repo: "o/r", Prefix: "profile", Service: "api"},
			seq:  2,
			want: "profile/api/2026-10-26.2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.key.Tag(d, tt.seq))
		})
	}
}

// TestKey_ParseTag_StaysInsideKey verifies a key never parses a tag that belongs
// to a different prefix or service.
func TestKey_ParseTag_StaysInsideKey(t *testing.T) {
	plain := store.Key{Repo: "o/r", Prefix: "profile"}
	svc := store.Key{Repo: "o/r", Prefix: "profile", Service: "api"}

	t.Run("plain key parses its own tag", func(t *testing.T) {
		d, seq, ok := plain.ParseTag("profile/2026-10-26.3")
		require.True(t, ok)
		assert.Equal(t, "2026-10-26", d.String())
		assert.Equal(t, 3, seq)
	})

	t.Run("plain key rejects a service tag", func(t *testing.T) {
		_, _, ok := plain.ParseTag("profile/api/2026-10-26.0")
		assert.False(t, ok)
	})

	t.Run("service key parses its own tag", func(t *testing.T) {
		d, seq, ok := svc.ParseTag("profile/api/2026-10-26.1")
		require.True(t, ok)
		assert.Equal(t, "2026-10-26", d.String())
		assert.Equal(t, 1, seq)
	})

	t.Run("service key rejects a plain tag", func(t *testing.T) {
		_, _, ok := svc.ParseTag("profile/2026-10-26.0")
		assert.False(t, ok)
	})

	t.Run("rejects a different prefix", func(t *testing.T) {
		_, _, ok := plain.ParseTag("other/2026-10-26.0")
		assert.False(t, ok)
	})

	t.Run("rejects a malformed sequence", func(t *testing.T) {
		_, _, ok := plain.ParseTag("profile/2026-10-26.x")
		assert.False(t, ok)
	})
}

func TestKey_Validate(t *testing.T) {
	tests := []struct {
		name    string
		key     store.Key
		wantErr bool
	}{
		{name: "valid plain key", key: store.Key{Repo: "o/r", Prefix: "profile"}},
		{name: "valid service key", key: store.Key{Repo: "o/r", Prefix: "profile", Service: "api"}},
		{name: "repo without slash", key: store.Key{Repo: "repo", Prefix: "profile"}, wantErr: true},
		{name: "empty prefix", key: store.Key{Repo: "o/r", Prefix: ""}, wantErr: true},
		{name: "service with slash", key: store.Key{Repo: "o/r", Prefix: "profile", Service: "a/b"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.key.Validate()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestRunAsset_RoundTrip(t *testing.T) {
	name := store.RunAsset("11873365122-1")
	assert.Equal(t, "runs/11873365122-1.pprof", name)

	id, ok := store.RunIDFromAsset(name)
	require.True(t, ok)
	assert.Equal(t, "11873365122-1", id)

	_, ok = store.RunIDFromAsset("daily.pprof")
	assert.False(t, ok)

	assert.True(t, store.ValidRunID("11873365122-1"))
	assert.False(t, store.ValidRunID("not-a-run"))
}
