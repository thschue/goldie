package engine

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/storage/memory"
)

// endlessPack is a pack header followed by bytes that never end.
type endlessPack struct{ n int64 }

func (r *endlessPack) Read(p []byte) (int, error) {
	if r.n == 0 {
		r.n = int64(copy(p, "PACK\x00\x00\x00\x02\x00\x00\x00\x01"))
		return int(r.n), nil
	}
	clear(p)
	r.n += int64(len(p))
	return len(p), nil
}

// TestPackWriterStopsAtTheLimit fetches an endless pack into the store a
// clone uses: the transfer stops at the budget and nothing is unpacked.
func TestPackWriterStopsAtTheLimit(t *testing.T) {
	const limit = 1 << 20
	b := &gitBudget{mem: limit}
	s := newBudgetStorage(b)
	r := &endlessPack{}
	err := packfile.UpdateObjectStorage(s, r)
	if !errors.Is(err, errLoadLimit) || !b.exceeded() {
		t.Fatalf("err = %v, want the load limit", err)
	}
	if r.n > limit+64<<10 {
		t.Fatalf("read %d bytes with a %d byte budget", r.n, limit)
	}
	if len(s.Objects) != 0 {
		t.Fatalf("%d objects unpacked", len(s.Objects))
	}
}

// TestCheckPackStopsBeforeUnpacking hands the store a small pack whose object
// unpacks past the budget: it is refused from its header, and nothing is
// unpacked.
func TestCheckPackStopsBeforeUnpacking(t *testing.T) {
	src := memory.NewStorage()
	obj := src.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	w, err := obj.Writer()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(strings.Repeat("a", 8<<20))); err != nil {
		t.Fatal(err)
	}
	h, err := src.SetEncodedObject(obj)
	if err != nil {
		t.Fatal(err)
	}
	var pack bytes.Buffer
	if _, err := packfile.NewEncoder(&pack, src, false).Encode([]plumbing.Hash{h}, 10); err != nil {
		t.Fatal(err)
	}

	const limit = 1 << 20
	if pack.Len() >= limit/2 {
		t.Fatalf("pack is %d bytes; the test needs it well under the budget", pack.Len())
	}
	b := &gitBudget{mem: limit}
	s := newBudgetStorage(b)
	if err := packfile.UpdateObjectStorage(s, bytes.NewReader(pack.Bytes())); !errors.Is(err, errLoadLimit) {
		t.Fatalf("err = %v, want the load limit", err)
	}
	if len(s.Objects) != 0 {
		t.Fatalf("%d objects unpacked", len(s.Objects))
	}

	// With room for it, the same pack unpacks.
	s = newBudgetStorage(&gitBudget{mem: 16 << 20})
	if err := packfile.UpdateObjectStorage(s, bytes.NewReader(pack.Bytes())); err != nil || len(s.Objects) != 1 {
		t.Fatalf("err = %v, objects = %d", err, len(s.Objects))
	}
}

func TestDeltaTargetSize(t *testing.T) {
	for _, tc := range []struct {
		in   []byte
		want int64
		err  bool
	}{
		{[]byte{0x05, 0x07}, 7, false},
		{[]byte{0x05, 0x80, 0x01}, 128, false},
		{[]byte{0x80, 0x01, 0xff, 0xff, 0x7f}, 0x1fffff, false},
		{[]byte{0x05}, 0, true},
		{[]byte{0x05, 0x80}, 0, true},
		{append([]byte{0x05}, bytes.Repeat([]byte{0xff}, 11)...), 0, true},
		{[]byte{0x05, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f}, -1, false},
	} {
		got, err := deltaTargetSize(tc.in)
		if (err != nil) != tc.err || (!tc.err && got != tc.want) {
			t.Errorf("%x: got %d, %v; want %d, err %v", tc.in, got, err, tc.want, tc.err)
		}
	}
}
