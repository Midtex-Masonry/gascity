package api

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

// retainedCountStore answers the retained-row count without listing. Any List
// call is a regression to the hydrating scan and fails the test through
// listCalls.
type retainedCountStore struct {
	beads.Store
	count     int
	countErr  error
	countRuns int
	listCalls int
}

func (s *retainedCountStore) CountRetained(context.Context) (int, error) {
	s.countRuns++
	return s.count, s.countErr
}

// unsupportedRetainedCountStore reports that the retained count cannot be
// answered, so countBeadStoreRows must fall back to List on the wrapped store.
type unsupportedRetainedCountStore struct {
	beads.Store
}

func (unsupportedRetainedCountStore) CountRetained(context.Context) (int, error) {
	return 0, beads.ErrCountUnsupported
}

func (s *retainedCountStore) List(beads.ListQuery) ([]beads.Bead, error) {
	s.listCalls++
	return nil, errors.New("retained count must not hydrate rows through List")
}

// @critical: store health must count retained rows through the cheap path, not a full hydrating List.
func TestCountBeadStoreRowsUsesRetainedCounterWithoutListing(t *testing.T) {
	store := &retainedCountStore{Store: beads.NewMemStore(), count: 7}

	got, err := countBeadStoreRows(context.Background(), newFakeState(t), store)
	if err != nil {
		t.Fatalf("countBeadStoreRows: %v", err)
	}
	if got != 7 {
		t.Fatalf("countBeadStoreRows = %d, want 7 from the retained counter", got)
	}
	if store.countRuns != 1 {
		t.Fatalf("CountRetained ran %d times, want 1", store.countRuns)
	}
	if store.listCalls != 0 {
		t.Fatalf("List ran %d times, want 0 when the retained counter answers", store.listCalls)
	}
}

// @critical: an unsupported retained count must fall back to List rather than report a wrong total.
func TestCountBeadStoreRowsFallsBackToListWhenRetainedCountUnsupported(t *testing.T) {
	mem := beads.NewMemStore()
	for _, title := range []string{"open", "closed"} {
		if _, err := mem.Create(beads.Bead{Title: title}); err != nil {
			t.Fatalf("Create %s: %v", title, err)
		}
	}
	store := unsupportedRetainedCountStore{Store: mem}

	got, err := countBeadStoreRows(context.Background(), newFakeState(t), store)
	if err != nil {
		t.Fatalf("countBeadStoreRows: %v", err)
	}
	if got != 2 {
		t.Fatalf("countBeadStoreRows = %d, want 2 from the List fallback", got)
	}
}

// @critical: a failed store-health refresh must be negatively cached so /status does not rescan on every poll.
func TestCachedStoreHealthCachesFailureUntilBackoffExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wantErr := errors.New("store health row scan failed")
		var calls int
		s := &Server{}
		s.storeHealthComputer = func(context.Context) (*StatusStoreHealth, error) {
			calls++
			return nil, wantErr
		}

		for i := range 2 {
			if _, err := s.cachedStoreHealth(context.Background(), time.Now()); !errors.Is(err, wantErr) {
				t.Fatalf("call %d error = %v, want %v", i+1, err, wantErr)
			}
		}
		if calls != 1 {
			t.Fatalf("computer called %d times within backoff, want 1", calls)
		}

		time.Sleep(storeHealthFailureBackoff)
		if _, err := s.cachedStoreHealth(context.Background(), time.Now()); !errors.Is(err, wantErr) {
			t.Fatalf("post-backoff error = %v, want %v", err, wantErr)
		}
		if calls != 2 {
			t.Fatalf("computer called %d times after backoff expired, want 2", calls)
		}
	})
}

// @critical: a slow store-health refresh must not hold /status past its wait budget; the last known value is served and marked stale.
func TestCachedStoreHealthReturnsLastKnownValueWhenRefreshExceedsWaitBudget(t *testing.T) {
	prevBudget := storeHealthWaitBudget
	storeHealthWaitBudget = 20 * time.Millisecond
	t.Cleanup(func() { storeHealthWaitBudget = prevBudget })

	release := make(chan struct{})
	last := &StatusStoreHealth{Path: "/old"}
	fresh := &StatusStoreHealth{Path: "/new"}
	s := &Server{}
	s.storeHealthEntry = last
	s.storeHealthExpires = time.Now().Add(-time.Second)
	s.storeHealthComputer = func(context.Context) (*StatusStoreHealth, error) {
		<-release
		return fresh, nil
	}

	got, err := s.cachedStoreHealth(context.Background(), time.Now())
	if err == nil {
		t.Fatal("cachedStoreHealth error = nil, want stale marker while refresh is still running")
	}
	if got != last {
		t.Fatalf("cachedStoreHealth = %+v, want last known value %+v", got, last)
	}

	close(release)
	storeHealthWaitBudget = 5 * time.Second
	got, err = s.cachedStoreHealth(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("cachedStoreHealth after refresh completed: %v", err)
	}
	if got != fresh {
		t.Fatalf("cachedStoreHealth after refresh = %+v, want fresh value %+v", got, fresh)
	}
}
