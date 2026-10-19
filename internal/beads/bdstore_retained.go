package beads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// CountRetained implements RetainedRowCounter for the bd CLI. It returns the
// cardinality of List(ListQuery{AllowScan: true, IncludeClosed: true}), which
// runs `bd list --all --include-infra --include-gates`. bd count has no
// --include-gates flag, so the same total is built from two hydration-free
// counts: `bd count --include-infra` covers every row except gate beads, and
// `bd count --type gate --include-infra` covers the gate beads. The two reads
// are separate bd invocations, not one snapshot, so a row written between them
// can shift the total by one.
func (s *BdStore) CountRetained(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("counting retained bead rows: %w", err)
	}
	nonGate, err := s.bdCountJSON(ctx, "--include-infra")
	if err != nil {
		return 0, fmt.Errorf("counting retained bead rows: %w", err)
	}
	gates, err := s.bdCountJSON(ctx, "--type", "gate", "--include-infra")
	if err != nil {
		return 0, fmt.Errorf("counting retained bead rows: %w", err)
	}
	return nonGate + gates, nil
}

// bdCountJSON runs one `bd count` with the given filter flags and returns its
// total. A reply without a count field is an error, never a silent zero.
func (s *BdStore) bdCountJSON(ctx context.Context, filters ...string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	args := append([]string{"count"}, filters...)
	args = append(args, "--json")
	out, err := s.runBDTransientRead(args...)
	if err != nil {
		return 0, err
	}
	var result struct {
		Count *int `json:"count"`
	}
	if err := json.Unmarshal(extractJSON(out), &result); err != nil {
		return 0, fmt.Errorf("bd count: parsing result: %w", err)
	}
	if result.Count == nil {
		return 0, errors.New("bd count: result has no count field")
	}
	return *result.Count, nil
}
