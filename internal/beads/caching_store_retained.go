package beads

import (
	"context"
	"fmt"
)

// CountRetained forwards to the backing store's RetainedRowCounter. The cache
// holds only active beads, so the retained total (closed history included) is
// never answerable from memory. A backing store without the capability reports
// ErrCountUnsupported so callers fall back to List.
func (c *CachingStore) CountRetained(ctx context.Context) (int, error) {
	counter, ok := c.backing.(RetainedRowCounter)
	if !ok {
		return 0, fmt.Errorf("counting retained bead rows: backing store: %w", ErrCountUnsupported)
	}
	return counter.CountRetained(ctx)
}
