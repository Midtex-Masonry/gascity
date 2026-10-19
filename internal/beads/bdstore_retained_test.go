package beads_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

func TestBdStoreCountRetainedMatchesListWithInfraAndGates(t *testing.T) {
	runner := fakeRunner(map[string]struct {
		out []byte
		err error
	}{
		`bd count --include-infra --json`: {
			out: []byte(`{"count":5,"schema_version":1}`),
		},
		`bd count --type gate --include-infra --json`: {
			out: []byte(`{"count":1,"schema_version":1}`),
		},
	})
	s := beads.NewBdStore("/city", runner)

	got, err := s.CountRetained(context.Background())
	if err != nil {
		t.Fatalf("CountRetained: %v", err)
	}
	if got != 6 {
		t.Fatalf("CountRetained = %d, want 6 (5 non-gate infra rows plus 1 gate)", got)
	}
}

func TestBdStoreCountRetainedReportsCountFailure(t *testing.T) {
	wantErr := errors.New("bd count unavailable")
	runner := fakeRunner(map[string]struct {
		out []byte
		err error
	}{
		`bd count --include-infra --json`: {err: wantErr},
	})
	s := beads.NewBdStore("/city", runner)

	got, err := s.CountRetained(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("CountRetained error = %v, want %v", err, wantErr)
	}
	if got != 0 {
		t.Fatalf("CountRetained = %d on failure, want 0", got)
	}
}

func TestBdStoreCountRetainedRejectsMalformedCountOutput(t *testing.T) {
	runner := fakeRunner(map[string]struct {
		out []byte
		err error
	}{
		`bd count --include-infra --json`: {out: []byte(`not json`)},
	})
	s := beads.NewBdStore("/city", runner)

	if _, err := s.CountRetained(context.Background()); err == nil {
		t.Fatal("CountRetained with malformed bd output returned nil error, want parse error instead of a silent zero")
	}
}

func TestBdStoreCountRetainedStopsBeforeRunningBdWhenCanceled(t *testing.T) {
	var calls int
	runner := func(_, _ string, _ ...string) ([]byte, error) {
		calls++
		return nil, errors.New("bd should not run")
	}
	s := beads.NewBdStore("/city", runner)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.CountRetained(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CountRetained error = %v, want context.Canceled", err)
	}
	if calls != 0 {
		t.Fatalf("bd ran %d times after cancellation, want 0", calls)
	}
}
