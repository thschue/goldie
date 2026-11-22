package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/thschue/goldie/internal/contract"
	"github.com/thschue/goldie/internal/platform"
)

// Environment variables of the client commands. The token is a secret, so it
// also has a _FILE form (PC-05). They are registered in
// platform.ClientSettings, which the configuration reference is rendered from.
const (
	EnvAddr  = platform.EnvAddr
	EnvToken = platform.EnvToken
	// EnvProject names the Project, overriding detection from git remotes.
	EnvProject = platform.EnvProject
)

// Usage is the one-line usage of the client commands, without the program
// name, so the dispatcher can list it after its own commands.
const Usage = "search <words> [--project ID] | lookup <id> [--project ID] | status [--project ID] | init [--project ID] | version  [--addr URL] [--token TOKEN] [--json]"

// TokenNote follows the usage text: it recommends the token variables over
// --token, whose value shows in the process list.
const TokenNote = "Prefer " + EnvToken + " or " + EnvToken + "_FILE to --token: a --token value shows in the process list (ps) of the machine"

// IsCommand reports whether name is a client command, for the dispatcher in
// cmd/goldie.
func IsCommand(name string) bool {
	switch name {
	case "search", "lookup", "status", "init", "version":
		return true
	}
	return false
}

// Env is what a command reads from its surroundings, so tests need no globals.
type Env struct {
	Lookup   platform.Lookup              // environment variables
	ReadFile func(string) ([]byte, error) // reads a _FILE secret
	Out      io.Writer                    // results
	Client   *http.Client                 // nil: contract's client, with a timeout
}

// DefaultEnv is the process environment.
func DefaultEnv(out io.Writer) Env {
	return Env{Lookup: os.LookupEnv, ReadFile: os.ReadFile, Out: out}
}

// settings are the flags and variables common to every command.
type settings struct {
	addr, token, project string
	json                 bool
}

// Run runs one client command. args[0] is the command name.
func Run(ctx context.Context, args []string, env Env) error {
	if len(args) == 0 || !IsCommand(args[0]) {
		return errors.New("usage: goldie " + Usage + "\n" + TokenNote)
	}
	name := args[0]
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := BindClientFlags(fs)
	s := &f.s
	if name != "version" { // version has no use for a Project
		fs.StringVar(&s.project, "project", "", "Project as host/org/repo")
	}
	if name != "init" { // init prints three fixed lines
		fs.BoolVar(&s.json, "json", false, "print JSON")
	}
	pos, err := parseInterleaved(fs, args[1:])
	if err != nil {
		return fmt.Errorf("%w\nusage: goldie %s\n%s", err, Usage, TokenNote)
	}
	// version needs no token, and must keep working where the token settings
	// are absent or broken.
	c, err := f.commander(env, name != "version")
	if err != nil {
		return err
	}

	switch name {
	case "search":
		if len(pos) == 0 {
			return errors.New("usage: goldie search <words>")
		}
		return c.search(ctx, strings.Join(pos, " "))
	case "lookup":
		if len(pos) != 1 {
			return errors.New("usage: goldie lookup <id>")
		}
		return c.lookup(ctx, pos[0])
	case "status":
		if len(pos) != 0 {
			return errors.New("usage: goldie status")
		}
		return c.status(ctx)
	case "init":
		if len(pos) != 0 {
			return errors.New("usage: goldie init [--project ID]")
		}
		return c.initProject(ctx)
	default:
		if len(pos) != 0 {
			return errors.New("usage: goldie version")
		}
		return c.version(ctx)
	}
}

// ClientFlags are the client settings: the server address and the API token.
// Every client command reads them the same way, and so does any other role
// that talks to the API as a client (goldie mcp --transport stdio).
type ClientFlags struct{ s settings }

// BindClientFlags registers --addr and --token on fs.
func BindClientFlags(fs *flag.FlagSet) *ClientFlags {
	f := &ClientFlags{}
	fs.StringVar(&f.s.addr, "addr", "", "server address")
	fs.StringVar(&f.s.token, "token", "", "API token")
	return f
}

// Resolve returns the server address and the API token for the settings,
// after fs is parsed, for contract.NewAPIClient. A flag overrides its
// variable: --addr over GOLDIE_ADDR, --token over GOLDIE_TOKEN or
// GOLDIE_TOKEN_FILE. A missing or invalid setting is an error that names it
// and shows no value. The address check is for startup only: a role that
// resolves its settings before it serves fails there, with the CLI's wording.
// contract.NewAPIClient checks the address again, so Resolve is not what keeps
// a bad one from the client.
func (f *ClientFlags) Resolve(env Env) (addr, token string, err error) {
	c, err := f.commander(env, true)
	if err != nil {
		return "", "", err
	}
	if err := c.check(true); err != nil {
		return "", "", err
	}
	return c.s.addr, c.s.token, nil
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
	return &commander{s: s, out: env.Out, env: env}, nil
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
	s   settings
	out io.Writer
	env Env
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
