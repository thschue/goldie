package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/contract/project"
)

const (
	errInitNoRepo    = "not inside a git repository: goldie init writes .goldie/config.yaml at the repository root\nRun it in your repository, or create one first: git init"
	errInitName      = "Name it: goldie init --project github.com/<org>/<repo>"
	errInitNoProject = "no Project found: set --project or " + EnvProject + ", or add an upstream or origin remote\n" + errInitName
	errInitSource    = "this is a Source repository (global/articles or projects/ at its root): goldie init is for a project repository"
	errInitStopped   = "init stopped; nothing more was changed"
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

// initDetect finds the repository root and the Project as init needs them and
// refuses, before anything is printed or read, where init cannot go on: no
// repository, a Source repository, or no Project.
func (c *commander) initDetect() (root string, proj project.Detected, err error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", proj, errors.New("cannot read the working directory")
	}
	root, ok := project.RepositoryRoot(dir)
	if !ok {
		return "", proj, errors.New(errInitNoRepo)
	}
	if project.IsSourceRepository(root) { // before detection: a Source needs no Project
		return "", proj, errors.New(errInitSource)
	}
	proj, err = projectIn(dir, c.env.Lookup, c.s.project)
	if err != nil {
		return "", proj, err
	}
	if h := projectHint(proj); h != "" {
		return "", proj, errors.New(h)
	}
	if proj.ID == "" {
		return "", proj, errors.New(errInitNoProject)
	}
	return root, proj, nil
}

// initPreflight refuses what project.WriteConfig would refuse for the state of
// .goldie, so a guided run does not ask its questions for nothing.
// WriteConfig still decides when it writes.
func initPreflight(root string) error {
	g, err := os.Lstat(filepath.Join(root, ".goldie"))
	switch {
	case err != nil:
		return nil
	case g.Mode()&os.ModeSymlink != 0:
		return errors.New(initRefusal(project.ErrSymlink))
	}
	if _, err := os.Lstat(filepath.Join(root, ".goldie", "config.yaml")); err == nil {
		return errors.New(initRefusal(project.ErrConfigExists))
	}
	return nil
}

// writeConfig writes .goldie/config.yaml, wording a refusal for init.
func writeConfig(root, id string) error {
	if err := project.WriteConfig(root, id); err != nil {
		if msg := initRefusal(err); msg != "" {
			return errors.New(msg)
		}
		return fmt.Errorf("cannot write .goldie/config.yaml at the repository root: %w", err)
	}
	return nil
}

// initProject writes .goldie/config.yaml at the repository root with the
// Project found as status finds it, then says whether the server holds it. A
// refusal prints nothing and writes nothing. With a terminal (Env.In) it
// guides the user instead: initGuided.
func (c *commander) initProject(ctx context.Context) error {
	root, proj, err := c.initDetect()
	if err != nil {
		return err
	}
	if c.env.In != nil {
		if err := initPreflight(root); err != nil {
			return err
		}
		return c.initGuided(ctx, root, proj)
	}
	return c.initPlain(ctx, root, proj, true)
}

// initPlain is init without questions: the Project line (when withProject),
// the file it wrote, and whether the server holds the Project.
func (c *commander) initPlain(ctx context.Context, root string, proj project.Detected, withProject bool) error {
	if err := writeConfig(root, proj.ID); err != nil {
		return err
	}
	var b strings.Builder
	if withProject {
		say(&b, "Project: %s\n", describeProject(proj, ""))
	}
	b.WriteString(wroteLine(root))
	list, why := c.listProjects(ctx)
	b.WriteString(registeredLines(proj.ID, list, why, false))
	_, err := fmt.Fprint(c.out, b.String())
	return err
}

func wroteLine(root string) string {
	return fmt.Sprintf("Wrote: %s\n", contract.CleanLine(filepath.Join(root, ".goldie", "config.yaml")))
}

