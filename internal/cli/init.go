package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/contract/project"
)

const (
	errInitNoRepo    = "not inside a git repository: goldie init writes .goldie/config.yaml at the repository root\nRun it in your repository, or create one first: git init"
	errInitNoProject = "no Project found: set --project or " + EnvProject + ", or add an upstream or origin remote\nName it: goldie init --project github.com/<org>/<repo>"
	errInitSource    = "this is a Source repository (global/articles or projects/ at its root): goldie init is for a project repository"
)

// initRefusal words a refusal of project.WriteConfig for goldie init, or
// returns "" for any other error.
func initRefusal(err error) string {
	switch {
	case errors.Is(err, project.ErrConfigExists):
		return ".goldie/config.yaml already exists at the repository root; goldie init never overwrites it"
	case errors.Is(err, project.ErrSymlink):
		return ".goldie at the repository root is a symlink; goldie init never follows it"
	case errors.Is(err, project.ErrNotRepository):
		return errInitNoRepo
	case errors.Is(err, project.ErrSourceRepository):
		return errInitSource
	}
	return ""
}

// initProject writes .goldie/config.yaml at the repository root with the
// Project found as status finds it, then says whether the server holds it. A
// refusal prints nothing and writes nothing.
func (c *commander) initProject(ctx context.Context) error {
	dir, err := os.Getwd()
	if err != nil {
		return errors.New("cannot read the working directory")
	}
	root, ok := project.RepositoryRoot(dir)
	if !ok {
		return errors.New(errInitNoRepo)
	}
	if project.IsSourceRepository(root) { // before detection: a Source needs no Project
		return errors.New(errInitSource)
	}
	proj, err := projectIn(dir, c.env.Lookup, c.s.project)
	if err != nil {
		return err
	}
	if h := projectHint(proj); h != "" {
		return errors.New(h)
	}
	if proj.ID == "" {
		return errors.New(errInitNoProject)
	}
	if err := project.WriteConfig(root, proj.ID); err != nil {
		if msg := initRefusal(err); msg != "" {
			return errors.New(msg)
		}
		return fmt.Errorf("cannot write .goldie/config.yaml at the repository root: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Project: %s\n", describeProject(proj, ""))
	fmt.Fprintf(&b, "Wrote: %s\n", contract.CleanLine(filepath.Join(root, ".goldie", "config.yaml")))
	registered, why := c.registered(ctx, proj.ID)
	switch {
	case why != "":
		fmt.Fprintf(&b, "Registered: not checked (%s)\n", why)
	case registered:
		b.WriteString("Registered: yes\n")
	default:
		b.WriteString("Registered: no\n")
		// The same copy as unregisteredHint in internal/api/read.go and the
		// hint example in internal/contract/openapi.yaml: keep them in step.
		fmt.Fprintf(&b, "Hint: To register it, add a projects/%s/ directory to one of goldie's Sources, usually through the platform team.\n", proj.ID)
	}
	_, err = fmt.Fprint(c.out, b.String())
	return err
}

// registered asks the server, through GET /api/v1/projects, whether it holds
// id, ignoring case (ADR 0020). why says why it could not ask, and is empty
// when it did. Without an address or a token it sends no request.
func (c *commander) registered(ctx context.Context, id string) (ok bool, why string) {
	cl, err := c.client(true)
	if err != nil {
		return false, err.Error()
	}
	// A 401 is "token rejected"; contract.Answer reads every other answer, as
	// probeToken does for status.
	raw, err := cl.ListProjects(ctx)
	if err == nil && raw.StatusCode == http.StatusUnauthorized {
		_ = raw.Body.Close()
		return false, "token rejected"
	}
	list, err := contract.Answer(raw, err, contract.ParseListProjectsResponse, projectList)
	if err != nil {
		return false, contract.CleanLine(wordError(err, c.local).Error())
	}
	for _, p := range list.Projects {
		if strings.EqualFold(p, id) {
			return true, ""
		}
	}
	return false, ""
}

func projectList(r *contract.ListProjectsResponse) *contract.ProjectList { return r.JSON200 }
