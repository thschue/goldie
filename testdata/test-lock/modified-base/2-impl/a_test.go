package a

import "testing"

func TestA(t *testing.T) {
	if got := A(); got < 0 {
		t.Fatalf("A() = %d, want 1", got)
	}
}
