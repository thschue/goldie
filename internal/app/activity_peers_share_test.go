package app

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// countingResolver counts the A queries and can hold them until released.
type countingResolver struct {
	*fakeResolver
	hostCalls atomic.Int32
	hold      chan struct{}
}

func (c *countingResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	c.hostCalls.Add(1)
	if c.hold != nil {
		<-c.hold
	}
	return c.fakeResolver.LookupHost(ctx, host)
}

func sharedFinder(r peerResolver) *activityPeerFinder {
	return &activityPeerFinder{resolver: r, logger: slog.New(slog.DiscardHandler), name: headless, port: "8080", ttl: time.Hour}
}

func goldieZero() *fakeResolver {
	return &fakeResolver{
		hosts: map[string][]string{headless: {"10.9.0.5"}},
		ptrs:  map[string][]string{"10.9.0.5": {"goldie-0.goldie-headless.goldie.svc.cluster.local."}},
	}
}

// Within the ttl one lookup serves every caller; after it, a new one runs.
func TestPeerFinderSharesOneLookupWithinTheTTL(t *testing.T) {
	r := &countingResolver{fakeResolver: goldieZero()}
	f := sharedFinder(r)
	for range 3 {
		if peers, err := f.find(t.Context()); err != nil || len(peers) != 1 {
			t.Fatalf("find = %+v, %v", peers, err)
		}
	}
	if n := r.hostCalls.Load(); n != 1 {
		t.Fatalf("%d lookups within the ttl, want 1", n)
	}
	f.mu.Lock()
	f.at = f.at.Add(-2 * time.Hour)
	f.mu.Unlock()
	if _, err := f.find(t.Context()); err != nil || r.hostCalls.Load() != 2 {
		t.Fatalf("after the ttl: err %v, %d lookups, want a second", err, r.hostCalls.Load())
	}
}

// A failed lookup is not kept: the next caller asks again.
func TestPeerFinderDoesNotKeepAFailure(t *testing.T) {
	fake := goldieZero()
	good := fake.hosts
	fake.hosts = nil
	r := &countingResolver{fakeResolver: fake}
	f := sharedFinder(r)
	if _, err := f.find(t.Context()); err == nil {
		t.Fatal("want the lookup's error")
	}
	fake.mu.Lock()
	fake.hosts = good
	fake.mu.Unlock()
	if peers, err := f.find(t.Context()); err != nil || len(peers) != 1 {
		t.Fatalf("after the failure: %+v, %v; want the peer", peers, err)
	}
}

// A caller that gives up neither cuts the shared lookup short nor leaves it
// out of the share: the next caller gets the complete result without a
// second lookup.
func TestPeerFinderLookupOutlivesACancelledCaller(t *testing.T) {
	r := &countingResolver{fakeResolver: goldieZero(), hold: make(chan struct{})}
	f := sharedFinder(r)
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() { _, err := f.find(ctx); errc <- err }()
	for r.hostCalls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-errc; err == nil {
		t.Fatal("the cancelled caller wants its context's error")
	}
	close(r.hold)
	peers, err := f.find(t.Context())
	if err != nil || len(peers) != 1 || r.hostCalls.Load() != 1 {
		t.Fatalf("next caller: %+v, %v after %d lookups; want the peer from the one lookup", peers, err, r.hostCalls.Load())
	}
}
