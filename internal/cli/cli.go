package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/contract/project"
	"github.com/thschue/goldie/internal/platform"
)

// Environment variables of the client commands. The token is a secret, so it
// also has a _FILE form (PC-05). They are registered in
// platform.ClientSettings, which the configuration reference is rendered from.
const (
	EnvAddr  = platform.EnvAddr
	EnvToken = platform.EnvToken
	// EnvDir names a directory of content to serve from an embedded server.
	EnvDir = platform.EnvDir
	// EnvProject names the Project, overriding detection from git remotes.
	EnvProject = platform.EnvProject
)

// TokenNote goes in the help of the commands that take --token.
const TokenNote = "Prefer " + EnvToken + " or " + EnvToken + "_FILE to --token: a --token value shows in the process list (ps) of the machine."

// Commands lists the client commands in help order.
var Commands = []string{"search", "lookup", "skills", "agents", "loops", "decisions", "status", "init", "instructions", "pull", "version"}

// isCommand reports whether name is one of Commands.
func isCommand(name string) bool { return slices.Contains(Commands, name) }

// UsageError is a command used wrongly; the dispatcher exits 2 for it.
type UsageError struct {
	Command string // the command that was misused
	Msg     string // the reason only: the dispatcher adds the program name
}

func (e *UsageError) Error() string { return e.Msg }

// Env is what a command reads from its surroundings, so tests need no globals.
type Env struct {
	Lookup   platform.Lookup              // environment variables
	ReadFile func(string) ([]byte, error) // reads a _FILE secret
	Out      io.Writer                    // results
	Err      io.Writer                    // warnings, such as a deprecated article's; nil discards them
	Client   *http.Client                 // nil: contract's client, with a timeout
	// StartLocal starts an embedded server over the content directory content,
	// which the CLI opened and closes when Open's stop runs; nil is empty
	// content. It returns the server's address and token. stop ends it. cmd
	// injects app.StartLocal; nil means local mode is not available.
	StartLocal func(ctx context.Context, content *os.Root) (addr, token string, stop func(), err error)
	// StartLocalScope is StartLocal for a repository with a local Scope:
	// goldieDir is the absolute path of its .goldie directory, and content the
	// content directory, nil for none. cmd injects it from app.StartLocal.
	StartLocalScope func(ctx context.Context, content *os.Root, goldieDir string) (addr, token string, stop func(), err error)
}

// DefaultEnv is the process environment.
func DefaultEnv(out io.Writer) Env {
	return Env{Lookup: os.LookupEnv, ReadFile: os.ReadFile, Out: out, Err: os.Stderr}
}

// settings are the flags and variables common to every command.
type settings struct {
	addr, token, project string
	dir                  string // content directory of local mode
	target               string // instructions: claude or agents
	force                bool   // instructions apply: replace a hand-edited block
	dryRun, check        bool   // pull: list what would change; fail when stale
	file                 string // skills show: the file of the skill to print
	fileSet              bool   // --file was given, even empty
	json                 bool
}

// Command registers the flags of the client command name (an isCommand) on fs
// and returns what runs it once fs is parsed, which cmd/goldie does.
func Command(name string, fs *flag.FlagSet) func(ctx context.Context, pos []string, env Env) error {
	f := BindClientFlags(fs)
	if name != "version" { // version has no use for a Project
		fs.StringVar(&f.s.project, "project", "", "Project as host/org/repo")
	}
	if name == "pull" {
		fs.StringVar(&f.s.target, "target", "claude", "layout to write: claude (.claude/skills and .claude/agents)")
		fs.BoolVar(&f.s.dryRun, "dry-run", false, "list the files a pull would write or remove, and write nothing")
		fs.BoolVar(&f.s.check, "check", false, "exit non-zero when the namespace was never pulled or is stale, and write nothing")
	}
	if name == "instructions" {
		fs.StringVar(&f.s.target, "target", "claude", "file to manage: claude (CLAUDE.md) or agents (AGENTS.md)")
		fs.BoolVar(&f.s.force, "force", false, "apply: replace a block that was edited by hand and discard those edits")
	}
	if name == "skills" { // only skills have files to read
		fs.Func("file", "show: print one file of the skill to stdout, as its raw bytes (a `path` from its file list)", func(v string) error {
			f.s.file, f.s.fileSet = v, true
			return nil
		})
	}
	if name != "init" && name != "instructions" && name != "pull" { // init prints three fixed lines; instructions and pull have no JSON
		fs.BoolVar(&f.s.json, "json", false, "print JSON")
	}
	return func(ctx context.Context, pos []string, env Env) error {
		return f.run(ctx, name, pos, env)
	}
}

