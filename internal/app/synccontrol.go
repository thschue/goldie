package app

import (
	"context"
	"sync"

	"github.com/thschue/goldie/internal/engine"
)

// syncControl is the api.SyncControl over the server's Syncer. The router is
// built before the Syncer and the server's context exist, so it is created
// empty and bound by newPublic and Public.Start. A poll it triggers gets the
// server's context, like the poll loop's (#256), so shutdown stops it, and is
// logged like a loop poll.
type syncControl struct {
	mu     sync.Mutex
	syncer *engine.Syncer
	ctx    context.Context
	onPoll func(engine.SyncStatus)
}

// bind sets the Syncer the control reads and polls.
func (c *syncControl) bind(s *engine.Syncer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.syncer = s
}

// run sets the context and the poll callback of the polls Trigger starts.
func (c *syncControl) run(ctx context.Context, onPoll func(engine.SyncStatus)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ctx, c.onPoll = ctx, onPoll
}

// Status is the Syncer's status, empty while none is bound.
func (c *syncControl) Status() engine.SyncStatus {
	c.mu.Lock()
	s := c.syncer
	c.mu.Unlock()
	if s == nil {
		return engine.SyncStatus{}
	}
	return s.Status()
}

// Trigger starts a poll in the background and reports whether it did.
func (c *syncControl) Trigger() bool {
	c.mu.Lock()
	s, ctx, onPoll := c.syncer, c.ctx, c.onPoll
	c.mu.Unlock()
	if s == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background() // Start has not run: nothing to stop it yet
	}
	return s.Trigger(ctx, onPoll)
}
