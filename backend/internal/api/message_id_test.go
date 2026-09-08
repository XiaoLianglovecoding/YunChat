package api

import (
	"context"
	"sync/atomic"
)

type apiTestMessageIDs struct{ next atomic.Int64 }

func (g *apiTestMessageIDs) Next(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return g.next.Add(1), nil
}