// registeredLines words the Registered line, and the hint when the Project is
// not there. why says why the server could not say. loopback says the server
// is on this machine, where the way to register is the content directory of
// goldie server --dev.
func registeredLines(id string, list []string, why string, loopback bool) string {
	if why != "" {
		return fmt.Sprintf("Registered: not checked (%s)\n", why)
	}
	for _, p := range list {
		if strings.EqualFold(p, id) { // ADR 0020
			return "Registered: yes\n"
		}
	}
	if loopback {
		return fmt.Sprintf("Registered: no\nHint: To register it, add a projects/%s/ directory to the content directory and restart goldie server --dev\n", id)
	}
	// The same copy as unregisteredHint in internal/api/read.go and the
	// hint example in internal/contract/openapi.yaml: keep them in step.
	return fmt.Sprintf("Registered: no\nHint: To register it, add a projects/%s/ directory to one of goldie's Sources, usually through the platform team.\n", id)
}

// initGuided is init on a terminal: it shows the Project and asks to use it,
// writes the config, asks for the server address, probes the server and the
// token once, and prints the commands to run next. Questions and the Server
// and Token lines go to stderr, results to stdout. A run whose input ends at
// the first question is initPlain. A later end of input stops it; what was
// written stays.
func (c *commander) initGuided(ctx context.Context, root string, proj project.Detected) error {
	p := newPrompter(c.env.In, c.errOut)
	say(c.out, "Project: %s\n", describeProject(proj, ""))
	use, err := p.confirm("Use this Project?")
	switch {
	case errors.Is(err, errPromptEOF):
		return c.initPlain(ctx, root, proj, false)
	case err != nil:
		return err
	case !use:
		return errors.New("no Project chosen\n" + errInitName)
	}
	if err := writeConfig(root, proj.ID); err != nil {
		return err
	}
	say(c.out, "%s", wroteLine(root))

	addr, err := c.askAddress(p)
	if err != nil {
		return err
	}
	c.s.addr = addr
	list, why := c.reportServer(ctx)
	say(c.out, "%s", registeredLines(proj.ID, list, why, isLoopback(addr)))
	say(c.out, "%s", c.nextBlock(addr))
	return nil
}

// askAddress asks for the server address; the default is the one --addr or
// GOLDIE_ADDR gave. It returns "" for no server, and says so on stderr.
func (c *commander) askAddress(p *prompter) (string, error) {
	q := "goldie server address: "
	if c.s.addr != "" {
		q = fmt.Sprintf("goldie server address [%s]: ", contract.CleanLine(c.s.addr))
	}
	a, err := p.ask(q)
	if err != nil {
		return "", errors.New(errInitStopped)
	}
	if a == "" {
		a = c.s.addr
	}
	if a == "" {
		say(c.errOut, "Server: not set\nHint: start one on this machine with goldie server --dev\n")
		return "", nil
	}
	return strings.TrimRight(a, "/"), nil
}

// reportServer probes the server at c.s.addr and the token, once each, and
// prints the Server and Token lines on stderr. It returns what the project
// list said for Registered; why is set when it said nothing.
func (c *commander) reportServer(ctx context.Context) (list []string, why string) {
	given := c.s.token != ""
	if c.s.addr == "" {
		c.tokenLine(given, "not checked: no server")
		return nil, noAddressInit
	}
	cl, err := c.client(given)
	if err != nil { // an address CheckBaseURL refuses
		say(c.errOut, "Server: %s (not usable: %s)\n", contract.CleanLine(c.s.addr), contract.CleanLine(err.Error()))
		c.tokenLine(given, "not checked: no usable server")
		return nil, "no usable server address"
	}
	probe := probeServer(ctx, cl, given)
	if probe.err != nil {
		say(c.errOut, "Server: %s (not reachable: %s)\n", contract.CleanLine(c.s.addr), contract.CleanLine(wordError(probe.err, false).Error()))
		c.tokenLine(given, "not checked: server not reachable")
		return nil, "server not reachable"
	}
	say(c.errOut, "Server: %s (reachable)\n", contract.CleanLine(c.s.addr))
	if !given {
		c.tokenLine(false, "")
		return nil, "no token: set --token, " + EnvToken + " or " + EnvToken + "_FILE"
	}
	if probe.tokenState == tokenRejected {
		c.tokenLine(true, "rejected")
		return nil, "token rejected"
	}
	list, why = c.listProjects(ctx)
	switch why {
	case "":
		c.tokenLine(true, "accepted")
	case tokenRejectedWhy:
		c.tokenLine(true, "rejected")
	default:
		c.tokenLine(true, "not checked: "+why)
	}
	return list, why
}

