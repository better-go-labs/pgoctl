package storetest_test

import (
	"testing"

	"github.com/Better-Go-Labs/pgoctl/internal/store/storetest"
)

// TestMemoryStore runs the full Store contract suite against the in-memory
// implementation. Any future backend must pass the same suite.
func TestMemoryStore(t *testing.T) {
	storetest.RunContracts(t, storetest.NewMemoryStore())
}
