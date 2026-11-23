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
var Commands = []string{"search", "lookup", "status", "init", "version"}

// IsCommand reports whether name is one of Commands.
func IsCommand(name string) bool { return slices.Contains(Commands, name) }

// UsageError is a command used wrongly; the dispatcher exits 2 for it.
type UsageError struct {
	Command string // the command that was misused
	Msg     string
}

func (e *UsageError) Error() string { return e.Msg }

// Env is what a command reads from its surroundings, so tests need no globals.
type Env struct {
	Lookup   platform.Lookup              // environment variables
	ReadFile func(string) ([]byte, error) // reads a _FILE secret
	Out      io.Writer                    // results
	Err      io.Writer                    // warnings, such as a deprecated article's; nil discards them
	Client   *http.Client                 // nil: contract's client, with a timeout
	// StartLocal starts an embedded server over the content at dir and returns
	// its address and token. stop ends it. cmd injects app.StartLocal; nil
	// means local mode is not available.
	StartLocal func(ctx context.Context, dir string) (addr, token string, stop func(), err error)
	// StartLocalScope is StartLocal for a repository with a local Scope:
	// goldieDir is the absolute path of its .goldie directory, and dir the
	// content directory under it, "" for none. cmd injects it from app.StartLocal.
	StartLocalScope func(ctx context.Context, dir, goldieDir string) (addr, token string, stop func(), err error)
}

// DefaultEnv is the process environment.
func DefaultEnv(out io.Writer) Env {
	return Env{Lookup: os.LookupEnv, ReadFile: os.ReadFile, Out: out, Err: os.Stderr}
}

// settings are the flags and variables common to every command.
type settings struct {
	addr, token, project string
	dir                  string // content directory of local mode
	json                 bool
}

