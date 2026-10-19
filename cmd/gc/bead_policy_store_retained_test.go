package main

import (
	"context"
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

type retainedInnerStore struct {
	beads.Store
	count int
	runs  int
}

func (s *retainedInnerStore) CountRetained(context.Context) (int, error) {
	s.runs++
	return s.count, nil
}

func TestBeadPolicyStoreCountRetainedForwardsToInnerCounter(t *testing.T) {
	inner := &retainedInnerStore{Store: beads.NewMemStore(), count: 4}
	policy := &beadPolicyStore{Store: inner}

	got, err := policy.CountRetained(context.Background())
	if err != nil {
		t.Fatalf("CountRetained: %v", err)
	}
	if got != 4 {
		t.Fatalf("CountRetained = %d, want 4 from the inner counter", got)
	}
	if inner.runs != 1 {
		t.Fatalf("inner CountRetained ran %d times, want 1", inner.runs)
	}
}

func TestBeadPolicyStoreCountRetainedUnsupportedWithoutInnerCapability(t *testing.T) {
	policy := &beadPolicyStore{Store: beads.NewMemStore()}

	if _, err := policy.CountRetained(context.Background()); !errors.Is(err, beads.ErrCountUnsupported) {
		t.Fatalf("CountRetained error = %v, want ErrCountUnsupported", err)
	}
}
