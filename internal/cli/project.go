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

// resolveProject detects the Project of dir with project.Detect. A Project the
// user gave but that is unusable is reported with the words of its source as a
// prefix.
func resolveProject(dir, flagValue, envValue string) (project.Detected, error) {
	d, err := project.Detect(dir, flagValue, envValue)
	var se *project.SourceError
	if errors.As(err, &se) {
		return project.Detected{}, fmt.Errorf("%s: %w", sourceWords[se.Source], se.Err)
	}
	return d, err
}

// describe is the status line for a Project.
func describe(p project.Detected) string {
	if p.ID == "" && p.Rejected != "" {
		return remoteHint(p)
	}
	if p.ID == "" {
		return "none found (set --project, " + EnvProject + " or .goldie/config.yaml, or run inside a git repository with an upstream or origin remote)"
	}
	return fmt.Sprintf("%s (from %s)", contract.CleanLine(p.ID), sourceWords[p.Source])
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
