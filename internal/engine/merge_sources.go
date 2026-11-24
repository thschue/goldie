package engine

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// NamedSource is a Source with the name an operator gave it. Names appear in
// SyncStatus and in conflict errors.
type NamedSource struct {
	Name   string
	Source Source
}

// Shadowed is one Item a Source lost to an earlier Source of the same Scope:
// Item is <kind>:<ID>, By the Source whose copy answers.
type Shadowed struct {
	Item  string
	Scope string
	By    string
}

// SourceSet forms one view over several Sources. Scope comes first (local,
// Project, global), as winner has it; within a Scope the earlier listed Source
// wins and the loser is recorded as shadowed. Each Source keeps its own last
// good Snapshot and SyncStatus. An enforced Item that would lose on preference,
// or a Suppression that names an enforced Item of another Source, rejects the
// Snapshot that just loaded (ADR 0030).
type SourceSet struct {
	entries []*sourceEntry

	// mu guards every entry's snap and status and merged. A poll checks its
	// Snapshot against the others' and swaps it in under one hold, so two
	// Sources changing in the same tick cannot both pass the check.
	mu     sync.Mutex
	merged *Snapshot
	// now is replaced in tests.
	now func() time.Time
}

type sourceEntry struct {
	name string
	src  Source
	// pollMu serialises the polls of this Source, Load included, so a slow
	// older load never replaces a newer Snapshot. mu is not held while loading.
	pollMu sync.Mutex
	snap   *Snapshot
	status SyncStatus
}

// NewSourceSet returns a SourceSet over srcs, in preference order. It loads
// nothing until Poll.
func NewSourceSet(srcs ...NamedSource) *SourceSet {
	s := &SourceSet{merged: newSnapshot(nil), now: time.Now}
	for _, n := range srcs {
		s.entries = append(s.entries, &sourceEntry{name: n.Name, src: n.Source})
	}
	return s
}

// Merged returns the view over every Source's current Snapshot. A Source that
// has not loaded yet contributes nothing.
func (s *SourceSet) Merged() *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.merged
}

// Status returns a copy of the named Source's status, or the zero value for an
// unknown name.
func (s *SourceSet) Status(name string) SyncStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.name == name {
			st := e.status
			st.Warnings = slices.Clone(st.Warnings)
			st.Shadowed = slices.Clone(st.Shadowed)
			return st
		}
	}
	return SyncStatus{}
}

// entry is the named Source's entry, or nil.
func (s *SourceSet) entry(name string) *sourceEntry {
	for _, c := range s.entries {
		if c.name == name {
			return c
		}
	}
	return nil
}

// Current is Merged, or nil while no Source has loaded, as Syncer.Current is
// before its first load.
func (s *SourceSet) Current() *Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.snap != nil {
			return s.merged
		}
	}
	return nil
}

// Poll loads the named Source once. The Snapshot enters the view only when it
// passes the checks against the other Sources' current Snapshots; otherwise the
// Source keeps its last good Snapshot, the error is recorded redacted in its
// status and returned.
func (s *SourceSet) Poll(ctx context.Context, name string) error {
	e := s.entry(name)
	if e == nil {
		return fmt.Errorf("no Source named %q", name)
	}
	e.pollMu.Lock()
	defer e.pollMu.Unlock()
	return s.pollLocked(ctx, e)
}

// pollLocked is Poll for a caller that holds e.pollMu.
func (s *SourceSet) pollLocked(ctx context.Context, e *sourceEntry) error {
	snap, rev, began, ended, err := timedLoad(ctx, e.src, s.now)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		err = s.accept(e, snap)
	}
	e.status.stamp(began, ended, rev, err)
	return err
}

