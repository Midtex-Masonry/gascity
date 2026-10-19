package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/storehealth"
)

// storeHealthCacheTTL is the refresh interval for the /v0/status
// StoreHealth block. Its inputs include a full closed-history row count
// whose cost grows with store history, so keep the interval above the worst
// observed refresh.
const storeHealthCacheTTL = 3 * time.Minute

// storeHealthFailureBackoff is how long a failed refresh is served from the
// negative cache before another refresh may run. A failing store is retried
// at most once per backoff, not once per /status poll.
const storeHealthFailureBackoff = time.Minute

// storeHealthRefreshTimeout bounds one background refresh. A refresh runs
// detached from every request so that a poll ending cannot cancel its backend
// read mid-query; this deadline still stops a hung refresh from holding the
// singleflight slot forever.
const storeHealthRefreshTimeout = 2 * time.Minute

// storeHealthWaitBudget bounds how long one /status request waits for a
// refresh before it serves the last known value. It shares the status read
// budget. Var, not const, so tests can shorten it.
var storeHealthWaitBudget = statusStoreReadTimeout

// cachedStoreHealth returns the memoized StoreHealth block. A fresh entry is
// returned immediately. Otherwise one shared refresh runs detached from the
// request and the request waits at most storeHealthWaitBudget for it. When the
// refresh is still running or has failed, the last known value (nil if none)
// is returned with an error, so store health never holds /status past its
// budget. A failed refresh is remembered for storeHealthFailureBackoff and
// served from that negative cache without rerunning. Safe for concurrent
// callers.
func (s *Server) cachedStoreHealth(ctx context.Context, now time.Time) (*StatusStoreHealth, error) {
	s.storeHealthMu.Lock()
	last := s.storeHealthEntry
	fresh := last != nil && now.Before(s.storeHealthExpires)
	failure := s.storeHealthErr
	backingOff := failure != nil && now.Before(s.storeHealthRetryAt)
	s.storeHealthMu.Unlock()
	if fresh {
		return last, nil
	}
	if backingOff {
		return last, failure
	}

	ch := s.storeHealthFlight.DoChan("refresh", func() (any, error) {
		return s.refreshStoreHealth()
	})
	wait := time.NewTimer(storeHealthWaitBudget)
	defer wait.Stop()
	select {
	case res := <-ch:
		if res.Err != nil {
			return s.lastStoreHealth(), res.Err
		}
		value, _ := res.Val.(*StatusStoreHealth)
		return value, nil
	case <-wait.C:
		return s.lastStoreHealth(), fmt.Errorf("refresh still running after %s; serving last known value", storeHealthWaitBudget)
	case <-ctx.Done():
		return s.lastStoreHealth(), ctx.Err()
	}
}

// refreshStoreHealth runs one detached refresh and records its outcome. A
// success replaces the memoized entry for storeHealthCacheTTL; a failure is
// remembered for storeHealthFailureBackoff. Callers coalesce through
// storeHealthFlight, so at most one refresh runs at a time.
func (s *Server) refreshStoreHealth() (any, error) {
	// Another refresh may have completed between this caller's miss and its
	// election into the singleflight group.
	if entry := s.cachedStoreHealthEntry(time.Now()); entry != nil {
		return entry, nil
	}

	s.storeHealthMu.Lock()
	compute := s.storeHealthComputer
	if compute == nil {
		compute = s.computeStoreHealth
	}
	s.storeHealthMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), storeHealthRefreshTimeout)
	defer cancel()
	health, err := compute(ctx)
	completedAt := time.Now()

	s.storeHealthMu.Lock()
	defer s.storeHealthMu.Unlock()
	if err != nil {
		s.storeHealthErr = err
		s.storeHealthRetryAt = completedAt.Add(storeHealthFailureBackoff)
		return nil, err
	}
	s.storeHealthEntry = health
	s.storeHealthExpires = completedAt.Add(storeHealthCacheTTL)
	s.storeHealthErr = nil
	s.storeHealthRetryAt = time.Time{}
	return health, nil
}

