package a

import "testing"

func TestA(t *testing.T) {
	if got := A(); got != 1 {
		t.Fatalf("A() = %d, want 1", got)
	}
}

func TestB(t *testing.T) {
	if got := A() + 2; got != 3 {
		t.Fatalf("A()+2 = %d, want 3", got)
	}
}
