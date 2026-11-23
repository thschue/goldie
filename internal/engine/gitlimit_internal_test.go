package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/transport"
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
	s := newLoadStorage(context.Background(), b)
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
	s := newLoadStorage(context.Background(), b)
	if err := packfile.UpdateObjectStorage(s, bytes.NewReader(pack.Bytes())); !errors.Is(err, errLoadLimit) {
		t.Fatalf("err = %v, want the load limit", err)
	}
	if len(s.Objects) != 0 {
		t.Fatalf("%d objects unpacked", len(s.Objects))
	}

	// With room for it, the same pack unpacks.
	s = newLoadStorage(context.Background(), &gitBudget{mem: 16 << 20})
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

// tinyObjectPack encodes n distinct blobs of a few bytes each, the shape of
// pack whose bookkeeping outweighs its content. It returns the pack and the
// sum of the blob sizes.
func tinyObjectPack(t *testing.T, n int) ([]byte, int64) {
	t.Helper()
	src := memory.NewStorage()
	hashes := make([]plumbing.Hash, 0, n)
	var content int64
	for i := range n {
		obj := src.NewEncodedObject()
		obj.SetType(plumbing.BlobObject)
		w, err := obj.Writer()
		if err != nil {
			t.Fatal(err)
		}
		c := strconv.Itoa(i)
		if _, err := w.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
		content += int64(len(c))
		h, err := src.SetEncodedObject(obj)
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
	}
	var pack bytes.Buffer
	if _, err := packfile.NewEncoder(&pack, src, false).Encode(hashes, 0); err != nil {
		t.Fatal(err)
	}
	return pack.Bytes(), content
}

// heapSampler is a context that measures the live heap each time the code
// under test asks it whether it is done, once enough has been allocated since
// the last sample. Unpacking checks its Load's context as it reads the pack,
// so the samples are taken on the goroutine doing the work, while it
// allocates nothing: runtime.GC then returns with exactly what is reachable,
// whatever the scheduler or the load on the machine. A sampler on its own
// goroutine can starve while the unpacking runs, and a collection that
// overlaps the unpacking counts what it allocates meanwhile as live.
type heapSampler struct {
	context.Context
	step      uint64 // bytes allocated between samples
	base      int64
	lastAlloc uint64
	samples   int
	peak      int64
	last      int64
}

func readHeap() (live int64, allocated uint64) {
	s := []metrics.Sample{{Name: "/gc/heap/live:bytes"}, {Name: "/gc/heap/allocs:bytes"}}
	metrics.Read(s)
	return int64(s[0].Value.Uint64()), s[1].Value.Uint64()
}

func newHeapSampler(step uint64) *heapSampler {
	runtime.GC()
	base, alloc := readHeap()
	return &heapSampler{Context: context.Background(), step: step, base: base, lastAlloc: alloc}
}

// sample collects and records the live heap above the heap before the work.
func (h *heapSampler) sample() {
	runtime.GC()
	live, alloc := readHeap()
	h.peak = max(h.peak, live-h.base)
	h.last = live
	h.lastAlloc = alloc
	h.samples++
}

func (h *heapSampler) Err() error {
	if _, alloc := readHeap(); alloc-h.lastAlloc >= h.step {
		h.sample()
	}
	return h.Context.Err()
}

const (
	memPeakChildEnv = "GOLDIE_TEST_GIT_MEMORY_PEAK_CHILD"
	memPeakLine     = "GIT_MEMORY_PEAK "
	memPeakObjects  = 30000
)

// TestGitMemoryPeakChild is the measuring half of
// TestGitMemoryChargeCoversMeasuredPeak. It runs only in the child process
// that test starts, and prints the peak it measured. In a process of its own
// nothing else in the package allocates while it measures, which a live-heap
// reading cannot tell apart from the unpacking.
func TestGitMemoryPeakChild(t *testing.T) {
	if os.Getenv(memPeakChildEnv) == "" {
		t.Skip("runs as a child of TestGitMemoryChargeCoversMeasuredPeak")
	}
	pack, _ := tinyObjectPack(t, memPeakObjects)
	const room = 1 << 30
	hs := newHeapSampler(1 << 18)
	s := newLoadStorage(hs, &gitBudget{wire: room, mem: room})
	err := packfile.UpdateObjectStorage(s, bytes.NewReader(pack))
	hs.sample() // what stays live once everything is unpacked
	if err != nil || len(s.Objects) != memPeakObjects {
		t.Fatalf("err = %v, objects = %d, want %d", err, len(s.Objects), memPeakObjects)
	}
	fmt.Printf("%speak=%d samples=%d\n", memPeakLine, hs.peak, hs.samples)
}

