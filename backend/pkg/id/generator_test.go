package id

import "testing"

func TestGeneratorCreatesIncreasingIDs(t *testing.T) {
	generator, err := New(7)
	if err != nil {
		t.Fatal(err)
	}

	first, err := generator.Next()
	if err != nil {
		t.Fatal(err)
	}
	second, err := generator.Next()
	if err != nil {
		t.Fatal(err)
	}
	if second <= first {
		t.Fatalf("ids are not increasing: %d then %d", first, second)
	}
	if got := Parse(first).WorkerID; got != 7 {
		t.Fatalf("worker id = %d, want 7", got)
	}
}
