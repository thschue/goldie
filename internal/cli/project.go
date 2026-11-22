package cli

import (
	"errors"
	"fmt"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/contract/project"
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

// detectedProject is a detected or configured Project and where it came from,
// in the words of the user.
type detectedProject struct {
	ID     string // host/org/repo, or the value given by the user
	Source string // one of the source constants

	// Rejected names the remote that exists but is not in the host/org/repo
	// form (the name only, never the URL).
	Rejected string
}

// resolveProject detects the Project of dir with project.Resolve and words
// where it came from. A Project the user gave but that is unusable is
// reported with the words of its source as a prefix.
func resolveProject(dir, flagValue, envValue string) (detectedProject, error) {
	d, err := project.Resolve(dir, flagValue, envValue)
	var se *project.SourceError
	if errors.As(err, &se) {
		return detectedProject{}, fmt.Errorf("%s: %w", sourceWords[se.Source], se.Err)
	}
	if err != nil {
		return detectedProject{}, err
	}
	return detectedProject{ID: d.ID, Source: sourceWords[d.Source], Rejected: d.Rejected}, nil
}

// describe is the status line for a Project.
func (p detectedProject) describe() string {
	if p.ID == "" && p.Rejected != "" {
		return p.remoteHint()
	}
	if p.ID == "" {
		return "none found (set --project, " + EnvProject + " or .goldie/config.yaml, or run inside a git repository with an upstream or origin remote)"
	}
	return fmt.Sprintf("%s (from %s)", contract.CleanLine(p.ID), p.Source)
}

// hint is the text of the rejected-remote hint, or "" when no remote was
// rejected.
func (p detectedProject) hint() string {
	if p.ID != "" || p.Rejected == "" {
		return ""
	}
	return p.remoteHint()
}

func (p detectedProject) remoteHint() string {
	return "found a git remote " + contract.CleanLine(p.Rejected) + ", but its URL is not in the accepted host/org/repo form (set --project, " + EnvProject + " or .goldie/config.yaml)"
}

// param is the project= query value: the Project, or nil when none was found.
// The ID is already lower-cased and valid (project.Resolve).
func (p detectedProject) param() *contract.ProjectId {
	if p.ID == "" {
		return nil
	}
	id := p.ID
	return &id
}