// memPeakChildBound caps how long the measuring child may run. The child takes
// about 4 seconds; runChild takes the bound as a parameter so a test can use a short one.
const memPeakChildBound = 2 * time.Minute

// memPeakBound is the bound measurePeakInChild starts from; a test may raise it
// to see the test's own deadline cut it short.
var memPeakBound = memPeakChildBound

const memHangChildEnv = "GOLDIE_TEST_GIT_MEMORY_HANG_CHILD"

// TestGitMemoryHangChild is the hanging child TestGitMemoryChildDeadline starts.
// It runs only there, and sleeps far longer than any deadline that test sets.
func TestGitMemoryHangChild(t *testing.T) {
	if os.Getenv(memHangChildEnv) == "" {
		t.Skip("runs as a child of TestGitMemoryChildDeadline")
	}
	time.Sleep(30 * time.Second)
}

// childBound is the deadline for a child: the bound, cut short by the test's
// own deadline when it has one (t.Deadline reports ok == false when unset).
func childBound(t *testing.T, bound time.Duration) time.Duration {
	if dl, ok := t.Deadline(); ok {
		if rem := time.Until(dl); rem < bound {
			return rem
		}
	}
	return bound
}

// runChild re-executes the test binary to run only the named test, with env
// set, under a deadline. A child past the deadline is killed and reaped, and
// the error names the deadline. It returns the child's combined output and its
// pid.
func runChild(test, env string, bound time.Duration) (out []byte, pid int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+test+"$", "-test.count=1")
	cmd.Env = append(os.Environ(), env+"=1")
	cmd.WaitDelay = 5 * time.Second
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Start(); err != nil {
		return nil, 0, err
	}
	pid = cmd.Process.Pid
	waitErr := cmd.Wait() // Wait ends the copying into buf, so read buf only after it
	return buf.Bytes(), pid, childResult(ctx, test, bound, waitErr)
}

// childResult turns the child's Wait result into runChild's error. The
// deadline is reported only for a child that failed with the context done: one
// that exited cleanly at the last instant succeeded, whatever the clock says.
func childResult(ctx context.Context, test string, bound time.Duration, waitErr error) error {
	if waitErr != nil && ctx.Err() != nil {
		return fmt.Errorf("the child %s hit its %v deadline and was killed: %w", test, bound, ctx.Err())
	}
	return waitErr
}

// runChildFn is the seam measurePeakInChild runs the child through, so
// the bound test can observe what it passes.
var runChildFn = runChild

// measurePeakInChild runs TestGitMemoryPeakChild in a new process and returns
// what it measured. A child that fails, crashes, hangs past its deadline or
// prints nothing parseable fails the test.
func measurePeakInChild(t *testing.T) (peak int64, samples int) {
	t.Helper()
	out, _, err := runChildFn("TestGitMemoryPeakChild", memPeakChildEnv, childBound(t, memPeakBound))
	if err != nil {
		t.Fatalf("the measuring child failed: %v\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(line, memPeakLine); ok {
			if _, err := fmt.Sscanf(rest, "peak=%d samples=%d", &peak, &samples); err != nil {
				t.Fatalf("the measuring child printed %q: %v", line, err)
			}
			return peak, samples
		}
	}
	t.Fatalf("the measuring child printed no measurement:\n%s", out)
	return 0, 0
}

