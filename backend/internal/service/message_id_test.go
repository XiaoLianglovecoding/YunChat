package service

import (
	"context"
	"sync/atomic"
)

// serviceTestMessageIDs keeps unrelated Docker integration tests isolated from
// the production "message" allocator row. MySQLStore itself has dedicated
// two-pool integration coverage in internal/messageid.
type serviceTestMessageIDs struct{ next atomic.Int64 }

func (g *serviceTestMessageIDs) Next(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return g.next.Add(1), nil
}