// Trigger starts one poll of the named Source in the background, with ctx and
// then onPoll as Run has them. found is false for a name that is no Source.
// started is false, with found true, when that Source's poll is already
// running or ctx has ended. The poll runs to the end of ctx, not of the
// caller's request, and nothing waits for it at stop: like Syncer.Trigger, a
// triggered poll is deliberately not tracked.
func (s *SourceSet) Trigger(ctx context.Context, name string, onPoll func(SyncStatus)) (started, found bool) {
	e := s.entry(name)
	if e == nil {
		return false, false
	}
	if ctx.Err() != nil || !e.pollMu.TryLock() {
		return false, true
	}
	go func() {
		defer e.pollMu.Unlock()
		s.pollReport(ctx, name, e, onPoll)
	}()
	return true, true
}

// Run polls the named Source once at once, then on every tick of interval,
// and returns when ctx ends, as Syncer.Run does. onPoll, when not nil, gets
// the Source's status after each poll. A name that is no Source returns at
// once.
func (s *SourceSet) Run(ctx context.Context, name string, interval time.Duration, onPoll func(SyncStatus)) {
	e := s.entry(name)
	if e == nil {
		return
	}
	runLoop(ctx, interval, realTicker, func() {
		// pollMu is held through onPoll, as Trigger holds it, so onPoll never
		// runs concurrently with itself.
		e.pollMu.Lock()
		defer e.pollMu.Unlock()
		s.pollReport(ctx, name, e, onPoll)
	})
}

// pollReport polls the Source and hands its Status to onPoll, when not nil.
// The caller holds e.pollMu, so onPoll never runs concurrently with itself,
// whether the poll is Run's or a Trigger's.
func (s *SourceSet) pollReport(ctx context.Context, name string, e *sourceEntry, onPoll func(SyncStatus)) {
	_ = s.pollLocked(ctx, e)
	if onPoll != nil {
		onPoll(s.Status(name))
	}
}

// loaded lists the Sources that hold a Snapshot, in preference order. The
// caller holds mu.
func (s *SourceSet) loaded() (names []string, snaps []*Snapshot) {
	for _, e := range s.entries {
		if e.snap != nil {
			names, snaps = append(names, e.name), append(snaps, e.snap)
		}
	}
	return names, snaps
}

// accept checks snap against the other Sources' Snapshots and, when it
// passes, swaps it in and rebuilds the view. The caller holds mu.
func (s *SourceSet) accept(e *sourceEntry, snap *Snapshot) error {
	old := e.snap
	e.snap = snap
	names, snaps := s.loaded()
	if err := checkSources(names, snaps); err != nil {
		e.snap = old
		return err
	}
	merged, shadows, warnings := mergeSnapshots(names, snaps)
	s.merged = merged
	for _, c := range s.entries {
		c.status.Shadowed, c.status.Warnings = shadows[c.name], warnings[c.name]
	}
	return nil
}

// checkSources fails on the first conflict between two Sources, each pair with
// the earlier listed Source first, and names both.
func checkSources(names []string, snaps []*Snapshot) error {
	for i := range snaps {
		for j := i + 1; j < len(snaps); j++ {
			if err := pairConflict(names[i], snaps[i], names[j], snaps[j]); err != nil {
				return err
			}
		}
	}
	return nil
}

// pairConflict is the conflict between a, the preferred Source, and b, if any:
//   - an enforced Item that loses to the other Source's copy in the same Scope;
//   - a Suppression of one Source that names an enforced global Item of the
//     other, whether or not it names anything in its own Source;
//   - an Item at a narrower Scope in one Source over an enforced Item at a
//     wider Scope in the other (ADR 0021 across Sources).
//
// A draft does not enforce and shadows nothing. The error names Items as
// <kind>:<ID> and never their content.
func pairConflict(an string, a *Snapshot, bn string, b *Snapshot) error {
	for _, p := range scopePairs(a, b) {
		for _, list := range p.lose.lists() {
			for i := range *list {
				l := &(*list)[i]
				if w := p.win.index[l.key()]; w != nil && l.Enforced && l.served() && w.served() {
					return fmt.Errorf("sources %q and %q conflict: the enforced Item %s (%s Scope) of %q loses to %q on preference", an, bn, itemRef(l), p.scope, bn, an)
				}
			}
		}
	}
	for _, d := range [2]struct {
		sn   string
		s    *Snapshot
		on   string
		over *Snapshot
	}{{an, a, bn, b}, {bn, b, an, a}} {
		for _, key := range d.s.suppressKeys() {
			if g := d.over.index[key]; g != nil && g.Enforced && g.served() {
				return fmt.Errorf("sources %q and %q conflict: %q suppresses the enforced Item %s of %q", an, bn, d.sn, itemRef(g), d.on)
			}
		}
		if narrow, enforced := narrowOver(d.s, d.over); narrow != nil {
			return fmt.Errorf("sources %q and %q conflict: the Item %s of %q shadows the enforced Item %s of %q", an, bn, itemRef(narrow), d.sn, itemRef(enforced), d.on)
		}
	}
	return nil
}

