package beads_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

type retainedBackingStore struct {
	beads.Store
	count int
	runs  int
}

func (s *retainedBackingStore) CountRetained(context.Context) (int, error) {
	s.runs++
	return s.count, nil
}

func TestCachingStoreCountRetainedForwardsToBackingCounter(t *testing.T) {
	backing := &retainedBackingStore{Store: beads.NewMemStore(), count: 9}
	cs := beads.NewCachingStore(backing, nil)

	got, err := cs.CountRetained(context.Background())
	if err != nil {
		t.Fatalf("CountRetained: %v", err)
	}
	if got != 9 {
		t.Fatalf("CountRetained = %d, want 9 from the backing counter", got)
	}
	if backing.runs != 1 {
		t.Fatalf("backing CountRetained ran %d times, want 1", backing.runs)
	}
}

func TestCachingStoreCountRetainedUnsupportedWithoutBackingCapability(t *testing.T) {
	cs := beads.NewCachingStore(beads.NewMemStore(), nil)

	if _, err := cs.CountRetained(context.Background()); !errors.Is(err, beads.ErrCountUnsupported) {
		t.Fatalf("CountRetained error = %v, want ErrCountUnsupported so callers fall back to List", err)
	}
}
