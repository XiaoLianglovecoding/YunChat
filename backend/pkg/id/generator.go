package id

import (
	"errors"
	"sync"
	"time"
)

const (
	workerBits   = 10
	sequenceBits = 12
	maxWorkerID  = (1 << workerBits) - 1
	maxSequence  = (1 << sequenceBits) - 1
)

var epoch = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC).UnixMilli()

var ErrClockMovedBackwards = errors.New("system clock moved backwards")

// Generator creates sortable 64-bit identifiers using timestamp, worker, and sequence bits.
type Generator struct {
	mu        sync.Mutex
	workerID  uint16
	lastMilli int64
	sequence  uint16
	now       func() time.Time
}

func New(workerID uint16) (*Generator, error) {
	if workerID > maxWorkerID {
		return nil, errors.New("worker id exceeds 10-bit range")
	}
	return &Generator{workerID: workerID, now: time.Now}, nil
}

func (g *Generator) Next() (uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	nowMilli := g.now().UTC().UnixMilli()
	if nowMilli < epoch || nowMilli < g.lastMilli {
		return 0, ErrClockMovedBackwards
	}

	if nowMilli == g.lastMilli {
		g.sequence = (g.sequence + 1) & maxSequence
		if g.sequence == 0 {
			for nowMilli <= g.lastMilli {
				time.Sleep(100 * time.Microsecond)
				nowMilli = g.now().UTC().UnixMilli()
			}
		}
	} else {
		g.sequence = 0
	}

	g.lastMilli = nowMilli
	timestamp := uint64(nowMilli - epoch)
	return (timestamp << (workerBits + sequenceBits)) |
		(uint64(g.workerID) << sequenceBits) |
		uint64(g.sequence), nil
}

type Parts struct {
	Time     time.Time
	WorkerID uint16
	Sequence uint16
}

func Parse(value uint64) Parts {
	milliseconds := int64(value>>(workerBits+sequenceBits)) + epoch
	return Parts{
		Time:     time.UnixMilli(milliseconds).UTC(),
		WorkerID: uint16((value >> sequenceBits) & maxWorkerID),
		Sequence: uint16(value & maxSequence),
	}
}