// narrowOver returns a served Project or local Item of n that shadows a
// published enforced Item of w at a wider Scope, with that Item, or nil.
func narrowOver(n, w *Snapshot) (narrow, enforced *Article) {
	if narrow, enforced = w.shadowedEnforced(n.projectItems(), false); narrow != nil || n.local == nil {
		return narrow, enforced
	}
	return w.shadowedEnforced(n.local.items(), true)
}

// suppressKeys lists every Item a Project or the local Scope of s suppresses,
// including a reference that named nothing in s, sorted.
func (s *Snapshot) suppressKeys() []itemKey {
	set := map[itemKey]bool{}
	for _, d := range s.dangling {
		set[d.key] = true
	}
	for _, p := range s.projects {
		for k := range p.suppressed {
			set[k] = true
		}
	}
	if s.local != nil {
		for k := range s.local.suppressed {
			set[k] = true
		}
	}
	keys := make([]itemKey, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].kind+":"+keys[i].name < keys[j].kind+":"+keys[j].name })
	return keys
}

// danglingRef is a suppress reference that named nothing where it was read:
// scope is the Project's, or ScopeLocal, and warning the text it left.
type danglingRef struct {
	scope   string
	key     itemKey
	warning string
}

// scopePair is the same Scope in two Sources, the preferred one first.
type scopePair struct {
	scope     string
	win, lose *scopeItems
}

func scopePairs(win, lose *Snapshot) []scopePair {
	ps := []scopePair{{ScopeGlobal, &win.scopeItems, &lose.scopeItems}}
	for _, id := range win.Projects() {
		w, _ := win.lookupProject(id)
		if l, ok := lose.lookupProject(id); ok {
			ps = append(ps, scopePair{w.scope, &w.scopeItems, &l.scopeItems})
		}
	}
	if win.local != nil && lose.local != nil {
		ps = append(ps, scopePair{ScopeLocal, &win.local.scopeItems, &lose.local.scopeItems})
	}
	return ps
}

// lists points at every Item list of the Scope, in one fixed order.
func (h *scopeItems) lists() [5]*[]Article {
	return [5]*[]Article{&h.articles, &h.agents, &h.loops, &h.decisions, &h.instructions}
}

