package messageid

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

type memoryStore struct {
	mu       sync.Mutex
	next     int64
	reserveN int
	err      error
}

func newMemoryStore(first int64) *memoryStore { return &memoryStore{next: first} }

func (s *memoryStore) Reserve(_ context.Context, size int64) (Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return Segment{}, s.err
	}
	if s.next < 1 || s.next > MaxID || size-1 > MaxID-s.next {
		return Segment{}, ErrExhausted
	}
	segment := Segment{First: s.next, Last: s.next + size - 1}
	s.next = segment.Last + 1
	s.reserveN++
	return segment, nil
}

func TestGeneratorBurstBeyondOldPerMillisecondLimit(t *testing.T) {
	store := newMemoryStore(1)
	generator, err := New(store, Options{SegmentSize: 256})
	if err != nil {
		t.Fatal(err)
	}

	const total = 10_000 // the removed scheme failed after 999 IDs in one millisecond
	for want := int64(1); want <= total; want++ {
		got, nextErr := generator.Next(context.Background())
		if nextErr != nil {
			t.Fatalf("Next(%d): %v", want, nextErr)
		}
		if got != want {
			t.Fatalf("Next() = %d, want %d", got, want)
		}
	}
	if store.reserveN != 40 {
		t.Fatalf("reservations = %d, want 40", store.reserveN)
	}
}

func TestGeneratorsAreUniqueAcrossInstancesAndGoroutines(t *testing.T) {
	const (
		instances   = 8
		perInstance = 25_000
	)
	store := newMemoryStore(1)
	generated := make(chan int64, instances*perInstance)
	errCh := make(chan error, instances)
	var workers sync.WaitGroup

	for instance := 0; instance < instances; instance++ {
		generator, err := New(store, Options{SegmentSize: 997})
		if err != nil {
			t.Fatal(err)
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < perInstance; i++ {
				id, nextErr := generator.Next(context.Background())
				if nextErr != nil {
					errCh <- nextErr
					return
				}
				generated <- id
			}
		}()
	}
	workers.Wait()
	close(generated)
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	seen := make(map[int64]struct{}, instances*perInstance)
	for id := range generated {
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate message ID %d", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != instances*perInstance {
		t.Fatalf("unique IDs = %d, want %d", len(seen), instances*perInstance)
	}
}

func TestOneGeneratorIsUniqueUnderConcurrentNext(t *testing.T) {
	const (
		goroutines   = 32
		perGoroutine = 5_000
	)
	store := newMemoryStore(1)
	generator, err := New(store, Options{SegmentSize: 1_009})
	if err != nil {
		t.Fatal(err)
	}
	generated := make(chan int64, goroutines*perGoroutine)
	errCh := make(chan error, goroutines)
	var workers sync.WaitGroup
	for range goroutines {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range perGoroutine {
				id, nextErr := generator.Next(context.Background())
				if nextErr != nil {
					errCh <- nextErr
					return
				}
				generated <- id
			}
		}()
	}
	workers.Wait()
	close(generated)
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	seen := make(map[int64]struct{}, goroutines*perGoroutine)
	for id := range generated {
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate message ID %d", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != goroutines*perGoroutine {
		t.Fatalf("unique IDs = %d, want %d", len(seen), goroutines*perGoroutine)
	}
}

