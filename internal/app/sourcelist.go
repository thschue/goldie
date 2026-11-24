package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/thschue/goldie/internal/engine"
	"github.com/thschue/goldie/internal/platform"
)

// sourceList is the Sources of GOLDIE_SOURCES behind one SourceSet: the
// merged view the server serves, with a poll loop and a readiness check for
// each member. No member loads before Start, so startup never waits for a
// clone, and one failing member never stops another.
type sourceList struct {
	set     *engine.SourceSet
	members []*member
}

// member is one Source of the list.
type member struct {
	name     string
	interval time.Duration // zero follows the server's poll interval
	logAttrs []any         // the Source's name, and its URL redacted for a git Source
	pollLog  *slog.Logger  // logs every poll; set by logTo
	polls    *platform.PollCounter
}

// check is the member's readiness check.
func (m *member) check() string { return "source:" + m.name }

// serveSourceList builds the Sources cfg.Sources lists, in preference order.
// A git Source takes its secrets from the entry and its known hosts, CA bundle
// and file:// switch from the GOLDIE_GIT_ settings, which apply to every git
// Source of the list. Every problem is reported, each naming the entry.
func serveSourceList(cfg platform.Config) (source, error) {
	var errs []error
	var named []engine.NamedSource
	list := &sourceList{}
	for _, spec := range cfg.Sources {
		m := &member{name: spec.Name, interval: spec.Poll, logAttrs: []any{"source", spec.Name}}
		var src engine.Source
		switch spec.Type {
		case "git":
			var err error
			if src, err = listGitSource(cfg, spec); err != nil {
				errs = append(errs, err)
				continue
			}
			m.logAttrs = append(m.logAttrs, "url", spec.RedactedURL())
		default:
			src = engine.NewDirSource(spec.Dir)
		}
		named = append(named, engine.NamedSource{Name: spec.Name, Source: src})
		list.members = append(list.members, m)
	}
	if len(errs) > 0 {
		return source{}, errors.Join(errs...)
	}
	list.set = engine.NewSourceSet(named...)
	return source{list: list}, nil
}

// listGitSource is the git Source of one entry of the list.
func listGitSource(cfg platform.Config, spec platform.SourceSpec) (engine.Source, error) {
	keys := map[engine.GitOption]string{
		engine.GitOptionURL: "url", engine.GitOptionRef: "ref", engine.GitOptionToken: "token_env",
		engine.GitOptionSSHKey: "ssh_key_env", engine.GitOptionKnownHosts: platform.EnvGitSSHKnownHosts,
		engine.GitOptionCABundle: platform.EnvGitCABundle,
	}
	entry := fmt.Sprintf("%s: Source %q", platform.EnvSources, spec.Name)
	opts := engine.GitOptions{URL: spec.URL, Ref: spec.Ref, Path: spec.Path, Token: spec.Token, SSHKey: spec.SSHKey}
	// The shared settings go to the Sources they can apply to: the engine
	// refuses known hosts on a URL that is not ssh, a CA bundle on one that is
	// not https.
	lower := strings.ToLower(spec.URL)
	switch {
	case strings.HasPrefix(lower, "https://"):
		opts.CABundle = cfg.GitCABundle
	case strings.HasPrefix(lower, "ssh://"), !strings.Contains(lower, "://") && !strings.HasPrefix(lower, "file:"):
		opts.KnownHosts = cfg.GitSSHKnownHosts // ssh://, or scp-like
	}
	return newGitSource(opts, cfg.GitAllowFileURL, func(o engine.GitOption) string {
		if k := keys[o]; k != "" {
			return entry + " " + k
		}
		return entry + " url"
	}, entry+" path")
}

// addChecks registers one readiness check per member. It fails until the
// member has loaded once and then stays ready, so a stale Source stays ready
// and no error reaches /readyz (PC-02).
func (l *sourceList) addChecks(reg *platform.Registry) {
	for _, m := range l.members {
		reg.Add(m.check(), func(context.Context) error {
			if l.set.Status(m.name).LastSuccess.IsZero() {
				return errors.New("never loaded")
			}
			return nil
		})
	}
}

// run starts one poll loop per member, at its own interval or fallback, in
// wg, until ctx ends.
func (l *sourceList) run(ctx context.Context, wg *sync.WaitGroup, fallback time.Duration) {
	for _, m := range l.members {
		onPoll := pollHook(ctx, m.pollLog, m.polls)
		interval := max(cmp.Or(m.interval, fallback), minPollInterval)
		wg.Go(func() { l.set.Run(ctx, m.name, interval, onPoll) })
	}
}

// logTo has every member's polls logged to log, with its attributes. Nil
// safe, as the other list hooks are.
func (l *sourceList) logTo(log *slog.Logger) {
	if l == nil {
		return
	}
	for _, m := range l.members {
		m.pollLog = log.With(m.logAttrs...)
	}
}

// countPolls has every member's finished polls counted in goldie.syncs under
// its own name.
func (l *sourceList) countPolls(tel *platform.Telemetry) {
	if l == nil {
		return
	}
	for _, m := range l.members {
		m.polls = tel.PollCounter("goldie.syncs", "Source polls finished, by source and result.", m.name)
	}
}