// TestGitMemoryChildDeadline runs a child that hangs under a short deadline:
// the run returns quickly, the error names the deadline, and the child is gone.
func TestGitMemoryChildDeadline(t *testing.T) {
	const bound = 1500 * time.Millisecond
	start := time.Now()
	_, pid, err := runChild("TestGitMemoryHangChild", memHangChildEnv, bound)
	took := time.Since(start)
	if err == nil {
		t.Fatal("a hung child returned no error")
	}
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), bound.String()) {
		t.Fatalf("err = %q, want a timeout naming the %v deadline", err, bound)
	}
	if took < bound || took > bound+10*time.Second {
		t.Fatalf("returned after %v, want about %v", took, bound)
	}
	if pid <= 0 {
		t.Fatalf("pid = %d", pid)
	}
	// The elapsed-time check above is the main evidence the child was killed
	// at the deadline. This liveness probe is a second look: a pid reused by
	// another process could in theory fail it spuriously, which is rare enough
	// that it is not worth more machinery.
	if p, _ := os.FindProcess(pid); p != nil && p.Signal(syscall.Signal(0)) == nil {
		t.Fatalf("child %d is still running after the deadline", pid)
	}
}

// TestGitMemoryChildResult pins which Wait results runChild reports as a
// deadline: only a failure with the context done. A clean exit at an expired
// deadline is a success; a failure with a live context keeps its own error.
func TestGitMemoryChildResult(t *testing.T) {
	expired, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-expired.Done()
	live := context.Background()
	boom := errors.New("exit status 1")

	if err := childResult(expired, "X", time.Second, nil); err != nil {
		t.Fatalf("a child that exited cleanly at the deadline failed: %v", err)
	}
	err := childResult(expired, "X", time.Second, boom)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "1s") {
		t.Fatalf("err = %v, want a timeout naming the 1s deadline", err)
	}
	if err := childResult(live, "X", time.Second, boom); !errors.Is(err, boom) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the child's own failure", err)
	}
	if err := childResult(live, "X", time.Second, nil); err != nil {
		t.Fatalf("err = %v for a clean exit", err)
	}
}

// TestGitMemoryPeakChildUsesBound checks measurePeakInChild starts the peak
// child with childBound(t, memPeakBound), in two parts.
//
// A bound well under the test's remaining time (30s; the default test deadline
// is 10 minutes) must reach the child exactly. A revert to the raw constant
// memPeakChildBound (2 minutes) fails here. This part holds with or without a
// test deadline, so it is the only one that applies under -timeout 0. It is
// skipped when less than a minute of the test deadline remains, since the bound
// would then be cut legitimately.
//
// A bound far above the test deadline must be cut to the time remaining. A
// child started with the bound uncut fails here. Without a test deadline there
// is nothing to cut to, and the bound must arrive as it is.
func TestGitMemoryPeakChildUsesBound(t *testing.T) {
	const (
		short = 30 * time.Second
		huge  = 1000 * time.Hour
	)
	var gotTest, gotEnv string
	var gotBound time.Duration
	calls := 0
	origFn, origBound := runChildFn, memPeakBound
	t.Cleanup(func() { runChildFn, memPeakBound = origFn, origBound })
	runChildFn = func(test, env string, bound time.Duration) ([]byte, int, error) {
		calls++
		gotTest, gotEnv, gotBound = test, env, bound
		return []byte(memPeakLine + "peak=7 samples=3\n"), 1, nil
	}
	run := func(bound time.Duration) (started time.Time) {
		memPeakBound, calls = bound, 0
		started = time.Now() // the child's bound is measured no earlier than this
		peak, samples := measurePeakInChild(t)
		if calls != 1 || peak != 7 || samples != 3 {
			t.Fatalf("calls = %d, peak = %d, samples = %d", calls, peak, samples)
		}
		if gotTest != "TestGitMemoryPeakChild" || gotEnv != memPeakChildEnv {
			t.Fatalf("started %q with %q, want the peak child", gotTest, gotEnv)
		}
		return started
	}

	started := run(short)
	if dl, ok := t.Deadline(); !ok || dl.Sub(started) > time.Minute {
		if gotBound != short {
			t.Fatalf("bound = %v, want exactly the %v bound (not the %v constant)", gotBound, short, memPeakChildBound)
		}
	}

	started = run(huge)
	if dl, ok := t.Deadline(); ok {
		if max := dl.Sub(started); gotBound <= 0 || gotBound > max {
			t.Fatalf("bound = %v, want it cut to the test's remaining time (at most %v)", gotBound, max)
		}
	} else if gotBound != huge {
		t.Fatalf("bound = %v without a test deadline, want %v", gotBound, huge)
	}
}

