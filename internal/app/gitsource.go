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
		return source{syncer: syncer, check: sourceGitCheck, name: "git", logAttrs: []any{"source", "git", "url", logURL(cfg)}}, err
	case cfg.ContentDir != "":
		syncer, err := content(cfg.ContentDir)
		return source{syncer: syncer, check: sourceDirCheck, name: "dir"}, err
	}
	if len(cfg.Sources) > 0 {
		return source{}, fmt.Errorf("%s is valid, but this version cannot serve a list of Sources yet: set %s or %s instead", platform.EnvSources, platform.EnvContentDir, platform.EnvGitURL)
	}
	return source{}, fmt.Errorf("%s or %s is not set: the server needs one Source", platform.EnvContentDir, platform.EnvGitURL)
}

// logURL is the URL a poll record carries: platform's redacted form, except
// that a file:// URL, whose rest is a path on this host, shows only the scheme.
func logURL(cfg platform.Config) string {
	if scheme, _, ok := strings.Cut(cfg.GitURL, ":"); ok && strings.EqualFold(scheme, "file") {
		return "file://(path)"
	}
	return cfg.RedactedGitURL()
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
	var errs []error
	if scheme, _, ok := strings.Cut(cfg.GitURL, ":"); ok && strings.EqualFold(scheme, "file") && !cfg.GitAllowFileURL {
		errs = append(errs, fmt.Errorf("%s is a file:// URL, which is refused unless %s is true", platform.EnvGitURL, platform.EnvGitAllowFileURL))
	}
	src, err := engine.NewGitSource(engine.GitOptions{
		URL: cfg.GitURL, Ref: cfg.GitRef, Path: cfg.GitPath, Token: cfg.GitToken,
		SSHKey: cfg.GitSSHKey, KnownHosts: cfg.GitSSHKnownHosts, CABundle: cfg.GitCABundle,
	})
	if err != nil {
		name := platform.EnvGitURL
		var oe *engine.GitOptionError
		if errors.As(err, &oe) && gitOptionVars[oe.Option] != "" {
			name = gitOptionVars[oe.Option]
		}
		errs = append(errs, fmt.Errorf("%s: %w", name, err))
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return engine.NewSyncer(gitPathSource{src}), nil
}

// gitPathSource names GOLDIE_GIT_PATH in the engine's path error, which the
// engine words without a setting name. Revision comes from the embedded Source.
type gitPathSource struct{ *engine.GitSource }

func (s gitPathSource) Load(ctx context.Context) (*engine.Snapshot, error) {
	snap, err := s.GitSource.Load(ctx)
	if errors.Is(err, engine.ErrGitPath) {
		return nil, fmt.Errorf("git source: %s must be a relative directory of the repository at the served ref, without .. elements", platform.EnvGitPath)
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