func (f *ClientFlags) run(ctx context.Context, name string, pos []string, env Env) error {
	var err error
	// skills and agents check their words before any server or token is met.
	var verb, itemName string
	if name == "skills" || name == "agents" || name == "loops" || name == "decisions" {
		if verb, itemName, err = catalogUsage(name, pos); err != nil {
			return err
		}
		if err = f.s.checkFileUsage(name, verb); err != nil {
			return err
		}
	}
	// version needs no token, and must keep working where the token settings
	// are absent or broken.
	// init checks registration against a running server only: it writes into
	// the repository and has no use for an embedded server (#282 is silent on
	// local mode), so like version it ignores --dir and GOLDIE_DIR.
	var c *commander
	if name == "instructions" { // usage errors come before any server step
		if err := f.s.checkInstructionsUsage(pos); err != nil {
			return err
		}
	}
	if name == "pull" {
		if err := f.s.checkPullUsage(pos); err != nil {
			return err
		}
	}
	switch name {
	case "version":
		c, err = f.commander(env, tokenOptional)
	case "init":
		c, err = f.commander(env, tokenRequired)
	default:
		mode := tokenRequired
		if name == "status" { // status reports a broken token setting
			mode = tokenOptional
		}
		var stop func()
		if c, stop, err = f.open(ctx, env, mode); stop != nil {
			defer stop()
		}
	}
	if err != nil {
		return err
	}

	switch name {
	case "search":
		if len(pos) == 0 {
			return &UsageError{name, "search needs <words>"}
		}
		return c.search(ctx, strings.Join(pos, " "))
	case "lookup":
		if len(pos) != 1 {
			return &UsageError{name, "lookup takes one <id>"}
		}
		return c.lookup(ctx, pos[0])
	case "skills":
		return c.skills(ctx, verb, itemName)
	case "agents":
		return c.agents(ctx, verb, itemName)
	case "decisions":
		return c.decisions(ctx, verb, itemName)
	case "loops":
		return c.loops(ctx, verb, itemName)
	case "status":
		if len(pos) != 0 {
			return &UsageError{name, "status takes no arguments"}
		}
		return c.status(ctx)
	case "init":
		if len(pos) != 0 {
			return &UsageError{name, "init takes no arguments"}
		}
		return c.initProject(ctx)
	case "instructions":
		return c.instructions(ctx, pos[0])
	case "pull":
		return c.pull(ctx)
	default:
		if len(pos) != 0 {
			return &UsageError{name, "version takes no arguments"}
		}
		return c.version(ctx)
	}
}

// ClientFlags are the client settings: the server address and the API token,
// or the content directory of an embedded server.
// Every client command reads them the same way, and so does any other role
// that talks to the API as a client (goldie mcp --transport stdio).
type ClientFlags struct {
	s     settings
	local bool // Open started an embedded server
}

// BindClientFlags registers --addr, --dir and --token on fs.
func BindClientFlags(fs *flag.FlagSet) *ClientFlags {
	f := &ClientFlags{}
	fs.StringVar(&f.s.addr, "addr", "", "server address")
	fs.StringVar(&f.s.token, "token", "", "API token")
	fs.StringVar(&f.s.dir, "dir", "", "serve content from this directory on an embedded server")
	return f
}

// Open returns the server address and the API token for the settings, after
// fs is parsed, for contract.NewAPIClient. A flag overrides its variable:
// --addr over GOLDIE_ADDR, --token over GOLDIE_TOKEN or GOLDIE_TOKEN_FILE,
// --dir over GOLDIE_DIR. With a directory (local mode) it starts the embedded
// server and returns its address and token. stop ends it and is never nil
// without an error; the caller runs it when done. A missing or invalid setting
// is an error that names it and shows no value. The address check is for
// startup only: a role that resolves its settings before it serves fails
// there, with the CLI's wording. contract.NewAPIClient checks the address
// again, so Open is not what keeps a bad one from the client.
func (f *ClientFlags) Open(ctx context.Context, env Env) (addr, token string, stop func(), err error) {
	c, stop, err := f.open(ctx, env, tokenRequired)
	if err != nil {
		return "", "", nil, err
	}
	if err := c.check(true); err != nil {
		stop()
		return "", "", nil, err
	}
	f.local = c.local
	return c.s.addr, c.s.token, stop, nil
}

