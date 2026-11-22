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

// initRefusals words the refusals of project.WriteConfig for goldie init.
var initRefusals = map[error]string{
	project.ErrConfigExists:     ".goldie/config.yaml already exists at the repository root; goldie init never overwrites it",
	project.ErrSymlink:          ".goldie at the repository root is a symlink; goldie init never follows it",
	project.ErrNotRepository:    errInitNoRepo,
	project.ErrSourceRepository: "this is a Source repository (global/articles or projects/ at its root): goldie init is for a project repository",
}

const errInitNoRepo = "not inside a git repository: goldie init writes .goldie/config.yaml at the repository root"

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
	proj, err := c.projectIn(dir)
	if err != nil {
		return err
	}
	if proj.ID == "" && proj.Rejected != "" {
		return errors.New(remoteHint(proj))
	}
	if proj.ID == "" {
		return errors.New("no Project found: set --project or " + EnvProject + ", or add an upstream or origin remote")
	}
	if err := project.WriteConfig(root, proj.ID); err != nil {
		for refusal, msg := range initRefusals {
			if errors.Is(err, refusal) {
				return errors.New(msg)
			}
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
		// The same hint the API gives for an unregistered Project.
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
	raw, err := cl.ListProjects(ctx)
	if err != nil {
		return false, transportError(err).Error()
	}
	if raw.StatusCode == http.StatusUnauthorized {
		_ = raw.Body.Close()
		return false, "token rejected"
	}
	resp, e, err := contract.ParseOK(raw, contract.ParseListProjectsResponse)
	if err := answerError(e, err); err != nil {
		return false, contract.CleanLine(err.Error())
	}
	if resp.JSON200 == nil {
		return false, contract.CleanLine(apiError(resp.HTTPResponse, resp.Body).Error())
	}
	for _, p := range resp.JSON200.Projects {
		if strings.EqualFold(p, id) {
			return true, ""
		}
	}
	return false, ""
}