// Run runs one client command. args[0] is the command name. Its flags may
// come before, between or after the positional words.
func Run(ctx context.Context, args []string, env Env) error {
	if len(args) == 0 || !IsCommand(args[0]) {
		return errors.New("usage: goldie search | lookup | status | init | version")
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	exec := Command(args[0], fs)
	pos, err := parseInterleaved(fs, args[1:])
	if err != nil {
		return fmt.Errorf("%w\nusage: goldie %s [flags]\n%s", err, args[0], TokenNote)
	}
	return exec(ctx, pos, env)
}

// Command registers the flags of the client command name (an IsCommand) on fs
// and returns what runs it once fs is parsed, by Run or by cmd/goldie.
func Command(name string, fs *flag.FlagSet) func(ctx context.Context, pos []string, env Env) error {
	f := BindClientFlags(fs)
	if name != "version" { // version has no use for a Project
		fs.StringVar(&f.s.project, "project", "", "Project as host/org/repo")
	}
	if name != "init" { // init prints three fixed lines
		fs.BoolVar(&f.s.json, "json", false, "print JSON")
	}
	return func(ctx context.Context, pos []string, env Env) error {
		return f.run(ctx, name, pos, env)
	}
}

func (f *ClientFlags) run(ctx context.Context, name string, pos []string, env Env) error {
	var err error
	// version needs no token, and must keep working where the token settings
	// are absent or broken.
	// init checks registration against a running server only: it writes into
	// the repository and has no use for an embedded server (#282 is silent on
	// local mode), so like version it ignores --dir and GOLDIE_DIR.
	var c *commander
	switch name {
	case "version":
		c, err = f.commander(env, false)
	case "init":
		c, err = f.commander(env, true)
	default:
		var stop func()
		if c, stop, err = f.open(ctx, env); stop != nil {
			defer stop()
		}
	}
	if err != nil {
		return err
	}

	switch name {
	case "search":
		if len(pos) == 0 {
			return &UsageError{name, "usage: goldie search <words>"}
		}
		return c.search(ctx, strings.Join(pos, " "))
	case "lookup":
		if len(pos) != 1 {
			return &UsageError{name, "usage: goldie lookup <id>"}
		}
		return c.lookup(ctx, pos[0])
	case "status":
		if len(pos) != 0 {
			return &UsageError{name, "usage: goldie status"}
		}
		return c.status(ctx)
	case "init":
		if len(pos) != 0 {
			return &UsageError{name, "usage: goldie init [--project ID]"}
		}
		return c.initProject(ctx)
	default:
		if len(pos) != 0 {
			return &UsageError{name, "usage: goldie version"}
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
	c, stop, err := f.open(ctx, env)
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
func (f *ClientFlags) open(ctx context.Context, env Env) (*commander, func(), error) {
	dir, err := f.localDir(env)
	if err != nil {
		return nil, nil, err
	}
	goldieDir, dir, err := f.localScope(env, dir)
	if err != nil {
		return nil, nil, err
	}
	local := dir != "" || goldieDir != ""
	c, err := f.commander(env, !local)
	if err != nil {
		return nil, nil, err
	}
	if !local {
		return c, func() {}, nil
	}
	var addr, token string
	var stop func()
	switch {
	case goldieDir != "" && env.StartLocalScope != nil:
		addr, token, stop, err = env.StartLocalScope(ctx, dir, goldieDir)
	case goldieDir == "" && env.StartLocal != nil:
		addr, token, stop, err = env.StartLocal(ctx, dir)
	default:
		return nil, nil, errors.New("local mode is not available")
	}
	if err != nil {
		return nil, nil, err
	}
	c.s.addr, c.s.token, c.local = addr, token, true
	return c, stop, nil
}

// localScope finds the repository's local Scope from the working directory:
// goldieDir is its .goldie directory, set when .goldie/articles exists at the
// repository root. dir is the content directory given by --dir or GOLDIE_DIR,
// or else the one config.yaml names (project.ConfigContentDir), or "" for
// empty content. A .goldie without articles, such as the one goldie init
// writes, is not a local Scope: dir comes back as it was given. An address
// together with a local Scope is an error naming both, like one with a
// directory, because the address would hide the repository's own content.
func (f *ClientFlags) localScope(env Env, dir string) (goldieDir, content string, err error) {
	wd, werr := os.Getwd()
	if werr != nil {
		return "", dir, nil
	}
	root, ok := project.LocalRoot(wd)
	if !ok {
		return "", dir, nil
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
			return "", "", fmt.Errorf("%s is set and the repository has .goldie/articles: use %s for a running server, or unset it for the local content, not both", addrName, addrName)
		}
		if dir, err = project.ConfigContentDir(root); err != nil {
			return "", "", err
		}
	}
	return project.GoldieDir(root), dir, nil
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

// commander resolves the settings from the flags and env. With needToken it
// reads the token variables, which may fail.
func (f *ClientFlags) commander(env Env, needToken bool) (*commander, error) {
	s := f.s
	if s.addr == "" {
		s.addr, _ = env.Lookup(EnvAddr)
	}
	if s.token == "" && needToken {
		var err error
		if s.token, err = platform.Setting(env.Lookup, env.ReadFile, EnvToken); err != nil {
			return nil, err
		}
	}
	errOut := env.Err
	if errOut == nil {
		errOut = io.Discard
	}
	return &commander{s: s, out: env.Out, errOut: errOut, env: env}, nil
}

// parseInterleaved parses flags that may come before, between or after the
// positional arguments, and returns the positional ones.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// commander holds what the commands share.
type commander struct {
	s      settings
	out    io.Writer // results
	errOut io.Writer // warnings; env.Err, or io.Discard when it is nil
	env    Env
	local  bool // an embedded server over local content, not a remote one
}

// check reports a missing or invalid address and, with withToken, a missing
// token.
func (c *commander) check(withToken bool) error {
	if c.s.addr == "" {
		return fmt.Errorf("no server address: set --addr or %s", EnvAddr)
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
