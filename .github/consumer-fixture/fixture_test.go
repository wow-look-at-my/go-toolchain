package consumerfixture

import "testing"

func TestSum(t *testing.T) {
	if got := Sum(1, 2, 3); got != 6 {
		t.Fatalf("Sum(1, 2, 3) = %d, want 6", got)
	}
	if got := Sum(); got != 0 {
		t.Fatalf("Sum() = %d, want 0", got)
	}
}