// Local reports whether Open started an embedded server over local content,
// so a role words a failure to reach it without pointing at an address the
// user never gave.
func (f *ClientFlags) Local() bool { return f.local }

// open resolves the settings and, in local mode, starts the embedded server
// and takes its address and token. stop is never nil without an error.
func (f *ClientFlags) open(ctx context.Context, env Env, tm tokenMode) (*commander, func(), error) {
	dir, err := f.localDir(env)
	if err != nil {
		return nil, nil, err
	}
	goldieDir, dir, content, err := f.localScope(env, dir)
	if err != nil {
		return nil, nil, err
	}
	closeContent := func() {
		if content != nil {
			_ = content.Close()
		}
	}
	local := dir != "" || goldieDir != ""
	mode := tm
	if local {
		mode = tokenNone
	}
	c, err := f.commander(env, mode)
	if err != nil {
		closeContent()
		return nil, nil, err
	}
	if !local {
		return c, func() {}, nil
	}
	// Open the content directory once, here, and serve only through the handle:
	// a path swapped for a symlink after the checks above cannot redirect it.
	// The config.yaml directory arrives already open, through the repository root.
	if dir != "" && content == nil {
		if content, err = os.OpenRoot(dir); err != nil {
			return nil, nil, errors.New("content does not load: " + unreadableContentDir)
		}
	}
	var addr, token string
	var stop func()
	switch {
	case goldieDir != "" && env.StartLocalScope != nil:
		addr, token, stop, err = env.StartLocalScope(ctx, content, goldieDir)
	case goldieDir == "" && env.StartLocal != nil:
		addr, token, stop, err = env.StartLocal(ctx, content)
	default:
		closeContent()
		return nil, nil, errors.New("local mode is not available")
	}
	if err != nil {
		closeContent()
		return nil, nil, err
	}
	c.s.addr, c.s.token, c.local = addr, token, true
	if goldieDir != "" {
		c.localWarnings(ctx)
	}
	return c, func() {
		stop()
		closeContent()
	}, nil
}

// localWarnings prints the local Scope's load warnings, such as a decision
// file that is malformed and so not served, to stderr, one "Warning: <file>:
// <message>" line each. The embedded server's token is an operator's, so the
// sync route answers; a failure here is not reported, the command goes on.
func (c *commander) localWarnings(ctx context.Context) {
	cl, err := c.client(true)
	if err != nil {
		return
	}
	resp, err := cl.GetSyncWithResponse(ctx)
	if err != nil || resp.JSON200 == nil {
		return
	}
	for _, w := range resp.JSON200.Warnings {
		_, _ = fmt.Fprintln(c.errOut, "Warning: "+contract.CleanLine(w.File+": "+w.Message))
	}
}

// localScope finds the repository's local Scope from the working directory:
// goldieDir is its .goldie directory, set when .goldie/articles exists at the
// repository root, or config.yaml sets decisions.path. dir is the content
// directory given by --dir or GOLDIE_DIR, or else the one config.yaml names,
// which comes back already open as handle (project.OpenConfigContentDir), or
// "" for empty content. A .goldie without articles and without decisions.path,
// such as the one goldie init writes, is not a local Scope: dir comes back as
// it was given. An address together with a local Scope is an error naming
// both, like one with a directory, because the address would hide the
// repository's own content.
func (f *ClientFlags) localScope(env Env, dir string) (goldieDir, content string, handle *os.Root, err error) {
	wd, werr := os.Getwd()
	if werr != nil {
		return "", dir, nil, nil
	}
	root, ok := project.LocalRoot(wd)
	if !ok {
		return "", dir, nil, nil
	}
	if dir == "" {
		addrName := "--addr"
		if f.s.addr == "" {
			if v, _ := env.Lookup(EnvAddr); v != "" {
				addrName = EnvAddr
			} else {
				addrName = ""
			}
		}
		if addrName != "" {
			return "", "", nil, fmt.Errorf("%s is set and the repository has .goldie/articles: use %s for a running server, or unset it for the local content, not both", addrName, addrName)
		}
		if handle, err = project.OpenConfigContentDir(root); err != nil {
			return "", "", nil, err
		}
	}
	return project.GoldieDir(root), dir, handle, nil
}