// tokenLine prints the Token line on stderr. It never prints the token, only
// which setting gave it.
func (c *commander) tokenLine(given bool, state string) {
	switch {
	case !given:
		say(c.errOut, "Token: not set\nHint: goldie server --dev prints one; otherwise ask your platform team\n")
	case state == "rejected":
		say(c.errOut, "Token: rejected\n")
	case state == "accepted":
		say(c.errOut, "Token: accepted (from %s)\n", c.tokenSource())
	default:
		say(c.errOut, "Token: set (%s)\n", state)
	}
}

// tokenSource names the setting the token came from.
func (c *commander) tokenSource() string {
	if _, ok := c.env.Lookup(EnvToken); ok {
		return EnvToken
	}
	if _, ok := c.env.Lookup(EnvToken + "_FILE"); ok {
		return EnvToken + "_FILE"
	}
	return "--token"
}

// nextBlock is the commands to run next, one per line. Please keep in step with
// the checklist of docs/design/ui-handoff.md §3.2 item 4 (the same commands,
// with this server's origin) and with the README quickstart.
func (c *commander) nextBlock(addr string) string {
	if addr == "" {
		addr = "<server address>"
	}
	addr = contract.CleanLine(addr)
	var b strings.Builder
	b.WriteString("\nNext:\n")
	say(&b, "export %s=%s\n", EnvAddr, addr)
	if c.s.token == "" {
		say(&b, "export %s=<your token>\n", EnvToken)
	}
	say(&b, "claude mcp add --transport http goldie %s/mcp --header \"Authorization: Bearer $%s\"\n", addr, EnvToken)
	b.WriteString("goldie instructions apply\n")
	b.WriteString("goldie status\n")
	b.WriteString("git add .goldie/config.yaml && git commit -m \"Add goldie config\"\n")
	return b.String()
}

// isLoopback reports whether addr is a server on this machine.
func isLoopback(addr string) bool {
	u, err := url.Parse(addr)
	if err != nil {
		return false
	}
	h := u.Hostname()
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(h)
	return err == nil && ip.IsLoopback()
}

const tokenRejectedWhy = "token rejected"

// listProjects asks the server, through GET /api/v1/projects, which Projects
// it holds. why says why it could not ask or read the answer (tokenRejectedWhy
// for a 401), and is empty when it did. Without an address or a token it sends
// no request.
func (c *commander) listProjects(ctx context.Context) (list []string, why string) {
	cl, err := c.client(true)
	if err != nil {
		if errors.Is(err, ErrNoAddress) {
			return nil, noAddressInit
		}
		return nil, contract.CleanLine(err.Error())
	}
	// A 401 is "token rejected"; contract.Answer reads every other answer, as
	// probeToken does for status.
	raw, err := cl.ListProjects(ctx)
	if err == nil && raw.StatusCode == http.StatusUnauthorized {
		_ = raw.Body.Close()
		return nil, tokenRejectedWhy
	}
	res, err := contract.Answer(raw, err, contract.ParseListProjectsResponse, projectList)
	if err != nil {
		return nil, contract.CleanLine(wordError(err, c.local).Error())
	}
	return res.Projects, ""
}

func projectList(r *contract.ListProjectsResponse) *contract.ProjectList { return r.JSON200 }
