package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/contract/project"
	"github.com/thschue/goldie/internal/platform"
)

// How a Project was found, in the order of precedence. These are the words
// `goldie status` prints and the JSON "projectSource" carries.
const (
	sourceFlag     = "--project"
	sourceEnv      = EnvProject
	sourceConfig   = ".goldie/config.yaml"
	sourceUpstream = "git remote upstream"
	sourceOrigin   = "git remote origin"
)

// sourceWords maps what the detection reports to the words above.
var sourceWords = map[project.Source]string{
	project.SourceFlag:     sourceFlag,
	project.SourceEnv:      sourceEnv,
	project.SourceConfig:   sourceConfig,
	project.SourceUpstream: sourceUpstream,
	project.SourceOrigin:   sourceOrigin,
}

// detectProject detects the Project of dir with project.Detect. A Project the
// user gave but that is unusable is reported with the words of its source as a
// prefix.
func detectProject(dir, flagValue, envValue string) (project.Detected, error) {
	d, err := project.Detect(dir, flagValue, envValue)
	if err != nil {
		return project.Detected{}, withSourceWords(err)
	}
	return d, nil
}

// withSourceWords prefixes the error of an unusable Project with the words of
// its source, and passes any other error, or nil, on as it is.
func withSourceWords(err error) error {
	var se *project.SourceError
	if errors.As(err, &se) {
		return fmt.Errorf("%s: %w", sourceWords[se.Source], se.Err)
	}
	return err
}

// describeProject is the text of the Project line: the problem when there is
// one, else the Project or what was found.
func describeProject(p project.Detected, problem string) string {
	if problem != "" {
		return "problem: " + problem
	}
	if p.ID == "" && p.Rejected != "" {
		return remoteHint(p)
	}
	if p.ID == "" {
		return "none found (set --project, " + EnvProject + " or .goldie/config.yaml, or run inside a git repository with an upstream or origin remote)"
	}
	return fmt.Sprintf("%s (from %s)", contract.CleanLine(p.ID), sourceWords[p.Source])
}

// project detects the Project for the working directory.
func (c *commander) project() (project.Detected, error) {
	return workingProject(c.env.Lookup, c.s.project)
}

// workingProject detects the Project of the working directory from flagValue,
// then GOLDIE_PROJECT from lookup, then the checkout.
func workingProject(lookup platform.Lookup, flagValue string) (project.Detected, error) {
	dir, err := os.Getwd()
	if err != nil {
		return project.Detected{}, errors.New("cannot read the working directory")
	}
	return projectIn(dir, lookup, flagValue)
}

// projectIn detects the Project of dir from flagValue, then GOLDIE_PROJECT
// from lookup, then the checkout. goldie init uses it with the directory it
// also finds the repository root from.
func projectIn(dir string, lookup platform.Lookup, flagValue string) (project.Detected, error) {
	envValue, _ := lookup(EnvProject)
	return detectProject(dir, flagValue, envValue)
}

// SessionProject is the Project of a session in the words of goldie status:
// plain strings, for a role that hands them on without Project detection.
type SessionProject struct {
	ID       string // host/org/repo, or "" when there is none
	Source   string // where ID came from, as status words it
	Rejected string // without an ID: the name of a remote whose URL is not host/org/repo
	Problem  string // the Project problem as status words it, or ""
}

// ProjectWords words what project.Detect returned, d and err, as goldie
// status does: the source in status's words, or the problem of its Project
// line, with an unusable Project's source as a prefix. It does no I/O and
// detects nothing; the caller detects. Nothing returned holds a path, a
// remote URL or file content, because project.Detect's errors hold none.
func ProjectWords(d project.Detected, err error) SessionProject {
	if err = withSourceWords(err); err != nil {
		return SessionProject{Problem: contract.CleanLine(err.Error())}
	}
	return SessionProject{ID: d.ID, Source: sourceWords[d.Source], Rejected: d.Rejected}
}

// projectHint is the text of the rejected-remote hint, or "" when no remote was
// rejected.
func projectHint(p project.Detected) string {
	if p.ID != "" || p.Rejected == "" {
		return ""
	}
	return remoteHint(p)
}

func remoteHint(p project.Detected) string {
	return "found a git remote " + contract.CleanLine(p.Rejected) + ", but its URL is not in the accepted host/org/repo form (set --project, " + EnvProject + " or .goldie/config.yaml)"
}

// projectParam is the project= query value: the Project, or nil when none was found.
// The ID is already lower-cased and valid (project.Detect).
func projectParam(p project.Detected) *contract.ProjectId {
	if p.ID == "" {
		return nil
	}
	id := p.ID
	return &id
}