// localDir returns the content directory of local mode, or "" for remote
// mode. --dir overrides GOLDIE_DIR. A directory together with an address is
// an error naming both settings by how each was given, and no value.
func (f *ClientFlags) localDir(env Env) (string, error) {
	dir, dirName := f.s.dir, "--dir"
	if dir == "" {
		dir, _ = env.Lookup(EnvDir)
		dirName = EnvDir
	}
	if dir == "" {
		return "", nil
	}
	addrName := "--addr"
	if f.s.addr == "" {
		// An address from the environment yields to an explicit --dir, as a
		// flag overrides a variable; two variables are a conflict.
		if v, _ := env.Lookup(EnvAddr); v == "" || dirName == "--dir" {
			return dir, nil
		}
		addrName = EnvAddr
	}
	return "", fmt.Errorf("%s and %s are both set: use %s for a running server or %s for local content, not both", dirName, addrName, addrName, dirName)
}

// tokenMode says how commander reads the token variables.
type tokenMode int

const (
	tokenNone     tokenMode = iota // not read: local mode has no token
	tokenRequired                  // read; a broken setting is an error
	tokenOptional                  // read; a broken setting is kept as commander.tokenBroken, so version and status keep working
)

// commander resolves the settings from the flags and env. --token wins over
// the variables, which are read as the mode says.
func (f *ClientFlags) commander(env Env, mode tokenMode) (*commander, error) {
	s := f.s
	if s.addr == "" {
		s.addr, _ = env.Lookup(EnvAddr)
	}
	broken := ""
	if s.token == "" && mode != tokenNone {
		s.token, broken = readToken(env)
		if broken != "" && mode == tokenRequired {
			return nil, errors.New(broken)
		}
	}
	errOut := env.Err
	if errOut == nil {
		errOut = io.Discard
	}
	return &commander{s: s, out: env.Out, errOut: errOut, env: env, tokenBroken: broken}, nil
}

// readToken reads the token from GOLDIE_TOKEN or GOLDIE_TOKEN_FILE. A setting
// that cannot give a token (both set, or a token file that is unreadable,
// empty or blank) comes back as broken: words that name the
// setting and never the path, the error text or the content. The token is then
// "".
func readToken(env Env) (token, broken string) {
	_, hasVal := env.Lookup(EnvToken)
	_, hasFile := env.Lookup(EnvToken + "_FILE")
	tok, err := platform.Setting(env.Lookup, env.ReadFile, EnvToken)
	switch {
	case hasVal && hasFile:
		return "", EnvToken + " and " + EnvToken + "_FILE are both set: set only one"
	case err != nil || (hasFile && strings.TrimSpace(tok) == ""):
		return "", EnvToken + "_FILE names a token file that can't be used: it is unreadable, empty or blank"
	}
	return tok, ""
}

// ErrNoAddress is the missing address. Its text names the two settings; the
// entry point adds the next command (platform.FirstRunHint) on its own line,
// as it does for the server. init words the reason itself, without --dir.
var ErrNoAddress = errors.New("no server address: set --addr or " + EnvAddr + ", or read a content directory with --dir")

const noAddressInit = "no server address: set --addr or " + EnvAddr

// commander holds what the commands share.
type commander struct {
	s      settings
	out    io.Writer // results
	errOut io.Writer // warnings; env.Err, or io.Discard when it is nil
	env    Env
	local  bool // an embedded server over local content, not a remote one
	// tokenBroken words a token setting that cannot be used (readToken), for
	// version and status to show instead of "no token given"; "" when fine.
	tokenBroken string
}

// check reports a missing or invalid address and, with withToken, a missing
// token.
func (c *commander) check(withToken bool) error {
	if c.s.addr == "" {
		return ErrNoAddress
	}
	if err := contract.CheckBaseURL(c.s.addr); err != nil {
		return err
	}
	if withToken && c.s.token == "" {
		return fmt.Errorf("no token: set --token, %s or %s_FILE", EnvToken, EnvToken)
	}
	return nil
}

// client builds the hardened API client for the configured address. With
// withToken it sends the token as a bearer token and fails when there is none;
// without, it sends no token, for status and version, which still answer
// without one and then report less.
func (c *commander) client(withToken bool) (*contract.ClientWithResponses, error) {
	if err := c.check(withToken); err != nil {
		return nil, err
	}
	token := ""
	if withToken {
		token = c.s.token
	}
	return contract.NewAPIClient(c.s.addr, token, platform.Version, c.env.Client)
}

// unreadableContentDir is engine.ErrContentDirUnreadable and its hint. cli may
// not import engine, so the wording is repeated here; the tests in cmd/goldie
// hold both to the same text.
const unreadableContentDir = "content directory is not readable: check that the path you passed exists and is a readable directory"
