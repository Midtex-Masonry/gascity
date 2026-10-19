package main

import (
	"context"
	"fmt"

	"github.com/gastownhall/gascity/internal/beads"
)

// CountRetained forwards to the inner store's RetainedRowCounter. Policy List
// reads widen the issues tier to the union with the wisps tier, but the bd
// infra listing the inner count mirrors already includes ephemeral rows, so
// the inner total is the same cardinality the policy List returns.
func (s *beadPolicyStore) CountRetained(ctx context.Context) (int, error) {
	counter, ok := s.Store.(beads.RetainedRowCounter)
	if !ok {
		return 0, fmt.Errorf("counting retained bead rows: inner store: %w", beads.ErrCountUnsupported)
	}
	return counter.CountRetained(ctx)
}