// TestGitMemoryChildBoundUsesTestDeadline checks the child's bound is the
// shorter of the bound and the test's remaining time, and the bound alone when
// the test has no deadline.
func TestGitMemoryChildBoundUsesTestDeadline(t *testing.T) {
	if got := childBound(t, time.Nanosecond); got != time.Nanosecond {
		t.Fatalf("childBound = %v, want the 1ns bound", got)
	}
	const huge = 1000 * time.Hour
	got := childBound(t, huge)
	if _, ok := t.Deadline(); ok {
		if got <= 0 || got >= huge {
			t.Fatalf("childBound = %v, want it cut to the test deadline", got)
		}
	} else if got != huge {
		t.Fatalf("childBound = %v without a test deadline, want the bound", got)
	}
}

// TestGitMemoryChargeCoversMeasuredPeak unpacks a pack of many tiny distinct
// objects and measures the peak live heap while it unpacks: what the memory
// budget is charged must cover that peak, and a budget the size of the
// measured peak must refuse the pack before unpacking it. The charge per
// object stays within the 1024 bytes the spec allows.
func TestGitMemoryChargeCoversMeasuredPeak(t *testing.T) {
	const n = memPeakObjects
	pack, content := tinyObjectPack(t, n)

	peak, samples := measurePeakInChild(t)
	if samples < 10 || peak <= 0 {
		t.Fatalf("%d samples, peak %d: the measurement saw nothing of the unpacking", samples, peak)
	}

	const room = 1 << 30
	b := &gitBudget{wire: room, mem: room}
	s := newLoadStorage(context.Background(), b)
	if err := packfile.UpdateObjectStorage(s, bytes.NewReader(pack)); err != nil || len(s.Objects) != n {
		t.Fatalf("err = %v, objects = %d, want %d", err, len(s.Objects), n)
	}
	charged := room - b.mem
	t.Logf("%d samples, peak %d bytes (%d per object), charged %d (%d per object)", samples, peak, peak/n, charged, charged/n)
	if charged < peak {
		t.Fatalf("charged %d bytes (%d per object) for a pack whose unpacking peaked at %d live bytes (%d per object)",
			charged, charged/n, peak, peak/n)
	}
	if overhead := (charged - int64(len(pack)) - content) / n; overhead > 1024 {
		t.Fatalf("charged %d bytes per object beyond the pack and its content, over the 1024 the spec allows", overhead)
	}

	// A budget of exactly the measured peak refuses the pack, before any
	// object is unpacked.
	s = newLoadStorage(context.Background(), &gitBudget{wire: room, mem: peak})
	if err := packfile.UpdateObjectStorage(s, bytes.NewReader(pack)); !errors.Is(err, errLoadLimit) {
		t.Fatalf("err = %v under a budget of the measured peak %d, want the load limit", err, peak)
	}
	if len(s.Objects) != 0 {
		t.Fatalf("%d objects unpacked over the budget", len(s.Objects))
	}
}

