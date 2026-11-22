package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

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
const Usage = "search <words> | lookup <id> | status [--project ID] | version  [--addr URL] [--token TOKEN] [--json]"

// requestTimeout bounds every API call.
const requestTimeout = 30 * time.Second

// maxResponseBytes bounds the body of every API answer the client reads.
const maxResponseBytes = 16 << 20

// errTooLarge is returned by a body that exceeds maxResponseBytes.
var errTooLarge = errors.New("response body too large")

// IsCommand reports whether name is a client command, for the dispatcher in
// cmd/goldie.
func IsCommand(name string) bool {
	switch name {
	case "search", "lookup", "status", "version":
		return true
	}
	return false
}

// Env is what a command reads from its surroundings, so tests need no globals.
type Env struct {
	Lookup   platform.Lookup              // environment variables
	ReadFile func(string) ([]byte, error) // reads a _FILE secret
	Out      io.Writer                    // results
	Client   *http.Client                 // nil: a client with a timeout
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
		return errors.New("usage: goldie " + Usage)
	}
	name := args[0]
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := BindClientFlags(fs)
	s := &f.s
	if name == "status" { // only status reads the Project until #8
		fs.StringVar(&s.project, "project", "", "Project as host/org/repo")
	}
	fs.BoolVar(&s.json, "json", false, "print JSON")
	pos, err := parseInterleaved(fs, args[1:])
	if err != nil {
		return fmt.Errorf("%w\nusage: goldie %s", err, Usage)
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

// Client returns the API client for the settings, after fs is parsed. A flag
// overrides its variable: --addr over GOLDIE_ADDR, --token over GOLDIE_TOKEN
// or GOLDIE_TOKEN_FILE. Every call sends the token, every answer is capped at
// 16 MiB and every request has a timeout, as in the client commands. A missing
// or invalid setting is an error that names it and shows no value.
func (f *ClientFlags) Client(env Env) (*contract.ClientWithResponses, error) {
	c, err := f.commander(env, true)
	if err != nil {
		return nil, err
	}
	return c.client(true)
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
	httpc := &http.Client{Timeout: requestTimeout}
	if env.Client != nil {
		httpc = env.Client
	}
	limited := *httpc
	limited.Transport = limitTransport{base: httpc.Transport}
	return &commander{s: s, out: env.Out, http: &limited, env: env}, nil
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
	s    settings
	out  io.Writer
	http *http.Client
	env  Env
}

// client builds the generated client for the configured address. With
// withToken it sends the token as a bearer token and fails when there is none;
// without, it sends no token, for the public routes such as /openapi.json.
func (c *commander) client(withToken bool) (*contract.ClientWithResponses, error) {
	if c.s.addr == "" {
		return nil, fmt.Errorf("no server address: set --addr or %s", EnvAddr)
	}
	u, err := url.Parse(c.s.addr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("the server address must be an http:// or https:// URL")
	}
	if withToken && c.s.token == "" {
		return nil, fmt.Errorf("no token: set --token, %s or %s_FILE", EnvToken, EnvToken)
	}
	opts := []contract.ClientOption{contract.WithHTTPClient(c.http)}
	if withToken {
		token := c.s.token
		opts = append(opts, contract.WithRequestEditorFn(func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer "+token)
			return nil
		}))
	}
	return contract.NewClientWithResponses(strings.TrimRight(c.s.addr, "/"), opts...)
}

// limitTransport caps every response body at maxResponseBytes, so a hostile
// or broken server cannot make the client buffer an unbounded answer.
type limitTransport struct{ base http.RoundTripper }

func (t limitTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	resp.Body = &limitedBody{rc: resp.Body, left: maxResponseBytes}
	return resp, nil
}

// limitedBody reads at most left bytes and fails with errTooLarge when the
// body has more.
type limitedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		var probe [1]byte
		n, err := b.rc.Read(probe[:])
		if n > 0 {
			return 0, errTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	return n, err
}

func (b *limitedBody) Close() error { return b.rc.Close() }