// lastStoreHealth returns the most recent successful StoreHealth value, which
// may be expired. Callers that serve it must report it as stale.
func (s *Server) lastStoreHealth() *StatusStoreHealth {
	s.storeHealthMu.Lock()
	defer s.storeHealthMu.Unlock()
	return s.storeHealthEntry
}

func (s *Server) cachedStoreHealthEntry(now time.Time) *StatusStoreHealth {
	s.storeHealthMu.Lock()
	defer s.storeHealthMu.Unlock()
	if s.storeHealthEntry != nil && now.Before(s.storeHealthExpires) {
		return s.storeHealthEntry
	}
	return nil
}

// computeStoreHealth measures the Dolt store on disk and the latest
// gc.store.maintenance event via the server's State. Returns nil when
// the city path is empty (no state to measure against).
func (s *Server) computeStoreHealth(ctx context.Context) (*StatusStoreHealth, error) {
	cityPath := s.state.CityPath()
	if cityPath == "" {
		return nil, nil
	}
	// WalkSize is a synchronous, uncancellable disk walk; the
	// storeHealthCacheTTL cache bounds how often it runs. Plumbing
	// context/timeout through WalkSize is deferred until it shows up
	// in profiles.
	size := storehealth.WalkSize(storehealth.StorePath(cityPath))
	rows, err := countBeadStoreRows(ctx, s.state, s.state.CityBeadStore())
	if err != nil {
		return nil, err
	}
	lastAt, lastStatus := storehealth.LastMaintenance(s.state.EventProvider())
	h := storehealth.Compute(cityPath, size, rows, lastAt, lastStatus)
	return statusStoreHealthFromDomain(h), nil
}

// statusStoreHealthFromDomain adapts storehealth.Health to the wire
// type StatusStoreHealth, serializing LastGCAt to RFC3339 UTC.
func statusStoreHealthFromDomain(h storehealth.Health) *StatusStoreHealth {
	out := &StatusStoreHealth{
		Path:        h.Path,
		SizeBytes:   h.SizeBytes,
		LiveRows:    h.LiveRows,
		RatioMB:     h.RatioMB,
		Warning:     h.Warning,
		ThresholdMB: h.ThresholdMB,
	}
	if !h.LastGCAt.IsZero() {
		out.LastGCAt = h.LastGCAt.UTC().Format(time.RFC3339)
		out.LastGCStatus = h.LastGCStatus
	}
	return out
}

// countBeadStoreRows returns the number of retained beads in store, including
// open and closed beads. A nil store and measurement failures are returned as
// errors so callers do not mistake an unavailable denominator for zero. A
// store (or the scoped clone of it) implementing beads.RetainedRowCounter
// answers with a hydration-free count. Any other store falls back to a
// closed-inclusive List, which hydrates every row.
func countBeadStoreRows(ctx context.Context, state State, store beads.Store) (int, error) {
	if store == nil {
		return 0, errors.New("counting retained bead rows: store unavailable")
	}
	countStore := store
	scoped, err := state.ScopedStoreLike(ctx, store)
	if err != nil {
		return 0, fmt.Errorf("counting retained bead rows: resolving scoped store: %w", err)
	}
	if scoped != nil {
		countStore = scoped
	}
	if counter, ok := countStore.(beads.RetainedRowCounter); ok {
		n, err := counter.CountRetained(ctx)
		if err == nil {
			return n, nil
		}
		if !errors.Is(err, beads.ErrCountUnsupported) {
			return 0, fmt.Errorf("counting retained bead rows: %w", err)
		}
	}
	list, err := statusListStoreWithTimeout(ctx, state, store, beads.ListQuery{AllowScan: true, IncludeClosed: true})
	if err != nil {
		return 0, fmt.Errorf("counting retained bead rows: %w", err)
	}
	return len(list), nil
}
