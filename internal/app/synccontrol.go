package app

import (
	"context"
	"sync"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/engine"
)

// syncControl is the api.SourceSyncControl over the server's Syncer, or over
// the Sources of GOLDIE_SOURCES. The router is built before the Syncer and
// the server's context exist, so it is created empty and bound by newPublic
// and Public.Start. A poll it triggers gets the server's context, like the
// poll loop's (#256), so shutdown stops it, and is logged like a loop poll.
type syncControl struct {
	mu     sync.Mutex
	syncer *engine.Syncer
	name   string      // the Syncer's name in the status
	list   *sourceList // the list's Sources, when the server has one instead
	ctx    context.Context
	onPoll func(engine.SyncStatus)
}

// bind sets the Syncer the control reads and polls, under name, or the list
// that stands for the Sources of GOLDIE_SOURCES.
func (c *syncControl) bind(s *engine.Syncer, name string, list *sourceList) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.syncer, c.name, c.list = s, name, list
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

// Sources lists the Sources with their status in preference order: every
// member of the list, or the one Syncer.
func (c *syncControl) Sources() []api.SourceSync {
	c.mu.Lock()
	s, name, list := c.syncer, c.name, c.list
	c.mu.Unlock()
	switch {
	case list != nil:
		return list.statuses()
	case s != nil:
		return []api.SourceSync{{Name: name, Status: s.Status()}}
	}
	return nil
}

// Trigger starts a poll of every Source in the background. started is false
// while a poll runs; ready is false when no Syncer is bound, Start has not
// run, or the server is stopping, so nothing can poll.
func (c *syncControl) Trigger() (started, ready bool) {
	started, ready, _ = c.TriggerSource("")
	return started, ready
}

// TriggerSource starts the poll of the named Source in the background, or of
// every Source for an empty name, started when any poll started. found is
// false when no Source has the name.
func (c *syncControl) TriggerSource(name string) (started, ready, found bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if (c.syncer == nil && c.list == nil) || c.ctx == nil || c.ctx.Err() != nil {
		return false, false, true
	}
	if c.list != nil {
		started, found = c.list.trigger(c.ctx, name)
		return started, true, found
	}
	if name != "" && name != c.name {
		return false, true, false
	}
	return c.syncer.Trigger(c.ctx, c.onPoll), true, true
}