// TestGitHTTPDialerNeedsALoadBudget dials through the https client with and
// without a Load's budget in the context. With one, the request reaches the
// server and its reads are charged to the wire budget. Without one, or with
// anything else under the key, the dial fails and no request reaches the
// server: a connection nothing charges is never used.
func TestGitHTTPDialerNeedsALoadBudget(t *testing.T) {
	var reached atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	ep, err := transport.NewEndpoint(srv.URL + "/org/repo.git")
	if err != nil {
		t.Fatal(err)
	}
	client := newGitHTTPClient()
	list := func(ctx context.Context) error {
		sess, err := client.NewUploadPackSession(ep, nil)
		if err != nil {
			return err
		}
		defer func() { _ = sess.Close() }()
		_, err = sess.AdvertisedReferencesContext(ctx)
		return err
	}

	const room = 1 << 20
	b := &gitBudget{wire: room, mem: room}
	_ = list(context.WithValue(context.Background(), gitBudgetKey{}, b))
	if reached.Load() == 0 {
		t.Fatal("with a budget, no request reached the server")
	}
	if b.wire >= room {
		t.Fatal("the response was read without charging the wire budget")
	}

	cancellable, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The nil budget comes last: on a dialer that accepts it, reading the
	// response dereferences it and takes the test binary down.
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"plain context", context.Background()},
		{"cancellable context", cancellable},
		{"not a budget under the key", context.WithValue(context.Background(), gitBudgetKey{}, "budget")},
		{"nil budget", context.WithValue(context.Background(), gitBudgetKey{}, (*gitBudget)(nil))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reached.Store(0)
			if err := list(tc.ctx); err == nil {
				t.Fatal("dialled without a Load budget")
			}
			if n := reached.Load(); n != 0 {
				t.Fatalf("%d requests reached the server without a Load budget", n)
			}
		})
	}
}

// budgetSpender is the gitBudget method set the spec names, as treeBudget
// has spend and spendBytes. The test asserts it at run time, so this file
// compiles both before and after the rename.
type budgetSpender interface {
	spendWire(n int64) error
	spendMem(n int64) error
}

// TestGitBudgetSpendsWireAndMemorySeparately checks the budget's two spends:
// spendWire charges only the wire budget and spendMem only the memory budget.
// A spend past what is left, or of a negative size, fails with the load
// limit, records the hit and leaves that budget spent.
func TestGitBudgetSpendsWireAndMemorySeparately(t *testing.T) {
	spender := func(b *gitBudget) budgetSpender {
		t.Helper()
		var v any = b
		if _, old := v.(interface{ spend(*int64, int64) error }); old {
			t.Error("gitBudget still has spend(&field, n); it is split into spendWire and spendMem")
		}
		sp, ok := v.(budgetSpender)
		if !ok {
			t.Fatal("gitBudget has no spendWire(int64) error and spendMem(int64) error")
		}
		return sp
	}

	b := &gitBudget{wire: 10, mem: 100}
	sp := spender(b)
	if err := sp.spendWire(4); err != nil || b.wire != 6 || b.mem != 100 {
		t.Fatalf("spendWire(4): err = %v, wire = %d, mem = %d; want nil, 6, 100", err, b.wire, b.mem)
	}
	if err := sp.spendMem(40); err != nil || b.wire != 6 || b.mem != 60 {
		t.Fatalf("spendMem(40): err = %v, wire = %d, mem = %d; want nil, 6, 60", err, b.wire, b.mem)
	}
	if err := sp.spendWire(6); err != nil || b.wire != 0 || b.exceeded() {
		t.Fatalf("spendWire of exactly what is left: err = %v, wire = %d, exceeded = %v", err, b.wire, b.exceeded())
	}

	for _, tc := range []struct {
		name  string
		spend func(budgetSpender) error
		left  func(*gitBudget) int64
	}{
		{"wire past what is left", func(s budgetSpender) error { return s.spendWire(11) }, func(b *gitBudget) int64 { return b.wire }},
		{"memory past what is left", func(s budgetSpender) error { return s.spendMem(101) }, func(b *gitBudget) int64 { return b.mem }},
		{"negative wire", func(s budgetSpender) error { return s.spendWire(-1) }, func(b *gitBudget) int64 { return b.wire }},
		{"negative memory", func(s budgetSpender) error { return s.spendMem(-1) }, func(b *gitBudget) int64 { return b.mem }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &gitBudget{wire: 10, mem: 100}
			sp := spender(b)
			if err := tc.spend(sp); !errors.Is(err, errLoadLimit) || !b.exceeded() {
				t.Fatalf("err = %v, exceeded = %v; want the load limit, recorded", err, b.exceeded())
			}
			if left := tc.left(b); left != 0 {
				t.Fatalf("%d left after a refused spend, want the budget spent", left)
			}
		})
	}
}