func TestRestartAndRedisRecoveryCannotReuseCommittedSegment(t *testing.T) {
	store := newMemoryStore(1)
	beforeRestart, err := New(store, Options{SegmentSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := beforeRestart.Next(context.Background())
	second, _ := beforeRestart.Next(context.Background())

	// Simulate losing the process heap together with all Redis data. The new
	// generator knows only MySQL's durable high-water mark in the shared store.
	afterRestart, err := New(store, Options{SegmentSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	afterRecovery, err := afterRestart.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 2 || afterRecovery != 101 {
		t.Fatalf("IDs before/after recovery = %d, %d, %d; want 1, 2, 101", first, second, afterRecovery)
	}
}

func TestGeneratorDoesNotHideStoreFailureOrReuseOldSegment(t *testing.T) {
	store := newMemoryStore(1)
	generator, err := New(store, Options{SegmentSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = generator.Next(context.Background())
	_, _ = generator.Next(context.Background())
	store.err = errors.New("mysql unavailable")

	if id, nextErr := generator.Next(context.Background()); nextErr == nil || id != 0 {
		t.Fatalf("Next() = (%d, %v), want a fail-closed error", id, nextErr)
	}
	store.err = nil
	id, err := generator.Next(context.Background())
	if err != nil || id != 3 {
		t.Fatalf("Next() after recovery = (%d, %v), want (3, nil)", id, err)
	}
}

func TestNewValidatesOptionsAndAppliesDefault(t *testing.T) {
	if _, err := New(nil, Options{}); err == nil {
		t.Fatal("expected nil store error")
	}
	store := newMemoryStore(1)
	for _, size := range []int64{-1, MaxSegmentSize + 1} {
		if _, err := New(store, Options{SegmentSize: size}); err == nil {
			t.Fatalf("expected segment size %d error", size)
		}
	}
	generator, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if generator.segmentSize != DefaultSegmentSize {
		t.Fatalf("default segment size = %d, want %d", generator.segmentSize, DefaultSegmentSize)
	}
}

func TestGeneratorHonorsCanceledContextBeforeReserving(t *testing.T) {
	store := newMemoryStore(1)
	generator, err := New(store, Options{SegmentSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if id, err := generator.Next(ctx); !errors.Is(err, context.Canceled) || id != 0 {
		t.Fatalf("Next(canceled) = (%d, %v), want (0, context.Canceled)", id, err)
	}
	if store.reserveN != 0 {
		t.Fatalf("reservations = %d, want 0", store.reserveN)
	}
}

func TestGeneratorRejectsInvalidStoreSegment(t *testing.T) {
	for _, segment := range []Segment{
		{First: 0, Last: 1},
		{First: 2, Last: 1},
		{First: 1, Last: 3},
		{First: MaxID, Last: MaxID + 1},
	} {
		generator, err := New(staticStore{segment: segment}, Options{SegmentSize: 2})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := generator.Next(context.Background()); !errors.Is(err, ErrInvalidSegment) {
			t.Fatalf("Next() for segment %+v error = %v, want ErrInvalidSegment", segment, err)
		}
	}
}

func TestGeneratorStopsAtBrowserSafeIntegerLimit(t *testing.T) {
	store := &finalSegmentStore{next: MaxID - 1}
	generator, err := New(store, Options{SegmentSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, 2)
	for range 2 {
		id, nextErr := generator.Next(context.Background())
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		ids = append(ids, id)
	}
	if want := []int64{MaxID - 1, MaxID}; !equalIDs(ids, want) {
		t.Fatalf("IDs = %v, want %v", ids, want)
	}
	if _, err := generator.Next(context.Background()); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Next() error = %v, want ErrExhausted", err)
	}
}

type staticStore struct{ segment Segment }

func (s staticStore) Reserve(context.Context, int64) (Segment, error) { return s.segment, nil }

type finalSegmentStore struct{ next int64 }

func (s *finalSegmentStore) Reserve(_ context.Context, size int64) (Segment, error) {
	if s.next > MaxID {
		return Segment{}, ErrExhausted
	}
	remaining := MaxID - s.next + 1
	if size > remaining {
		size = remaining
	}
	segment := Segment{First: s.next, Last: s.next + size - 1}
	s.next = segment.Last + 1
	return segment, nil
}

func equalIDs(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	sort.Slice(left, func(i, j int) bool { return left[i] < left[j] })
	sort.Slice(right, func(i, j int) bool { return right[i] < right[j] })
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func BenchmarkGeneratorParallel(b *testing.B) {
	store := newMemoryStore(1)
	generator, err := New(store, Options{SegmentSize: DefaultSegmentSize})
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	var failed atomic.Bool
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, nextErr := generator.Next(ctx); nextErr != nil {
				failed.Store(true)
				return
			}
		}
	})
	if failed.Load() {
		b.Fatal("generator returned an error")
	}
}
