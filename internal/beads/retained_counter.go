package beads

import "context"

// RetainedRowCounter is an optional Store capability: counting the rows a
// store retains (open and closed, including infrastructure and gate beads)
// without hydrating them. It answers the same cardinality as
// List(ListQuery{AllowScan: true, IncludeClosed: true}) and exists separately
// from Counter so that existing Counter callers, which assume their query
// shapes are answerable, keep their behavior.
type RetainedRowCounter interface {
	CountRetained(ctx context.Context) (int, error)
}
