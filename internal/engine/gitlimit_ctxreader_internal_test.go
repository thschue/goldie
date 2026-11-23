package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

// TestCtxReaderStopsWhenTheContextEnds reads through a ctxReader: while the
// context is live it reads and seeks like the reader under it, and once the
// context is cancelled Read returns the context's error and no bytes.
func TestCtxReaderStopsWhenTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := ctxReader{Reader: bytes.NewReader([]byte("0123456789")), ctx: ctx}

	buf := make([]byte, 4)
	if n, err := r.Read(buf); err != nil || string(buf[:n]) != "0123" {
		t.Fatalf("live Read = %q, %v", buf[:n], err)
	}
	if _, err := r.Seek(2, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	if n, err := r.Read(buf); err != nil || string(buf[:n]) != "2345" {
		t.Fatalf("Read after Seek = %q, %v", buf[:n], err)
	}

	cancel()
	n, err := r.Read(buf)
	if n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("Read after cancel = %d, %v; want 0, context.Canceled", n, err)
	}

	dctx, dcancel := context.WithTimeout(context.Background(), 0)
	defer dcancel()
	r = ctxReader{Reader: bytes.NewReader([]byte("abc")), ctx: dctx}
	if n, err := r.Read(buf); n != 0 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Read past the deadline = %d, %v; want 0, context.DeadlineExceeded", n, err)
	}
}

// afterErrs is a context that reports no error for its first ok calls to Err
// and context.Canceled from then on. The test counts how often checkPack asks
// on the pack and lets exactly that many calls succeed, which puts the end of
// the context at the first read of the parse, deterministically and with no
// timing, however many times checkPack asks.
type afterErrs struct {
	context.Context
	ok    int
	calls int
}

func (c *afterErrs) Err() error {
	c.calls++
	if c.calls > c.ok {
		return context.Canceled
	}
	return nil
}

// TestPackWriterStopsParsingWhenTheContextEnds ends the Load's context after
// checkPack has walked the pack and before the parse reads it: closing the
// pack writer fails with the context's error and unpacks nothing, whereas
// with a live context the same pack unpacks whole. The pack is stored whole
// objects, so no delta observer could stop it: the reader is the only stop.
func TestPackWriterStopsParsingWhenTheContextEnds(t *testing.T) {
	const objects = 50
	pack, _ := tinyObjectPack(t, objects)

	unpack := func(ctx context.Context) (budgetStorage, error) {
		s := newLoadStorage(ctx, &gitBudget{mem: 1 << 30, wire: 1 << 30})
		w, err := s.PackfileWriter()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(pack); err != nil {
			t.Fatal(err)
		}
		return s, w.Close()
	}

	s, err := unpack(context.Background())
	if err != nil || len(s.Objects) == 0 {
		t.Fatalf("with a live context: err = %v, objects = %d", err, len(s.Objects))
	}

	// Calibrate: how many Err calls does checkPack make on this pack?
	counter := &afterErrs{Context: context.Background(), ok: 1 << 30}
	if err := checkPack(counter, pack, &gitBudget{mem: 1 << 30, wire: 1 << 30}); err != nil {
		t.Fatalf("checkPack on the whole pack: %v", err)
	}
	if counter.calls == 0 {
		t.Fatal("checkPack never asked the context, so the calibration is meaningless")
	}

	ctx := &afterErrs{Context: context.Background(), ok: counter.calls}
	s, err = unpack(ctx)
	if ctx.calls <= ctx.ok {
		t.Fatalf("the parse never asked the context: %d calls, %d allowed", ctx.calls, ctx.ok)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled from the parse", err)
	}
	if len(s.Objects) != 0 {
		t.Fatalf("%d objects unpacked after the context ended", len(s.Objects))
	}
}
