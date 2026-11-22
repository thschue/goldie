package a

import "testing"

func TestA(t *testing.T) {
	if got := A(); got != 1 {
		t.Fatalf("A() = %d, want 1", got)
	}
}

func TestB(t *testing.T) {
	if got := A() + 1; got != 2 {
		t.Fatalf("A()+1 = %d, want 2", got)
	}
}

func TestC(t *testing.T) {
	if A() == 0 {
		t.Fatal("zero")
	}
}
