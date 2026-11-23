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

// Trigger starts a poll in the background. started is false while a poll
// runs; ready is false when no Syncer is bound, Start has not run, or the
// server is stopping, so nothing can poll.
func (c *syncControl) Trigger() (started, ready bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.syncer == nil || c.ctx == nil || c.ctx.Err() != nil {
		return false, false
	}
	return c.syncer.Trigger(c.ctx, c.onPoll), true
}
