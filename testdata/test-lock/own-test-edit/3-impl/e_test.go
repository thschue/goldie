package a

import "testing"

func TestE(t *testing.T) {
	if A() != 1 {
		t.Fatal("x")
	}
}
