package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/thschue/goldie/internal/engine"
	"github.com/thschue/goldie/internal/platform"
)

// minPollInterval is the floor newPublic applies to GOLDIE_POLL_INTERVAL, so a
// Config built by hand never busy-loops. It is platform's floor, which only
// the process tests' binary lowers; a variable so tests can shorten it.
var minPollInterval = platform.PollIntervalFloor()

// The readiness checks of the Sources (PC-02).
const (
	sourceDirCheck = "source:dir"
	sourceGitCheck = "source:git"
)

// source is the one Source a Public serves.
type source struct {
	syncer *engine.Syncer
	check  string // its readiness check
	name   string // its name in metrics: "dir" or "git"
	// logAttrs, when not nil, has Start log every poll with them.
	logAttrs []any
	// list, when not nil, is the list of GOLDIE_SOURCES: syncer is nil, and
	// every member has its own poll loop and readiness check (sourcelist.go).
	list *sourceList
	// firstLoadHint, when set, is logged once, after the reason, when the
	// Source's first load fails.
	firstLoadHint string
}

// current is the Snapshot the server serves, nil before the first load.
func (s source) current() *engine.Snapshot {
	if s.list != nil {
		return s.list.set.Current()
	}
	return s.syncer.Current()
}

// serveSource builds the one Source cfg names: the dir Source, loaded here so
// unloadable content fails startup, or the git Source, which Start loads, so
// startup never waits for a clone. Errors name the variables to fix and never
// a value.
func serveSource(cfg platform.Config) (source, error) {
	if err := platform.CheckOneSource(cfg.ContentDir, cfg.GitURL, len(cfg.Sources) > 0); err != nil {
		return source{}, err
	}
	switch {
	case cfg.GitURL != "":
		syncer, err := gitSource(cfg)
		// The URL is logged here and nowhere else, redacted by platform.
		return source{syncer: syncer, check: sourceGitCheck, name: "git", logAttrs: []any{"source", "git", "url", cfg.RedactedGitURL()}}, err
	case cfg.ContentDir != "":
		syncer, err := content(cfg.ContentDir)
		return source{syncer: syncer, check: sourceDirCheck, name: "dir"}, err
	}
	if len(cfg.Sources) > 0 {
		return serveSourceList(cfg)
	}
	return source{}, fmt.Errorf("%s or %s is not set: the server needs one Source", platform.EnvContentDir, platform.EnvGitURL)
}

// gitOptionVars names the setting behind each git Source option.
var gitOptionVars = map[engine.GitOption]string{
	engine.GitOptionURL:        platform.EnvGitURL,
	engine.GitOptionRef:        platform.EnvGitRef,
	engine.GitOptionToken:      platform.EnvGitToken,
	engine.GitOptionSSHKey:     platform.EnvGitSSHKey,
	engine.GitOptionKnownHosts: platform.EnvGitSSHKnownHosts,
	engine.GitOptionCABundle:   platform.EnvGitCABundle,
}

// gitSource puts the git Source the GOLDIE_GIT_ settings describe behind a
// Syncer, without loading it. A file:// URL needs GOLDIE_GIT_ALLOW_FILE_URL
// (ADR 0023); the engine refuses every URL form but https, ssh, scp-like and
// file, and a URL or ref starting with "-". Every problem is reported, each
// naming its variable; the engine's messages hold no value.
func gitSource(cfg platform.Config) (*engine.Syncer, error) {
	src, err := newGitSource(engine.GitOptions{
		URL: cfg.GitURL, Ref: cfg.GitRef, Path: cfg.GitPath, Token: cfg.GitToken,
		SSHKey: cfg.GitSSHKey, KnownHosts: cfg.GitSSHKnownHosts, CABundle: cfg.GitCABundle,
	}, cfg.GitAllowFileURL, func(o engine.GitOption) string {
		if v := gitOptionVars[o]; v != "" {
			return v
		}
		return platform.EnvGitURL
	}, platform.EnvGitPath)
	if err != nil {
		return nil, err
	}
	return engine.NewSyncer(src), nil
}

// newGitSource builds the git Source opts describes, refusing a file:// URL
// unless allowFile. name says which setting an option came from, for the
// errors, and pathName which one is the path.
func newGitSource(opts engine.GitOptions, allowFile bool, name func(engine.GitOption) string, pathName string) (engine.Source, error) {
	var errs []error
	if scheme, _, ok := strings.Cut(opts.URL, ":"); ok && strings.EqualFold(scheme, "file") && !allowFile {
		errs = append(errs, fmt.Errorf("%s is a file:// URL, which is refused unless %s is true", name(engine.GitOptionURL), platform.EnvGitAllowFileURL))
	}
	src, err := engine.NewGitSource(opts)
	if err != nil {
		var oe *engine.GitOptionError
		n := name(engine.GitOptionURL)
		if errors.As(err, &oe) {
			n = name(oe.Option)
		}
		errs = append(errs, fmt.Errorf("%s: %w", n, err))
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return gitPathSource{src, pathName}, nil
}

// gitPathSource names the path setting in the engine's path error, which the
// engine words without a setting name. Revision comes from the embedded Source.
type gitPathSource struct {
	*engine.GitSource
	pathName string
}

func (s gitPathSource) Load(ctx context.Context) (*engine.Snapshot, error) {
	snap, err := s.GitSource.Load(ctx)
	if errors.Is(err, engine.ErrGitPath) {
		return nil, fmt.Errorf("git source: %s must be a relative directory of the repository at the served ref, without .. elements", s.pathName)
	}
	return snap, err
}

// logPolls is the poll loop's onPoll for a Source whose polls are logged: one
// warn for each failed poll, with the Syncer's redacted error, and one info
// for each new revision, with its short SHA. A poll that ends with ctx, the
// server stopping, is no failure and logs nothing.
func logPolls(ctx context.Context, log *slog.Logger) func(engine.SyncStatus) {
	var rev string
	return func(st engine.SyncStatus) {
		switch {
		case ctx.Err() != nil:
		case st.LastError != "" && rev == "":
			log.Warn("source poll failed; no content has loaded yet and the server is not ready", "error", st.LastError)
		case st.LastError != "":
			log.Warn("source poll failed; the last good content is still served", "error", st.LastError)
		case st.Revision != rev:
			rev = st.Revision
			log.Info("source loaded a new revision", "revision", shortSHA(rev))
		}
	}
}

// shortSHA is the first seven characters of a commit SHA, as git abbreviates
// it.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