// mergeSnapshots builds the view over snaps, in preference order, along with
// each Source's shadowed Items and the warnings that still hold in the view:
// a suppress reference that named nothing in its own Source but names a global
// Item of another is applied, and its warning dropped.
func mergeSnapshots(names []string, snaps []*Snapshot) (*Snapshot, map[string][]Shadowed, map[string][]string) {
	m := &Snapshot{projects: map[string]project{}}
	shadows := map[string][]Shadowed{}
	globals := make([]*scopeItems, len(snaps))
	for i, s := range snaps {
		globals[i] = &s.scopeItems
	}
	m.scopeItems = mergeItems(names, globals, ScopeGlobal, shadows)

	ids := map[string]string{} // lower-cased Project id -> first spelling
	for _, s := range snaps {
		for _, id := range s.Projects() {
			if _, ok := ids[strings.ToLower(id)]; !ok {
				ids[strings.ToLower(id)] = id
			}
		}
	}
	lower := make([]string, 0, len(ids))
	for l := range ids {
		lower = append(lower, l)
	}
	sort.Strings(lower)
	for _, l := range lower {
		in := make([]*scopeItems, len(snaps))
		p := project{scope: ids[l], suppressed: map[itemKey]bool{}}
		for i, s := range snaps {
			if sp, ok := s.lookupProject(l); ok {
				in[i] = &sp.scopeItems
				for k := range sp.suppressed {
					p.suppressed[k] = true
				}
				p.suppressions = append(p.suppressions, sp.suppressions...)
			}
		}
		p.scopeItems = mergeItems(names, in, p.scope, shadows)
		p.suppressions = uniqueSorted(p.suppressions)
		m.projects[l] = p
	}

	in := make([]*scopeItems, len(snaps))
	for i, s := range snaps {
		if s.local == nil {
			continue
		}
		if m.local == nil {
			m.local = &localScope{suppressed: map[itemKey]bool{}, configFile: s.local.configFile}
		}
		in[i] = &s.local.scopeItems
		for k := range s.local.suppressed {
			m.local.suppressed[k] = true
		}
	}
	if m.local != nil {
		m.local.scopeItems = mergeItems(names, in, ScopeLocal, shadows)
	}

	warnings := map[string][]string{}
	for i, s := range snaps {
		applied := map[string]bool{}
		for _, d := range s.dangling {
			if t := m.index[d.key]; t != nil && t.served() && !t.Enforced && m.suppress(d.scope, t) {
				applied[d.warning] = true
			}
		}
		for _, w := range s.warnings {
			if !applied[w] {
				warnings[names[i]] = append(warnings[names[i]], w)
				m.warnings = append(m.warnings, w)
			}
		}
	}
	return m, shadows, warnings
}

// suppress hides the global Item t in scope, a Project's or ScopeLocal, and
// reports whether there was such a Scope.
func (m *Snapshot) suppress(scope string, t *Article) bool {
	key := t.key()
	if scope == ScopeLocal {
		if m.local == nil {
			return false
		}
		m.local.suppressed[key] = true
		return true
	}
	p, ok := m.lookupProject(scope)
	if !ok {
		return false
	}
	if !p.suppressed[key] {
		p.suppressed[key] = true
		p.suppressions = uniqueSorted(append(p.suppressions, t.Kind()+":"+t.ID))
	}
	m.projects[strings.ToLower(scope)] = p
	return true
}

func uniqueSorted(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

// mergeItems merges one Scope across Sources, in preference order; in[i] is
// Source i's Scope, or nil. The first served copy of an Item answers, and each
// later served copy is recorded as shadowed in that Source's list. A draft
// neither shadows nor is shadowed: a published copy replaces a draft that came
// first, and a draft behind a published copy is dropped without a record. The
// merged lists hold copies of the Items.
func mergeItems(names []string, in []*scopeItems, scope string, shadows map[string][]Shadowed) scopeItems {
	var out scopeItems
	type held struct {
		at int
		by string
	}
	for k, dst := range out.lists() {
		have := map[itemKey]held{}
		for i, h := range in {
			if h == nil {
				continue
			}
			for _, a := range *h.lists()[k] {
				key := a.key()
				w, ok := have[key]
				switch {
				case !ok:
					have[key] = held{len(*dst), names[i]}
					*dst = append(*dst, a)
				case !(*dst)[w.at].served():
					if a.served() {
						(*dst)[w.at], have[key] = a, held{w.at, names[i]}
					}
				case a.served():
					shadows[names[i]] = append(shadows[names[i]], Shadowed{Item: itemRef(&a), Scope: scope, By: w.by})
				}
			}
		}
	}
	out.index = keyIndex{}
	for _, l := range out.lists() {
		sortArticles(*l)
		out.index.add(*l)
	}
	for _, h := range in {
		if h == nil {
			continue
		}
		for name, b := range h.skills {
			if out.skills == nil {
				out.skills = map[string]skillBundle{}
			}
			if _, ok := out.skills[name]; !ok {
				out.skills[name] = b
			}
		}
	}
	return out
}
