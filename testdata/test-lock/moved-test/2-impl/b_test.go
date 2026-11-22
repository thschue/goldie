package a

import "testing"

func TestB(t *testing.T) {
	if got := A() + 1; got != 2 {
		t.Fatalf("A()+1 = %d, want 2", got)
	}
}
