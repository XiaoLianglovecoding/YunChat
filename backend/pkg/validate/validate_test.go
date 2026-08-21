package validate

import "testing"

func TestPageLimit(t *testing.T) {
	tests := []struct{ input, want int }{{0, 20}, {10, 10}, {200, 100}}
	for _, test := range tests {
		if got := PageLimit(test.input, 20, 100); got != test.want {
			t.Fatalf("PageLimit(%d) = %d, want %d", test.input, got, test.want)
		}
	}
}
